package needs

import (
	"context"
	"fmt"
	"path/filepath"
	"time"

	"github.com/cordanaLLM/praetor/internal/util"
)

// GoAnalyzer extracts Go module dependencies and maps them to capabilities of the go target
// framework.
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

// Analyze scans go.mod and Go AST imports to produce RepoNeeds scored against target.
func (a *GoAnalyzer) Analyze(ctx context.Context, repoPath string, target Target) (*RepoNeeds, error) {
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}

	module, err := parseGoMod(filepath.Join(repoPath, "go.mod"))
	if err != nil {
		return nil, fmt.Errorf("failed to parse go.mod: %w", err)
	}

	// The module path is only a valid import prefix when go.mod actually declares one;
	// the repository's name is a display fallback, never an import-classification
	// prefix (a directory called "go" would swallow every golang.org/x import).
	repoName, fallback := projectRepositoryName(module.modulePath, repoPath)

	astImports, err := scanASTImports(ctx, repoPath, module.modulePath, module.ignore)
	if err != nil {
		return nil, fmt.Errorf("failed to scan AST imports: %w", err)
	}

	repoNeeds := &RepoNeeds{
		Version:            1,
		Repository:         repoName,
		RepositoryFallback: fallback,
		Language:           "go",
		Languages:          []string{"go"},
		GoVersion:          module.goVersion,
		Capabilities:       CapabilityDeclaration{Required: make([]CapabilityKey, 0), Optional: make([]CapabilityKey, 0)},
		Dependencies:       make([]DependencyDemand, 0),
		UpdatedAt:          time.Now().UTC(),
	}

	buildDependencyDemands(module.directDeps, astImports, repoNeeds)
	target.applyTo(repoNeeds)
	if declErr := loadExistingDeclarations(ctx, repoPath, repoNeeds); declErr != nil {
		return nil, fmt.Errorf("failed to load existing declarations: %w", declErr)
	}
	calculateReadiness(repoNeeds)

	return repoNeeds, nil
}
