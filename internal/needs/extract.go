package needs

import (
	"context"
	"fmt"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/cordanaLLM/praetor/internal/util"
	"gopkg.in/yaml.v3"
)

// MaxScannedLines is the scalar upper bound (HISS-02) on the number of lines a single
// file scan reads. No source or manifest file in a governed repository approaches it;
// the constant exists so every scanner loop has a statically verifiable bound.
const MaxScannedLines = 200000

// ScanRepo extracts framework capability needs and dependency mappings from a repository.
func ScanRepo(ctx context.Context, repoPath string) (*RepoNeeds, error) {
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}

	needs, err := DefaultRegistry().AnalyzePolyglot(ctx, repoPath)
	if err == nil {
		return needs, nil
	}

	return NewGoAnalyzer().Analyze(ctx, repoPath)
}

// parseGoMod extracts the module path, go version, and direct dependencies from go.mod.
func parseGoMod(goModPath string) (string, string, map[string]string, error) {
	if !util.FileExists(goModPath) {
		return "unknown", "1.27", make(map[string]string), nil
	}

	// #nosec G304 -- goModPath is the scanned repository's own manifest, built by
	// joining the repository root with the constant "go.mod".
	content, err := os.ReadFile(goModPath)
	if err != nil {
		return "", "", nil, fmt.Errorf("read %q: %w", goModPath, err)
	}

	var modulePath, goVer string
	directDeps := make(map[string]string)
	inRequireBlock := false

	for _, raw := range strings.Split(string(content), "\n") {
		line := strings.TrimSpace(raw)
		inRequireBlock = parseGoModLine(line, inRequireBlock, &modulePath, &goVer, directDeps)
	}

	return modulePath, goVer, directDeps, nil
}

// parseGoModLine folds one go.mod line into the accumulated module path, go version and
// direct dependency set, returning the updated require-block state.
func parseGoModLine(line string, inRequireBlock bool, modulePath, goVer *string, directDeps map[string]string) bool {
	switch {
	case strings.HasPrefix(line, "module "):
		*modulePath = strings.TrimSpace(strings.TrimPrefix(line, "module"))
	case strings.HasPrefix(line, "go "):
		*goVer = strings.TrimSpace(strings.TrimPrefix(line, "go"))
	case strings.HasPrefix(line, "require ("):
		return true
	case inRequireBlock && line == ")":
		return false
	case inRequireBlock || strings.HasPrefix(line, "require "):
		parseRequireLine(line, directDeps)
	}
	return inRequireBlock
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
		if walkErr != nil {
			return walkErr
		}
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		if shouldSkipDir(info, path) {
			return filepath.SkipDir
		}
		if info.IsDir() || !strings.HasSuffix(info.Name(), ".go") || strings.HasSuffix(info.Name(), "_test.go") {
			return nil
		}
		collectFileImports(fset, path, modulePath, thirdParty)
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("walk %q for imports: %w", rootDir, err)
	}

	return thirdParty, nil
}

// collectFileImports adds the third-party imports of one Go file to out.
//
// An unparseable file - generated or partially written code - contributes nothing rather
// than failing the whole scan, so its parse error is deliberately not propagated.
func collectFileImports(fset *token.FileSet, path, modulePath string, out map[string]struct{}) {
	node, parseErr := parser.ParseFile(fset, path, nil, parser.ImportsOnly)
	if parseErr != nil {
		return
	}
	for _, imp := range node.Imports {
		rawPath := strings.Trim(imp.Path.Value, `"`)
		if isThirdPartyImport(rawPath, modulePath) {
			out[rawPath] = struct{}{}
		}
	}
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
		// #nosec G304 -- needsPath is the scanned repository's own .needs.yaml, a
		// constant filename under the caller-supplied repository root.
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
		// #nosec G304 -- standardsPath is the scanned repository's own
		// .standards.yaml, a constant filename under the repository root.
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
		switch d.Status {
		case StatusCovered, StatusAdapterAvailable, StatusNative:
			covered++
		case StatusGap:
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
//
// The manifest enumerates a repository's full third-party dependency inventory, so it is
// written owner-only rather than world-readable.
func WriteNeedsManifest(repoPath string, repoNeeds *RepoNeeds) error {
	if repoNeeds == nil {
		return fmt.Errorf("needs: cannot write a nil manifest for %q", repoPath)
	}
	targetFile := filepath.Join(repoPath, ".needs.yaml")
	data, err := yaml.Marshal(repoNeeds)
	if err != nil {
		return fmt.Errorf("failed to marshal needs manifest: %w", err)
	}
	if err := util.WriteFileSecure(targetFile, data, util.SecureFilePerm); err != nil {
		return fmt.Errorf("failed to write %s: %w", targetFile, err)
	}
	return nil
}
