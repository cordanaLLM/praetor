package flavor

import (
	"fmt"
	"sync"

	"github.com/cordanaLLM/praetor/internal/util"
)

// TemplateItem defines a template file required by a flavor.
type TemplateItem struct {
	Path        string                                     `json:"path"`
	Description string                                     `json:"description"`
	ContentFunc func(repoName string, owner string) string `json:"-"`

	// AltPaths lists equally valid alternatives to Path. A repository satisfies the
	// template when Path or any AltPath is present, and scaffolding is skipped in that
	// case. This exists because ecosystems rename their configuration without changing
	// its meaning: ESLint 9 replaced .eslintrc.json with eslint.config.*, and a workspace
	// commonly carries its compiler options in tsconfig.base.json. Demanding the older
	// name makes a conforming repository fail, and scaffolding it writes a second,
	// contradictory config that the toolchain then ignores.
	AltPaths []string `json:"alt_paths,omitempty"`
}

// SettingItem defines a configuration setting required by a flavor.
type SettingItem struct {
	Name        string                    `json:"name"`
	Path        string                    `json:"path"`
	Description string                    `json:"description"`
	Validator   func(content []byte) bool `json:"-"`
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
