package needs

import (
	"bufio"
	"context"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/cordanaLLM/praetor/internal/util"
)

// NativeAnalyzer extracts C/C++ and GPU accelerator dependencies from Meson/CMake manifests.
type NativeAnalyzer struct{}

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

	deps := parseNativeBuildManifests(repoPath)
	for pkg, ver := range deps {
		demand := mapNativeDependency(pkg, ver)
		repoNeeds.Dependencies = append(repoNeeds.Dependencies, demand)
		repoNeeds.Capabilities.Required = appendUniqueCap(repoNeeds.Capabilities.Required, demand.Capability)
	}

	loadExistingDeclarations(repoPath, repoNeeds)
	calculateReadiness(repoNeeds)
	return repoNeeds, nil
}

func parseNativeBuildManifests(repoPath string) map[string]string {
	deps := make(map[string]string)
	mesonPath := filepath.Join(repoPath, "meson.build")
	if util.FileExists(mesonPath) {
		parseMesonBuild(mesonPath, deps)
	}

	cmakePath := filepath.Join(repoPath, "CMakeLists.txt")
	if util.FileExists(cmakePath) {
		parseCMakeLists(cmakePath, deps)
	}
	return deps
}

func parseMesonBuild(path string, deps map[string]string) {
	file, err := os.Open(path)
	if err != nil {
		return
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for lines := 0; lines < MaxScannedLines && scanner.Scan(); lines++ {
		line := strings.TrimSpace(scanner.Text())
		if strings.Contains(line, "dependency(") {
			idx := strings.Index(line, "dependency(")
			pkg := extractQuotedString(line[idx:])
			if pkg != "" {
				deps[pkg] = "native"
			}
		}
	}
}

func parseCMakeLists(path string, deps map[string]string) {
	file, err := os.Open(path)
	if err != nil {
		return
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for lines := 0; lines < MaxScannedLines && scanner.Scan(); lines++ {
		line := strings.TrimSpace(scanner.Text())
		if strings.Contains(line, "find_package(") {
			idx := strings.Index(line, "find_package(")
			rest := strings.TrimPrefix(line[idx:], "find_package(")
			parts := strings.Fields(rest)
			if len(parts) >= 1 {
				pkg := strings.Trim(parts[0], "()")
				deps[pkg] = "native"
			}
		}
	}
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

func mapNativeDependency(pkg, ver string) DependencyDemand {
	mapping, found := lookupNativeCatalog(pkg)
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
		Capability:       CapabilityKey("native.external." + cleanDepKey(pkg)),
		Status:           StatusGap,
		TargetBuilderKit: "golusoris/template-native-gpu",
		Notes:            "Native C/C++/GPU system library requiring Native-GPU template binding",
	}
}
