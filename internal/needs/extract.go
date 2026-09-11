package needs

import (
	"bufio"
	"context"
	"fmt"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/cordanaLLM/standards/internal/util"
	"gopkg.in/yaml.v3"
)

// ScanRepo extracts framework capability needs and dependency mappings from a repository.
func ScanRepo(ctx context.Context, repoPath string) (*RepoNeeds, error) {
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}

	modulePath, goVer, directDeps, err := parseGoMod(filepath.Join(repoPath, "go.mod"))
	if err != nil {
		return nil, fmt.Errorf("failed to parse go.mod: %w", err)
	}

	astImports, err := scanASTImports(ctx, repoPath, modulePath)
	if err != nil {
		return nil, fmt.Errorf("failed to scan AST imports: %w", err)
	}

	repoNeeds := &RepoNeeds{
		Version:    1,
		Repository: modulePath,
		Language:   "go",
		GoVersion:  goVer,
		Framework:  defaultFrameworkModule,
		UpdatedAt:  time.Now().UTC(),
	}

	loadExistingDeclarations(repoPath, repoNeeds)
	buildDependencyDemands(directDeps, astImports, repoNeeds)
	calculateReadiness(repoNeeds)

	return repoNeeds, nil
}

// parseGoMod extracts the module path, go version, and direct dependencies from go.mod.
func parseGoMod(goModPath string) (string, string, map[string]string, error) {
	if !util.FileExists(goModPath) {
		return "unknown", "1.24", make(map[string]string), nil
	}

	file, err := os.Open(goModPath)
	if err != nil {
		return "", "", nil, err
	}
	defer file.Close()

	var modulePath, goVer string
	directDeps := make(map[string]string)
	scanner := bufio.NewScanner(file)
	inRequireBlock := false

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if strings.HasPrefix(line, "module ") {
			modulePath = strings.TrimSpace(strings.TrimPrefix(line, "module"))
		} else if strings.HasPrefix(line, "go ") {
			goVer = strings.TrimSpace(strings.TrimPrefix(line, "go"))
		} else if strings.HasPrefix(line, "require (") {
			inRequireBlock = true
		} else if inRequireBlock && line == ")" {
			inRequireBlock = false
		} else if inRequireBlock || strings.HasPrefix(line, "require ") {
			parseRequireLine(line, directDeps)
		}
	}

	return modulePath, goVer, directDeps, scanner.Err()
}

// parseRequireLine extracts a dependency if it is not marked as indirect.
func parseRequireLine(line string, directDeps map[string]string) {
	clean := strings.TrimPrefix(line, "require ")
	clean = strings.TrimSpace(clean)
	if strings.HasPrefix(clean, "//") || strings.HasPrefix(clean, "#") ||
		strings.Contains(clean, "// indirect") || clean == "" || clean == "(" || clean == ")" {
		return
	}
	parts := strings.Fields(clean)
	if len(parts) >= 2 {
		directDeps[parts[0]] = parts[1]
	}
}

// scanASTImports traverses the repo and extracts all unique third-party imports.
func scanASTImports(ctx context.Context, rootDir, modulePath string) (map[string]struct{}, error) {
	thirdParty := make(map[string]struct{})
	fset := token.NewFileSet()

	err := filepath.Walk(rootDir, func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil || ctx.Err() != nil {
			return walkErr
		}
		if shouldSkipDir(info, path) {
			return filepath.SkipDir
		}
		if info.IsDir() || !strings.HasSuffix(info.Name(), ".go") || strings.HasSuffix(info.Name(), "_test.go") {
			return nil
		}
		node, parseErr := parser.ParseFile(fset, path, nil, parser.ImportsOnly)
		if parseErr != nil {
			return nil // Skip unparseable generated code gracefully
		}
		for _, imp := range node.Imports {
			rawPath := strings.Trim(imp.Path.Value, `"`)
			if isThirdPartyImport(rawPath, modulePath) {
				thirdParty[rawPath] = struct{}{}
			}
		}
		return nil
	})

	return thirdParty, err
}

// shouldSkipDir checks whether the directory should be skipped during AST traversal.
func shouldSkipDir(info os.FileInfo, path string) bool {
	if !info.IsDir() {
		return false
	}
	name := info.Name()
	return name == "vendor" || name == ".git" || name == ".devcontainer" ||
		name == "node_modules" || strings.HasPrefix(name, ".") ||
		name == "scratch" || name == "cache" || strings.Contains(path, "/scratch") ||
		strings.Contains(path, "/.workingdir")
}

// isThirdPartyImport determines if an import path is external to stdlib and the current module.
func isThirdPartyImport(importPath, modulePath string) bool {
	if strings.HasPrefix(importPath, modulePath) {
		return false
	}
	firstSeg := strings.Split(importPath, "/")[0]
	return strings.Contains(firstSeg, ".")
}

// loadExistingDeclarations checks for existing .needs.yaml or .standards.yaml declarations.
func loadExistingDeclarations(repoPath string, repoNeeds *RepoNeeds) {
	needsPath := filepath.Join(repoPath, ".needs.yaml")
	if util.FileExists(needsPath) {
		data, err := os.ReadFile(needsPath)
		if err == nil {
			var existing RepoNeeds
			if yaml.Unmarshal(data, &existing) == nil {
				repoNeeds.Capabilities = existing.Capabilities
				return
			}
		}
	}

	standardsPath := filepath.Join(repoPath, ".standards.yaml")
	if util.FileExists(standardsPath) {
		data, err := os.ReadFile(standardsPath)
		if err == nil {
			var st struct {
				Needs CapabilityDeclaration `yaml:"needs"`
			}
			if yaml.Unmarshal(data, &st) == nil && len(st.Needs.Required) > 0 {
				repoNeeds.Capabilities = st.Needs
			}
		}
	}
}

// buildDependencyDemands maps discovered packages to capabilities and Golusoris replacements.
func buildDependencyDemands(directDeps map[string]string, astImports map[string]struct{}, repoNeeds *RepoNeeds) {
	pkgSet := make(map[string]string)
	for pkg, ver := range directDeps {
		pkgSet[pkg] = ver
	}
	for imp := range astImports {
		if _, ok := pkgSet[imp]; !ok {
			pkgSet[imp] = ""
		}
	}

	demands := make([]DependencyDemand, 0, len(pkgSet))
	for pkg, ver := range pkgSet {
		entry, found := MatchPackage(pkg)
		demand := DependencyDemand{
			Package: pkg,
			Version: ver,
		}
		if found {
			demand.Capability = entry.Capability
			demand.Status = entry.Status
			demand.GolusorisReplacement = entry.GolusorisReplacement
			demand.Notes = entry.Notes
		} else {
			demand.Capability = CapabilityKey("custom." + sanitizePackageName(pkg))
			demand.Status = StatusGap
			demand.Notes = "Third-party package without native Golusoris equivalent"
		}
		demands = append(demands, demand)
	}

	sort.Slice(demands, func(i, j int) bool {
		return demands[i].Package < demands[j].Package
	})
	repoNeeds.Dependencies = demands
}

// sanitizePackageName converts an import path into a safe capability identifier.
func sanitizePackageName(pkg string) string {
	parts := strings.Split(pkg, "/")
	if len(parts) > 0 {
		return parts[len(parts)-1]
	}
	return "lib"
}

// calculateReadiness computes the framework adoption score and dependency counts.
func calculateReadiness(repoNeeds *RepoNeeds) {
	total := len(repoNeeds.Dependencies)
	covered := 0
	gap := 0

	for _, d := range repoNeeds.Dependencies {
		if d.Status == StatusCovered || d.Status == StatusAdapterAvailable || d.Status == StatusNative {
			covered++
		} else if d.Status == StatusGap {
			gap++
		}
	}

	score := 100.0
	if total > 0 {
		score = (float64(covered) / float64(total)) * 100.0
	}

	repoNeeds.Readiness = ReadinessMetrics{
		Score:               score,
		TotalThirdPartyDeps: total,
		CoveredDeps:         covered,
		GapDeps:             gap,
	}
}

// WriteNeedsManifest serializes the RepoNeeds to .needs.yaml.
func WriteNeedsManifest(repoPath string, repoNeeds *RepoNeeds) error {
	targetFile := filepath.Join(repoPath, ".needs.yaml")
	data, err := yaml.Marshal(repoNeeds)
	if err != nil {
		return fmt.Errorf("failed to marshal needs manifest: %w", err)
	}
	return os.WriteFile(targetFile, data, 0644)
}
