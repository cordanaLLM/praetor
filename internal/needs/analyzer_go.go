package needs

import (
	"context"
	"fmt"
	"path/filepath"
	"time"

	"github.com/cordanaLLM/praetor/internal/util"
)

// GoAnalyzer extracts Go module dependencies and maps them to Golusoris capabilities.
type GoAnalyzer struct{}

// NewGoAnalyzer returns an initialized GoAnalyzer.
func NewGoAnalyzer() *GoAnalyzer {
	return &GoAnalyzer{}
}

// Language returns the language identifier.
func (a *GoAnalyzer) Language() string {
	return "go"
}

// Detect checks if the repository contains a go.mod file.
func (a *GoAnalyzer) Detect(repoPath string) bool {
	return util.FileExists(filepath.Join(repoPath, "go.mod"))
}

// Analyze scans go.mod and Go AST imports to produce RepoNeeds.
func (a *GoAnalyzer) Analyze(ctx context.Context, repoPath string) (*RepoNeeds, error) {
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}

	modulePath, goVer, directDeps, err := parseGoMod(filepath.Join(repoPath, "go.mod"))
	if err != nil {
		return nil, fmt.Errorf("failed to parse go.mod: %w", err)
	}
	if modulePath == "" || modulePath == "unknown" {
		modulePath = filepath.Base(filepath.Clean(repoPath))
	}

	astImports, err := scanASTImports(ctx, repoPath, modulePath)
	if err != nil {
		return nil, fmt.Errorf("failed to scan AST imports: %w", err)
	}

	repoNeeds := &RepoNeeds{
		Version:      1,
		Repository:   modulePath,
		Language:     "go",
		Languages:    []string{"go"},
		GoVersion:    goVer,
		Framework:    defaultFrameworkModule,
		BuilderKits:  []string{"golusoris/golusoris", "golusoris/goenvoy"},
		Capabilities: CapabilityDeclaration{Required: make([]CapabilityKey, 0), Optional: make([]CapabilityKey, 0)},
		Dependencies: make([]DependencyDemand, 0),
		UpdatedAt:    time.Now().UTC(),
	}

	loadExistingDeclarations(repoPath, repoNeeds)
	buildDependencyDemands(directDeps, astImports, repoNeeds)
	calculateReadiness(repoNeeds)

	return repoNeeds, nil
}
