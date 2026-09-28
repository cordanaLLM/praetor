package needs

import (
	"context"
	"fmt"
	"maps"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/cordanaLLM/praetor/internal/util"
)

// NativeAnalyzer extracts C/C++ and GPU accelerator dependencies from Meson/CMake manifests,
// and the packages a Zig build declares in build.zig.zon.
type NativeAnalyzer struct{}

// Native build markers. meson.build and CMakeLists.txt build C, C++ and CUDA; build.zig is a Zig
// build, and build.zig.zon its package manifest.
const (
	mesonMarker = "meson.build"
	cmakeMarker = "CMakeLists.txt"
	zigMarker   = "build.zig"
	zigManifest = "build.zig.zon"
)

var (
	cFamilyMarkers = []string{mesonMarker, cmakeMarker}
	zigMarkers     = []string{zigMarker, zigManifest}
)

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

// Detect checks if the repository contains a native build manifest: meson.build,
// CMakeLists.txt, build.zig or build.zig.zon.
func (a *NativeAnalyzer) Detect(repoPath string) bool {
	return hasAnyFile(repoPath, cFamilyMarkers) || hasAnyFile(repoPath, zigMarkers)
}

// hasAnyFile reports whether dir holds a file with one of names.
func hasAnyFile(dir string, names []string) bool {
	return slices.ContainsFunc(names, func(name string) bool {
		return util.FileExists(filepath.Join(dir, name))
	})
}

// NativeLanguages returns the languages the native builds in repoPath build: C, C++ and CUDA
// for meson or CMake, Zig for a Zig build, nothing without a native marker. A Zig build that
// also compiles C or C++ is C/C++ only through a C-family marker; build.zig is a program, not a
// manifest that declares it. The analyzer id "native" covers all three build systems, so a
// caller that needs the languages behind it asks here instead of reading the id as C/C++.
func NativeLanguages(repoPath string) []string {
	var languages []string
	if hasAnyFile(repoPath, cFamilyMarkers) {
		languages = append(languages, "c", "cpp", "cuda")
	}
	if hasAnyFile(repoPath, zigMarkers) {
		languages = append(languages, "zig")
	}
	return languages
}

// Analyze extracts native C/C++ and GPU library dependencies and maps them to capabilities
// of the native target framework.
func (a *NativeAnalyzer) Analyze(ctx context.Context, repoPath string, target Target) (*RepoNeeds, error) {
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}

	repoName := repositoryDirName(repoPath)
	repoNeeds := &RepoNeeds{
		Version:      1,
		Repository:   repoName,
		Language:     "native",
		Languages:    NativeLanguages(repoPath),
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
		demand := nativeClassifier.classify(deps[key].name, deps[key].version, target.RoutingKit())
		repoNeeds.Dependencies = append(repoNeeds.Dependencies, demand)
		repoNeeds.Capabilities.Required = appendUniqueCap(repoNeeds.Capabilities.Required, demand.Capability)
	}
	target.applyTo(repoNeeds)

	if declErr := loadExistingDeclarations(ctx, repoPath, repoNeeds); declErr != nil {
		return nil, fmt.Errorf("failed to load existing declarations: %w", declErr)
	}
	calculateReadiness(repoNeeds)
	return repoNeeds, nil
}

func parseNativeBuildManifests(repoPath string) (map[string]nativeDep, error) {
	deps := make(map[string]nativeDep)

	for _, manifest := range []struct {
		name  string
		parse func(string, map[string]nativeDep) error
	}{{mesonMarker, parseMesonBuild}, {cmakeMarker, parseCMakeLists}, {zigManifest, parseZigManifest}} {
		manifestPath := filepath.Join(repoPath, manifest.name)
		if !util.FileExists(manifestPath) {
			continue
		}
		if err := manifest.parse(manifestPath, deps); err != nil {
			return nil, err
		}
	}
	return deps, nil
}

// parseZigManifest records the third-party packages a build.zig.zon declares
// (zonDependency.thirdParty): every package fetched by url, and every path package vendored
// under a directory discovery prunes. A manifest parseZon refuses fails the scan with its reason.
func parseZigManifest(manifestPath string, deps map[string]nativeDep) error {
	data, err := readManifest(manifestPath)
	if err != nil {
		return err
	}
	zonDeps, err := parseZon(string(data))
	if err != nil {
		return fmt.Errorf("parse %q: %w", manifestPath, err)
	}
	for _, name := range slices.Sorted(maps.Keys(zonDeps)) {
		if zonDeps[name].thirdParty() {
			recordNativeDep(deps, name)
		}
	}
	return nil
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
