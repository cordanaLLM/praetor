package needs

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/cordanaLLM/standards/internal/util"
)

// NodeAnalyzer extracts JavaScript/TypeScript and Svelte dependencies from package.json.
type NodeAnalyzer struct{}

// NewNodeAnalyzer initializes a NodeAnalyzer instance.
func NewNodeAnalyzer() *NodeAnalyzer {
	return &NodeAnalyzer{}
}

// Language returns the language identifier.
func (a *NodeAnalyzer) Language() string {
	return "typescript"
}

// Detect checks if the repository contains package.json.
func (a *NodeAnalyzer) Detect(repoPath string) bool {
	return util.FileExists(filepath.Join(repoPath, "package.json"))
}

type packageJSON struct {
	Name            string            `json:"name"`
	Dependencies    map[string]string `json:"dependencies"`
	DevDependencies map[string]string `json:"devDependencies"`
}

// Analyze parses package.json and maps dependencies to SvelteSentio capabilities.
func (a *NodeAnalyzer) Analyze(ctx context.Context, repoPath string) (*RepoNeeds, error) {
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}

	pkgData, err := readPackageJSON(filepath.Join(repoPath, "package.json"))
	if err != nil {
		return nil, fmt.Errorf("failed to read package.json: %w", err)
	}

	repoName := pkgData.Name
	if repoName == "" {
		repoName = filepath.Base(repoPath)
	}

	repoNeeds := &RepoNeeds{
		Version:      1,
		Repository:   repoName,
		Language:     "typescript",
		Languages:    []string{"typescript", "svelte"},
		Framework:    "github.com/golusoris/sveltesentio",
		BuilderKits:  []string{"golusoris/sveltesentio"},
		Capabilities: CapabilityDeclaration{Required: make([]CapabilityKey, 0), Optional: make([]CapabilityKey, 0)},
		Dependencies: make([]DependencyDemand, 0),
		UpdatedAt:    time.Now().UTC(),
	}

	allDeps := mergeDependencies(pkgData.Dependencies, pkgData.DevDependencies)
	for pkg, ver := range allDeps {
		demand := mapNodeDependency(pkg, ver)
		repoNeeds.Dependencies = append(repoNeeds.Dependencies, demand)
		repoNeeds.Capabilities.Required = appendUniqueCap(repoNeeds.Capabilities.Required, demand.Capability)
	}

	loadExistingDeclarations(repoPath, repoNeeds)
	calculateReadiness(repoNeeds)
	return repoNeeds, nil
}

func readPackageJSON(pkgPath string) (*packageJSON, error) {
	data, err := os.ReadFile(pkgPath)
	if err != nil {
		return nil, err
	}
	var pkg packageJSON
	if err := json.Unmarshal(data, &pkg); err != nil {
		return nil, err
	}
	return &pkg, nil
}

func mergeDependencies(deps, devDeps map[string]string) map[string]string {
	merged := make(map[string]string)
	for k, v := range deps {
		merged[k] = v
	}
	for k, v := range devDeps {
		merged[k] = v
	}
	return merged
}

func mapNodeDependency(pkg, ver string) DependencyDemand {
	mapping, found := lookupNodeCatalog(pkg)
	if found {
		return DependencyDemand{
			Package:              pkg,
			Version:              ver,
			Language:             "typescript",
			Ecosystem:            "npm",
			Capability:           mapping.Capability,
			Status:               mapping.Status,
			GolusorisReplacement: mapping.Replacement,
			TargetBuilderKit:     "golusoris/sveltesentio",
			Notes:                mapping.Notes,
		}
	}
	return DependencyDemand{
		Package:          pkg,
		Version:          ver,
		Language:         "typescript",
		Ecosystem:        "npm",
		Capability:       CapabilityKey("ui.external." + cleanDepKey(pkg)),
		Status:           StatusGap,
		TargetBuilderKit: "golusoris/sveltesentio",
		Notes:            "External npm dependency requiring SvelteSentio adapter or evaluation",
	}
}
