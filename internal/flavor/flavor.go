package flavor

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"

	"gopkg.in/yaml.v3"

	"github.com/cordanaLLM/praetor/internal/util"
)

// TemplateItem defines a template file required by a flavor.
//
// Every template states where its content comes from, exactly one way: Source names an
// embedded body flavor apply renders, Producer names the command that writes the file
// instead, and ContentFunc is the hook a flavor registered from outside this package may
// supply. A template with none of the three has no content behind it, and flavor apply
// reports it as an error rather than writing a placeholder (#336).
type TemplateItem struct {
	Path        string `json:"path"`
	Description string `json:"description"`

	// Source is the body scaffolded at Path: a path below the repository's templates/
	// directory, such as "go/ci-go.yml.tmpl", rendered by templates.RenderFile. It used to
	// be a switch on the file's base name with a one-line "# <file> configuration" comment
	// as its default, so a workflow, a gitleaks config or a harness was scaffolded as a
	// comment and then scored present (BUG-028, BUG-029).
	Source string `json:"source,omitempty"`

	// Producer names the command that owns the file when flavor apply must not write it:
	// .standards.yaml is written by adoption from the operator's declared profile, and
	// CLAUDE.md is compiled from AGENTS.md. A second, flavor-side generator for either
	// would be a second implementation of one artifact (HISS-19), so flavor apply defers
	// these to their producer and the audit still requires them.
	Producer string `json:"producer,omitempty"`

	ContentFunc func(repoName string, owner string) string `json:"-"`

	// Requires reports what the repository lacks for the scaffolded body to work as written,
	// or "" when it lacks nothing. flavor apply writes no body whose requirement is unmet,
	// --force included, and lists it under ApplyReport.UnmetTemplates; the audit still
	// requires the file. A body is fixed text, so a workflow that runs `npm ci` can only pass
	// where CI's checkout holds package-lock.json, and adoption makes every job of a
	// scaffolded workflow a required status check: scaffolding it anywhere else hands the
	// repository a check no pull request can pass. The context bounds any probe the check
	// runs, such as asking Git whether a file it needs is committed.
	Requires func(ctx context.Context, repoPath string) string `json:"-"`

	// AltPaths lists equally valid alternatives to Path. A repository satisfies the
	// template when Path or any AltPath is present, and scaffolding is skipped in that
	// case. This exists because ecosystems rename their configuration without changing
	// its meaning: ESLint 9 replaced .eslintrc.json with eslint.config.*, and a workspace
	// commonly carries its compiler options in tsconfig.base.json. Demanding the older
	// name makes a conforming repository fail, and scaffolding it writes a second,
	// contradictory config that the toolchain then ignores.
	AltPaths []string `json:"alt_paths,omitempty"`

	// Validator decides whether a file at Path or an AltPath satisfies the template, the
	// way SettingItem.Validator does for settings. A template with no validator is
	// satisfied by a regular file alone. Presence used to be the whole check, so the
	// comment-only placeholder the scaffolder wrote scored the template compliant.
	Validator func(content []byte) bool `json:"-"`
}

// SettingItem defines a configuration setting required by a flavor.
type SettingItem struct {
	Name        string `json:"name"`
	Path        string `json:"path"`
	Description string `json:"description"`

	// Validator decides whether the file's content satisfies the setting. A setting with
	// no validator is satisfied by its presence alone, which is all that can be claimed
	// for a file with no checkable shape. The field used to be declared and never
	// assigned or called, so the audit counted a setting valid on existence while the
	// CLI printed "N/N valid": an empty lefthook.yml and a ruleset file holding prose
	// both scored as configuration.
	Validator func(content []byte) bool `json:"-"`
}

// validYAMLMapping reports whether content parses as a non-empty YAML mapping.
//
// A configuration file is a mapping of keys to values, so a scalar, a sequence, an empty
// document and a file that does not parse at all are all rejected: none of them configures
// the tool that reads the path. An empty mapping is rejected with them: `{}` parses, and a
// lefthook.yml holding it installs exactly as many hooks as a file that is not there.
func validYAMLMapping(content []byte) bool {
	var document map[string]any
	if err := yaml.Unmarshal(content, &document); err != nil {
		return false
	}
	return len(document) > 0
}

// validJSONObject reports whether content parses as a non-empty JSON object.
//
// Strict JSON, matching the line internal/clientsetup draws for the client configuration it
// merges: comments and trailing commas are rejected rather than tolerated, so a file this
// audit reports as valid is one every JSON consumer can also read. An object with no members
// is rejected for the same reason an empty YAML mapping is: `{}` configures nothing.
func validJSONObject(content []byte) bool {
	var document map[string]json.RawMessage
	if err := json.Unmarshal(content, &document); err != nil {
		return false
	}
	return len(document) > 0
}

// ToolchainItem defines an external CLI tool or compiler required by a flavor.
type ToolchainItem struct {
	Binary       string `json:"binary"`
	Purpose      string `json:"purpose"`
	InstallGuide string `json:"install_guide"`

	// AltBinaries lists other binaries that satisfy the same purpose, such as pnpm or
	// yarn in place of npm.
	AltBinaries []string `json:"alt_binaries,omitempty"`

	// ProjectLocal allows the tool to be resolved from the repository's own
	// node_modules/.bin rather than $PATH. Node projects pin their compilers as
	// devDependencies and invoke them through the package manager, so requiring a
	// global install reports a missing toolchain for a repository that builds fine.
	ProjectLocal bool `json:"project_local,omitempty"`
}

// Flavor represents an authoritative repository engineering archetype.
type Flavor interface {
	Name() string
	Description() string
	Detect(repoPath string) bool
	RequiredTemplates() []TemplateItem
	RequiredSettings() []SettingItem
	RequiredToolchains() []ToolchainItem
	HISSProfile() string
}

var (
	registryMu sync.RWMutex
	registry   = builtinFlavors()
	// detectionOrder is the precedence order, derived from the same list that builds the
	// registry rather than restated. Keeping one source is the whole point: the previous
	// second copy could omit a registered flavor, which then fell through to map iteration
	// order and had no stable precedence at all.
	detectionOrder = builtinFlavorList()
)

// Register registers a flavor archetype into the global registry. A replacement keeps the
// position the original held, so re-registering cannot silently reorder detection.
func Register(f Flavor) {
	registryMu.Lock()
	defer registryMu.Unlock()
	if _, replacing := registry[f.Name()]; replacing {
		for i := 0; i < len(detectionOrder) && i < maxDetectionCandidates; i++ {
			if detectionOrder[i].Name() == f.Name() {
				detectionOrder[i] = f
				break
			}
		}
	} else {
		detectionOrder = append(detectionOrder, f)
	}
	registry[f.Name()] = f
}

// Get returns the flavor by name, or an error if unregistered.
func Get(name string) (Flavor, error) {
	registryMu.RLock()
	defer registryMu.RUnlock()
	f, ok := registry[name]
	if !ok {
		return nil, fmt.Errorf("unknown flavor: %q", name)
	}
	return f, nil
}

// List returns all registered flavors in detection precedence order. It used to range over the
// registry map, so the command that prints the catalog produced a different order on every run.
func List() []Flavor {
	registryMu.RLock()
	defer registryMu.RUnlock()
	res := make([]Flavor, 0, len(detectionOrder))
	res = append(res, detectionOrder...)
	return res
}

// FallbackFlavor is what callers that must name something use when nothing matched. It is
// exported so the substitution is visible at the call site rather than hidden inside detection.
const FallbackFlavor = "go-library"

// maxDetectionCandidates bounds the detection scan (HISS-02).
const maxDetectionCandidates = 64

// Detect returns the best-matching flavor and whether anything matched at all.
//
// The second return value is the point. Detection used to end in an unconditional "go-library",
// so a repository that matched nothing was indistinguishable from one that is a Go library, and
// callers acted on the guess. Measured on a bare Dockerfile and on a bare agent harness, both of
// which reported go-library through that fallback while a second classifier reported
// container-image and framework respectively.
func Detect(repoPath string) (string, bool) {
	registryMu.RLock()
	defer registryMu.RUnlock()
	for i := 0; i < len(detectionOrder) && i < maxDetectionCandidates; i++ {
		if detectionOrder[i].Detect(repoPath) {
			return detectionOrder[i].Name(), true
		}
	}
	return "", false
}

// DetectFlavor returns the best-matching flavor name, substituting FallbackFlavor when nothing
// matched. Prefer Detect, which lets the caller see the difference.
func DetectFlavor(repoPath string) string {
	if name, ok := Detect(repoPath); ok {
		return name
	}
	return FallbackFlavor
}

// CheckFileExists is an internal helper for flavor detection.
func CheckFileExists(path string) bool {
	return util.PathExists(path)
}
