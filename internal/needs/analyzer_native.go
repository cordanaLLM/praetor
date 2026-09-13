package needs

import (
	"context"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/cordanaLLM/praetor/internal/util"
)

// NativeAnalyzer extracts C/C++ and GPU accelerator dependencies from Meson/CMake manifests.
type NativeAnalyzer struct{}

// nativeDep records a native dependency together with the spelling used in the manifest.
// The map key is the lower-cased name (CMake writes find_package(CUDA), Meson writes
// dependency('cuda'), and both must resolve to the same catalog entry exactly once).
type nativeDep struct {
	name    string
	version string
}

// NewNativeAnalyzer initializes a NativeAnalyzer instance.
func NewNativeAnalyzer() *NativeAnalyzer {
	return &NativeAnalyzer{}
}

// Language returns the language identifier.
func (a *NativeAnalyzer) Language() string {
	return "native"
}

// Detect checks if the repository contains C/C++ or GPU build manifests.
func (a *NativeAnalyzer) Detect(repoPath string) bool {
	return util.FileExists(filepath.Join(repoPath, "meson.build")) ||
		util.FileExists(filepath.Join(repoPath, "CMakeLists.txt"))
}

// Analyze extracts native C/C++ and GPU library dependencies.
func (a *NativeAnalyzer) Analyze(ctx context.Context, repoPath string) (*RepoNeeds, error) {
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}

	repoName := filepath.Base(repoPath)
	repoNeeds := &RepoNeeds{
		Version:      1,
		Repository:   repoName,
		Language:     "native",
		Languages:    []string{"c", "cpp", "cuda"},
		Framework:    "github.com/golusoris/template-native-gpu",
		BuilderKits:  []string{"golusoris/template-native-gpu"},
		Capabilities: CapabilityDeclaration{Required: make([]CapabilityKey, 0), Optional: make([]CapabilityKey, 0)},
		Dependencies: make([]DependencyDemand, 0),
		UpdatedAt:    time.Now().UTC(),
	}

	deps, err := parseNativeBuildManifests(repoPath)
	if err != nil {
		return nil, fmt.Errorf("failed to parse native build manifests in %q: %w", repoPath, err)
	}
	keys := make([]string, 0, len(deps))
	for k := range deps {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	for _, key := range keys {
		demand := mapNativeDependency(deps[key].name, deps[key].version)
		repoNeeds.Dependencies = append(repoNeeds.Dependencies, demand)
		repoNeeds.Capabilities.Required = appendUniqueCap(repoNeeds.Capabilities.Required, demand.Capability)
	}

	if declErr := loadExistingDeclarations(repoPath, repoNeeds); declErr != nil {
		return nil, fmt.Errorf("failed to load existing declarations: %w", declErr)
	}
	calculateReadiness(repoNeeds)
	return repoNeeds, nil
}

func parseNativeBuildManifests(repoPath string) (map[string]nativeDep, error) {
	deps := make(map[string]nativeDep)

	mesonPath := filepath.Join(repoPath, "meson.build")
	if util.FileExists(mesonPath) {
		if err := parseMesonBuild(mesonPath, deps); err != nil {
			return nil, err
		}
	}

	cmakePath := filepath.Join(repoPath, "CMakeLists.txt")
	if util.FileExists(cmakePath) {
		if err := parseCMakeLists(cmakePath, deps); err != nil {
			return nil, err
		}
	}
	return deps, nil
}

// recordNativeDep stores a dependency under its normalised key without overwriting a
// spelling that an earlier manifest already contributed.
func recordNativeDep(deps map[string]nativeDep, name string) {
	if name == "" {
		return
	}
	key := strings.ToLower(name)
	if _, exists := deps[key]; exists {
		return
	}
	deps[key] = nativeDep{name: name, version: "native"}
}

func parseMesonBuild(path string, deps map[string]nativeDep) error {
	return scanManifestLines(path, func(line string) {
		idx := strings.Index(line, "dependency(")
		if idx == -1 {
			return
		}
		recordNativeDep(deps, extractQuotedString(line[idx:]))
	})
}

func parseCMakeLists(path string, deps map[string]nativeDep) error {
	return scanManifestLines(path, func(line string) {
		idx := strings.Index(line, "find_package(")
		if idx == -1 {
			return
		}
		rest := strings.TrimPrefix(line[idx:], "find_package(")
		fields := strings.Fields(rest)
		if len(fields) == 0 {
			return
		}
		recordNativeDep(deps, strings.Trim(fields[0], "()"))
	})
}

func extractQuotedString(line string) string {
	start := strings.Index(line, "'")
	if start == -1 {
		start = strings.Index(line, "\"")
	}
	if start == -1 {
		return ""
	}
	quote := line[start : start+1]
	rest := line[start+1:]
	end := strings.Index(rest, quote)
	if end == -1 {
		return ""
	}
	return rest[:end]
}

// mapNativeDependency resolves a native library against the catalog. The lookup is
// case-insensitive: CMake's canonical module names are capitalised (CUDA, Vulkan,
// OpenCL) while the catalog is keyed in lower case.
func mapNativeDependency(pkg, ver string) DependencyDemand {
	mapping, found := lookupNativeCatalog(strings.ToLower(pkg))
	if found {
		return DependencyDemand{
			Package:              pkg,
			Version:              ver,
			Language:             "native",
			Ecosystem:            "system",
			Capability:           mapping.Capability,
			Status:               mapping.Status,
			GolusorisReplacement: mapping.Replacement,
			TargetBuilderKit:     "golusoris/template-native-gpu",
			Notes:                mapping.Notes,
		}
	}
	return DependencyDemand{
		Package:          pkg,
		Version:          ver,
		Language:         "native",
		Ecosystem:        "system",
		Capability:       CapabilityKey("native.external." + cleanDepKey(strings.ToLower(pkg))),
		Status:           StatusGap,
		TargetBuilderKit: "golusoris/template-native-gpu",
		Notes:            "Native C/C++/GPU system library requiring Native-GPU template binding",
	}
}
