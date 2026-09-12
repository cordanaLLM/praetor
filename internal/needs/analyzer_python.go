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

// PythonAnalyzer extracts Python dependencies from requirements.txt or pyproject.toml.
type PythonAnalyzer struct{}

// NewPythonAnalyzer initializes a PythonAnalyzer instance.
func NewPythonAnalyzer() *PythonAnalyzer {
	return &PythonAnalyzer{}
}

// Language returns the language identifier.
func (a *PythonAnalyzer) Language() string {
	return "python"
}

// Detect checks if the repository contains Python project indicators.
func (a *PythonAnalyzer) Detect(repoPath string) bool {
	return util.FileExists(filepath.Join(repoPath, "requirements.txt")) ||
		util.FileExists(filepath.Join(repoPath, "pyproject.toml")) ||
		util.FileExists(filepath.Join(repoPath, "setup.py"))
}

// Analyze extracts Python dependencies and maps them to pykit capabilities.
func (a *PythonAnalyzer) Analyze(ctx context.Context, repoPath string) (*RepoNeeds, error) {
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}

	repoName := filepath.Base(repoPath)
	repoNeeds := &RepoNeeds{
		Version:      1,
		Repository:   repoName,
		Language:     "python",
		Languages:    []string{"python"},
		Framework:    "github.com/golusoris/pykit",
		BuilderKits:  []string{"golusoris/pykit"},
		Capabilities: CapabilityDeclaration{Required: make([]CapabilityKey, 0), Optional: make([]CapabilityKey, 0)},
		Dependencies: make([]DependencyDemand, 0),
		UpdatedAt:    time.Now().UTC(),
	}

	deps := parsePythonDependencies(repoPath)
	for pkg, ver := range deps {
		demand := mapPythonDependency(pkg, ver)
		repoNeeds.Dependencies = append(repoNeeds.Dependencies, demand)
		repoNeeds.Capabilities.Required = appendUniqueCap(repoNeeds.Capabilities.Required, demand.Capability)
	}

	loadExistingDeclarations(repoPath, repoNeeds)
	calculateReadiness(repoNeeds)
	return repoNeeds, nil
}

func parsePythonDependencies(repoPath string) map[string]string {
	deps := make(map[string]string)
	reqPath := filepath.Join(repoPath, "requirements.txt")
	if util.FileExists(reqPath) {
		parseRequirementsFile(reqPath, deps)
	}

	pyprojectPath := filepath.Join(repoPath, "pyproject.toml")
	if util.FileExists(pyprojectPath) {
		parsePyprojectFile(pyprojectPath, deps)
	}
	return deps
}

func parseRequirementsFile(path string, deps map[string]string) {
	file, err := os.Open(path)
	if err != nil {
		return
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for lines := 0; lines < MaxScannedLines && scanner.Scan(); lines++ {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "-") {
			continue
		}
		parsePythonReqLine(line, deps)
	}
}

func parsePythonReqLine(line string, deps map[string]string) {
	separators := []string{"==", ">=", "<=", "~=", "!="}
	pkg := line
	ver := ""
	for _, sep := range separators {
		if idx := strings.Index(line, sep); idx != -1 {
			pkg = strings.TrimSpace(line[:idx])
			ver = strings.TrimSpace(line[idx+len(sep):])
			break
		}
	}
	if pkg != "" {
		deps[strings.ToLower(pkg)] = ver
	}
}

func parsePyprojectFile(path string, deps map[string]string) {
	file, err := os.Open(path)
	if err != nil {
		return
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	inDeps := false
	for lines := 0; lines < MaxScannedLines && scanner.Scan(); lines++ {
		line := strings.TrimSpace(scanner.Text())
		if strings.HasPrefix(line, "dependencies = [") {
			inDeps = true
			continue
		}
		if inDeps && strings.HasPrefix(line, "]") {
			inDeps = false
			break
		}
		if inDeps {
			clean := strings.Trim(line, "\", '")
			if clean != "" {
				parsePythonReqLine(clean, deps)
			}
		}
	}
}

func mapPythonDependency(pkg, ver string) DependencyDemand {
	mapping, found := lookupPythonCatalog(pkg)
	if found {
		return DependencyDemand{
			Package:              pkg,
			Version:              ver,
			Language:             "python",
			Ecosystem:            "pypi",
			Capability:           mapping.Capability,
			Status:               mapping.Status,
			GolusorisReplacement: mapping.Replacement,
			TargetBuilderKit:     "golusoris/pykit",
			Notes:                mapping.Notes,
		}
	}
	return DependencyDemand{
		Package:          pkg,
		Version:          ver,
		Language:         "python",
		Ecosystem:        "pypi",
		Capability:       CapabilityKey("python.external." + cleanDepKey(pkg)),
		Status:           StatusGap,
		TargetBuilderKit: "golusoris/pykit",
		Notes:            "External PyPI dependency requiring PyKit adapter or evaluation",
	}
}
