package forge

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/cordanaLLM/praetor/internal/contextopt"
	"github.com/cordanaLLM/praetor/internal/util"
	"gopkg.in/yaml.v3"
)

const (
	goreleaserActionPath = "goreleaser/goreleaser-action"
	maxRunScriptFields   = 4096
	// praetorNoticesSubcommand is the sbom subcommand that renders THIRD-PARTY-NOTICES.md
	// tables and writes no SBOM document. runSBOM in cmd/standardsctl/supplychain.go
	// dispatches to it only when it is the first argument after sbom.
	praetorNoticesSubcommand = "notices"
)

// sbomGeneratorActions are the actions, lower-cased, whose step writes or attests an SBOM
// document on its own. anchore/sbom-action/download-syft is deliberately absent: it installs
// Syft and catalogues nothing, so a workflow holding only that step produces no SBOM.
var sbomGeneratorActions = map[string]bool{
	"anchore/sbom-action": true,
	"actions/attest-sbom": true,
}

// goreleaserConfigNames are the files GoReleaser loads when no --config is given, in its
// own search order (cmd/config.go loadConfigCheck in goreleaser/goreleaser).
var goreleaserConfigNames = [...]string{
	".config/goreleaser.yml",
	".config/goreleaser.yaml",
	".goreleaser.yml",
	".goreleaser.yaml",
	"goreleaser.yml",
	"goreleaser.yaml",
}

// SBOMWorkflow returns the name of the first workflow under .github/workflows, in name
// order, with a step that generates an SBOM, or "" when none does.
//
// The file name is not the evidence. A dedicated sbom.yml and an SBOM step inside the
// release workflow both count, because producing the SBOM in the job that builds the
// release is what keeps it describing the released archives; an sbom.yml that runs no
// generator counts for nothing (#43, #315).
func SBOMWorkflow(ctx context.Context, repoPath string) (string, error) {
	if ctx == nil {
		return "", errors.New("SBOM workflow discovery requires a context")
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	files, err := readWorkflowFiles(ctx, repoPath)
	if err != nil {
		return "", err
	}
	for i := 0; i < len(files) && i < maxWorkflowFiles; i++ {
		generates, err := workflowGeneratesSBOM(ctx, repoPath, files[i].Data)
		if err != nil {
			return "", fmt.Errorf("workflow %s: %w", files[i].Name, err)
		}
		if generates {
			return files[i].Name, nil
		}
	}
	return "", nil
}

// workflowGeneratesSBOM reports whether any step of one workflow document generates an SBOM.
func workflowGeneratesSBOM(ctx context.Context, repoPath string, data []byte) (bool, error) {
	var spec workflowSpec
	if err := yaml.Unmarshal(data, &spec); err != nil {
		return false, fmt.Errorf("parse: %w", err)
	}
	if len(spec.Jobs) > maxJobsPerFile {
		return false, fmt.Errorf("workflow exceeds %d jobs", maxJobsPerFile)
	}
	for _, job := range spec.Jobs {
		for i := 0; i < len(job.Steps) && i < maxStepsPerJob; i++ {
			generates, err := stepGeneratesSBOM(ctx, repoPath, job.Steps[i])
			if err != nil || generates {
				return generates, err
			}
		}
	}
	return false, nil
}

// stepGeneratesSBOM reports whether one step writes an SBOM: an SBOM action, a run script
// invoking a generator, or a GoReleaser release whose configuration declares sboms.
func stepGeneratesSBOM(ctx context.Context, repoPath string, step workflowStep) (bool, error) {
	action := actionPath(step.Uses)
	if sbomGeneratorActions[action] {
		return true, nil
	}
	if action == goreleaserActionPath {
		// Without args the action names no command, so it is not read as a release.
		args, ok := step.With["args"].(string)
		if !ok {
			return false, nil
		}
		return goreleaserReleaseGeneratesSBOM(ctx, repoPath, strings.Fields(args))
	}
	fields, err := scriptFields(step.Run)
	if err != nil {
		return false, err
	}
	if runInvokesSBOMGenerator(fields) {
		return true, nil
	}
	if at := fieldIndex(fields, "goreleaser"); at >= 0 {
		return goreleaserReleaseGeneratesSBOM(ctx, repoPath, fields[at+1:])
	}
	return false, nil
}

// actionPath returns the action a step's uses: names, lower-cased and without its @ref. GitHub
// resolves owner and repository names case-insensitively.
func actionPath(uses string) string {
	action, _, _ := strings.Cut(strings.ToLower(strings.TrimSpace(uses)), "@")
	return action
}

// scriptFields splits a run: script into its whitespace-separated fields. A command named only
// in a comment does not run, so comments are dropped first. A backslash-newline continues one
// command and any other newline ends it, so a newline becomes a ";" field, where the command
// readers stop (commandSegment).
func scriptFields(run string) ([]string, error) {
	script, err := util.StripHashComments(strings.ReplaceAll(run, "\r\n", "\n"))
	if err != nil {
		return nil, fmt.Errorf("run script: %w", err)
	}
	script = strings.ReplaceAll(strings.ReplaceAll(script, "\\\n", " "), "\n", " ; ")
	fields := strings.Fields(script)
	if len(fields) > maxRunScriptFields {
		return nil, fmt.Errorf("run script exceeds %d fields", maxRunScriptFields)
	}
	return fields, nil
}

// runInvokesSBOMGenerator reports whether a run script's fields invoke a generator that
// writes an SBOM document: Syft with an output format, cyclonedx-gomod, cdxgen, or this
// tool's own sbom command.
func runInvokesSBOMGenerator(fields []string) bool {
	for i := 0; i < len(fields) && i < maxRunScriptFields; i++ {
		switch {
		case fields[i] == "cyclonedx-gomod", fields[i] == "cdxgen":
			return true
		case fields[i] == "syft" && hasOutputFlag(fields[i+1:]):
			return true
		case invokesPraetorSBOM(fields[i:]):
			return true
		}
	}
	return false
}

// invokesPraetorSBOM reports whether fields start with this tool's sbom command in its
// generating form. sbom notices writes THIRD-PARTY-NOTICES.md, not an SBOM, so a step that
// runs only it is no evidence for require_sbom.
func invokesPraetorSBOM(fields []string) bool {
	if !invokesPraetor(fields, "sbom") {
		return false
	}
	return len(fields) == 2 || fields[2] != praetorNoticesSubcommand
}

// invokesPraetor reports whether fields start with this tool's binary running subcommand.
func invokesPraetor(fields []string, subcommand string) bool {
	return len(fields) >= 2 && fields[1] == subcommand && isPraetorBinary(fields[0])
}

// hasOutputFlag reports whether a Syft invocation names an output document; without one
// Syft prints a table to the terminal and writes no SBOM.
func hasOutputFlag(fields []string) bool {
	segment := commandSegment(fields)
	for i := 0; i < len(segment) && i < maxRunScriptFields; i++ {
		f := segment[i]
		if f == "-o" || f == "--output" || strings.HasPrefix(f, "-o=") || strings.HasPrefix(f, "--output=") {
			return true
		}
	}
	return false
}

// commandSegment returns fields up to the first one that ends the command: a ";" (which
// scriptFields also writes for a newline), a pipe, a list operator or a background "&".
func commandSegment(fields []string) []string {
	for i := 0; i < len(fields) && i < maxRunScriptFields; i++ {
		switch fields[i] {
		case ";", "|", "&&", "||", "&":
			return fields[:i]
		}
	}
	return fields
}

// flagValues returns every value fields give one of names, in order; "--name value" and
// "--name=value" both count. Callers pass one command's fields (commandSegment).
func flagValues(fields []string, names ...string) []string {
	var values []string
	for i := 0; i < len(fields) && i < maxRunScriptFields; i++ {
		for j := 0; j < len(names); j++ {
			if value, ok := strings.CutPrefix(fields[i], names[j]+"="); ok {
				values = append(values, value)
			} else if fields[i] == names[j] && i+1 < len(fields) {
				values = append(values, fields[i+1])
			}
		}
	}
	return values
}

// flagValue returns the first value fields give one of names (flagValues), or "".
func flagValue(fields []string, names ...string) string {
	if values := flagValues(fields, names...); len(values) > 0 {
		return values[0]
	}
	return ""
}

// commandName returns the program a command field names without its directory, so
// ./bin/praetorctl and /usr/local/bin/cosign compare by their base names.
func commandName(field string) string {
	return filepath.Base(filepath.FromSlash(field))
}

// isPraetorBinary reports whether a field names this tool's binary under either name,
// including a path such as ./bin/praetorctl or go run ./cmd/standardsctl.
func isPraetorBinary(field string) bool {
	base := commandName(field)
	return base == "praetorctl" || base == "standardsctl"
}

// fieldIndex returns the index of the first field equal to want, or -1.
func fieldIndex(fields []string, want string) int {
	for i := 0; i < len(fields) && i < maxRunScriptFields; i++ {
		if fields[i] == want {
			return i
		}
	}
	return -1
}

// goreleaserReleaseGeneratesSBOM reports whether GoReleaser invoked with args runs a release
// (or snapshot) whose configuration declares at least one sboms entry.
func goreleaserReleaseGeneratesSBOM(ctx context.Context, repoPath string, args []string) (bool, error) {
	config, found, err := goreleaserReleaseConfig(ctx, repoPath, args)
	return found && len(config.SBOMs) > 0, err
}

// goreleaserConfig is the part of a GoReleaser configuration the supply-chain readers decide
// on: the sboms block and the three signing blocks (goreleaser.com/customization/sign).
type goreleaserConfig struct {
	SBOMs       []yaml.Node      `yaml:"sboms"`
	Signs       []goreleaserSign `yaml:"signs"`
	BinarySigns []goreleaserSign `yaml:"binary_signs"`
	DockerSigns []goreleaserSign `yaml:"docker_signs"`
}

// goreleaserSign is one entry of a GoReleaser signing block: the program it runs and the
// artifacts it signs, each empty when the entry keeps its block's default.
type goreleaserSign struct {
	Cmd       string `yaml:"cmd"`
	Artifacts string `yaml:"artifacts"`
}

// goreleaserReleaseConfig returns the configuration GoReleaser invoked with args loads. found
// is false when args run no release (or snapshot) or no configuration file exists.
func goreleaserReleaseConfig(ctx context.Context, repoPath string, args []string) (config goreleaserConfig, found bool, err error) {
	if fieldIndex(args, "release") < 0 {
		return goreleaserConfig{}, false, nil
	}
	candidates := goreleaserConfigNames[:]
	if explicit := goreleaserConfigArg(args); explicit != "" {
		candidates = []string{explicit}
	}
	for i := 0; i < len(candidates) && i < len(goreleaserConfigNames); i++ {
		config, found, err = readGoreleaserConfig(ctx, repoPath, candidates[i])
		if err != nil || found {
			return config, found, err
		}
	}
	return goreleaserConfig{}, false, nil
}

// goreleaserConfigArg returns the configuration file GoReleaser args name with --config or
// -f, or "" when they name none.
func goreleaserConfigArg(args []string) string {
	return flagValue(args, "--config", "-f")
}

// readGoreleaserConfig reads one GoReleaser configuration file under repoPath. found is false
// when the file does not exist, so the caller can try the next candidate; a path outside the
// repository is treated as absent rather than read.
func readGoreleaserConfig(ctx context.Context, repoPath, rel string) (config goreleaserConfig, found bool, err error) {
	if !filepath.IsLocal(filepath.FromSlash(rel)) {
		return goreleaserConfig{}, false, nil
	}
	data, err := contextopt.ReadSnapshot(ctx, filepath.Join(repoPath, filepath.FromSlash(rel)))
	if errors.Is(err, os.ErrNotExist) {
		return goreleaserConfig{}, false, nil
	}
	if err != nil {
		return goreleaserConfig{}, false, fmt.Errorf("read %s: %w", rel, err)
	}
	if err := yaml.Unmarshal(data, &config); err != nil {
		return goreleaserConfig{}, true, fmt.Errorf("parse %s: %w", rel, err)
	}
	return config, true, nil
}
