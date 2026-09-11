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

// RustAnalyzer extracts Rust crate dependencies from Cargo.toml.
type RustAnalyzer struct{}

// NewRustAnalyzer initializes a RustAnalyzer instance.
func NewRustAnalyzer() *RustAnalyzer {
	return &RustAnalyzer{}
}

// Language returns the language identifier.
func (a *RustAnalyzer) Language() string {
	return "rust"
}

// Detect checks if the repository contains Cargo.toml.
func (a *RustAnalyzer) Detect(repoPath string) bool {
	return util.FileExists(filepath.Join(repoPath, "Cargo.toml"))
}

// Analyze extracts dependencies from Cargo.toml and maps to rustkit capabilities.
func (a *RustAnalyzer) Analyze(ctx context.Context, repoPath string) (*RepoNeeds, error) {
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}

	repoName := filepath.Base(repoPath)
	repoNeeds := &RepoNeeds{
		Version:      1,
		Repository:   repoName,
		Language:     "rust",
		Languages:    []string{"rust"},
		Framework:    "github.com/golusoris/rustkit",
		BuilderKits:  []string{"golusoris/rustkit"},
		Capabilities: CapabilityDeclaration{Required: make([]CapabilityKey, 0), Optional: make([]CapabilityKey, 0)},
		Dependencies: make([]DependencyDemand, 0),
		UpdatedAt:    time.Now().UTC(),
	}

	deps := parseCargoToml(filepath.Join(repoPath, "Cargo.toml"))
	for pkg, ver := range deps {
		demand := mapRustDependency(pkg, ver)
		repoNeeds.Dependencies = append(repoNeeds.Dependencies, demand)
		repoNeeds.Capabilities.Required = appendUniqueCap(repoNeeds.Capabilities.Required, demand.Capability)
	}

	loadExistingDeclarations(repoPath, repoNeeds)
	calculateReadiness(repoNeeds)
	return repoNeeds, nil
}

func parseCargoToml(cargoPath string) map[string]string {
	deps := make(map[string]string)
	file, err := os.Open(cargoPath)
	if err != nil {
		return deps
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	inDeps := false
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if strings.HasPrefix(line, "[dependencies]") {
			inDeps = true
			continue
		}
		if inDeps && strings.HasPrefix(line, "[") {
			inDeps = false
			break
		}
		if inDeps && strings.Contains(line, "=") && !strings.HasPrefix(line, "#") {
			parts := strings.SplitN(line, "=", 2)
			pkg := strings.TrimSpace(parts[0])
			ver := strings.Trim(strings.TrimSpace(parts[1]), "\", '")
			if pkg != "" {
				deps[pkg] = ver
			}
		}
	}
	return deps
}

func mapRustDependency(pkg, ver string) DependencyDemand {
	mapping, found := lookupRustCatalog(pkg)
	if found {
		return DependencyDemand{
			Package:              pkg,
			Version:              ver,
			Language:             "rust",
			Ecosystem:            "cargo",
			Capability:           mapping.Capability,
			Status:               mapping.Status,
			GolusorisReplacement: mapping.Replacement,
			TargetBuilderKit:     "golusoris/rustkit",
			Notes:                mapping.Notes,
		}
	}
	return DependencyDemand{
		Package:          pkg,
		Version:          ver,
		Language:         "rust",
		Ecosystem:        "cargo",
		Capability:       CapabilityKey("rust.external." + cleanDepKey(pkg)),
		Status:           StatusGap,
		TargetBuilderKit: "golusoris/rustkit",
		Notes:            "External Cargo crate requiring RustKit adapter or evaluation",
	}
}
