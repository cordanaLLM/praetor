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
	"gopkg.in/yaml.v3"
)

const (
	goreleaserActionPath = "goreleaser/goreleaser-action"
	maxRunScriptFields   = 4096
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
	// GitHub resolves owner and repository names case-insensitively.
	action, _, _ := strings.Cut(strings.ToLower(strings.TrimSpace(step.Uses)), "@")
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
	fields := strings.Fields(withoutShellComments(step.Run))
	if len(fields) > maxRunScriptFields {
		return false, fmt.Errorf("run script exceeds %d fields", maxRunScriptFields)
	}
	if runInvokesSBOMGenerator(fields) {
		return true, nil
	}
	if at := fieldIndex(fields, "goreleaser"); at >= 0 {
		return goreleaserReleaseGeneratesSBOM(ctx, repoPath, fields[at+1:])
	}
	return false, nil
}

// withoutShellComments drops whole-line shell comments, so a script that only mentions a
// generator in prose is not mistaken for one that runs it.
func withoutShellComments(script string) string {
	lines := strings.Split(script, "\n")
	kept := make([]string, 0, len(lines))
	for i := 0; i < len(lines) && i < maxRunScriptFields; i++ {
		if !strings.HasPrefix(strings.TrimSpace(lines[i]), "#") {
			kept = append(kept, lines[i])
		}
	}
	return strings.Join(kept, "\n")
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
		case i+1 < len(fields) && fields[i+1] == "sbom" && isPraetorBinary(fields[i]):
			return true
		}
	}
	return false
}

// hasOutputFlag reports whether a Syft invocation names an output document; without one
// Syft prints a table to the terminal and writes no SBOM.
func hasOutputFlag(fields []string) bool {
	for i := 0; i < len(fields) && i < maxRunScriptFields; i++ {
		f := fields[i]
		if f == "-o" || f == "--output" || strings.HasPrefix(f, "-o=") || strings.HasPrefix(f, "--output=") {
			return true
		}
		if f == "&&" || f == ";" || f == "|" {
			return false
		}
	}
	return false
}

// isPraetorBinary reports whether a field names this tool's binary under either name,
// including a path such as ./bin/praetorctl or go run ./cmd/standardsctl.
func isPraetorBinary(field string) bool {
	base := filepath.Base(filepath.FromSlash(field))
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
	if fieldIndex(args, "release") < 0 {
		return false, nil
	}
	candidates := goreleaserConfigNames[:]
	if explicit := goreleaserConfigArg(args); explicit != "" {
		candidates = []string{explicit}
	}
	for i := 0; i < len(candidates) && i < len(goreleaserConfigNames); i++ {
		declares, found, err := goreleaserConfigDeclaresSBOMs(ctx, repoPath, candidates[i])
		if err != nil || found {
			return declares, err
		}
	}
	return false, nil
}

// goreleaserConfigArg returns the configuration file GoReleaser args name with --config or
// -f, or "" when they name none.
func goreleaserConfigArg(args []string) string {
	for i := 0; i < len(args) && i < maxRunScriptFields; i++ {
		if value, ok := strings.CutPrefix(args[i], "--config="); ok {
			return value
		}
		if (args[i] == "--config" || args[i] == "-f") && i+1 < len(args) {
			return args[i+1]
		}
	}
	return ""
}

// goreleaserConfigDeclaresSBOMs reads one GoReleaser configuration file under repoPath.
// found is false when the file does not exist, so the caller can try the next candidate;
// a path outside the repository is treated as absent rather than read.
func goreleaserConfigDeclaresSBOMs(ctx context.Context, repoPath, rel string) (declares, found bool, err error) {
	if !filepath.IsLocal(filepath.FromSlash(rel)) {
		return false, false, nil
	}
	data, err := contextopt.ReadSnapshot(ctx, filepath.Join(repoPath, filepath.FromSlash(rel)))
	if errors.Is(err, os.ErrNotExist) {
		return false, false, nil
	}
	if err != nil {
		return false, false, fmt.Errorf("read %s: %w", rel, err)
	}
	var config struct {
		SBOMs []yaml.Node `yaml:"sboms"`
	}
	if err := yaml.Unmarshal(data, &config); err != nil {
		return false, true, fmt.Errorf("parse %s: %w", rel, err)
	}
	return len(config.SBOMs) > 0, true, nil
}
