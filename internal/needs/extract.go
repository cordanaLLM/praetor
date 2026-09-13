package needs

import (
	"context"
	"errors"
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

// maxPathSegments bounds the per-path segment loop in shouldSkipDir (HISS-02).
const maxPathSegments = 128

// ErrGoModMissing is returned when a Go analysis is requested for a directory that has
// no go.mod. Callers must not substitute a fabricated Go manifest for the missing file.
var ErrGoModMissing = errors.New("needs: go.mod not found")

// ScanRepo extracts framework capability needs and dependency mappings from a repository.
// A repository that no registered language analyzer recognises is an error: silently
// falling back to a Go manifest would report an unanalysed repository as fully ready.
func ScanRepo(ctx context.Context, repoPath string) (*RepoNeeds, error) {
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}

	repoNeeds, err := DefaultRegistry().AnalyzePolyglot(ctx, repoPath)
	if err != nil {
		return nil, fmt.Errorf("failed to analyze repository %q: %w", repoPath, err)
	}
	return repoNeeds, nil
}

// ScanRepoWithFramework reconciles catalog demands against the selected framework.
// It reports available mappings, never runtime compatibility or passing tests.
func ScanRepoWithFramework(ctx context.Context, repoPath string, framework *FrameworkIndex) (*RepoNeeds, error) {
	if ctx == nil || framework == nil {
		return nil, errors.New("context and framework index are required")
	}
	report, err := ScanRepo(ctx, repoPath)
	if err != nil {
		return nil, err
	}
	applyFrameworkCoverage(framework, report)
	return report, nil
}

// parseGoMod extracts the module path, go version, and direct dependencies from go.mod.
func parseGoMod(goModPath string) (modulePath string, goVersion string, directDeps map[string]string, err error) {
	if !util.FileExists(goModPath) {
		return "", "", nil, fmt.Errorf("%w: %s", ErrGoModMissing, goModPath)
	}

	state := goModScanState{directDeps: make(map[string]string)}
	if scanErr := scanManifestLines(goModPath, state.consume); scanErr != nil {
		return "", "", nil, scanErr
	}

	return state.modulePath, state.goVersion, state.directDeps, nil
}

// goModScanState accumulates the go.mod directives seen so far. Keeping the per-line
// classification here holds parseGoMod itself under the HISS-04 complexity cap.
type goModScanState struct {
	modulePath     string
	goVersion      string
	directDeps     map[string]string
	inRequireBlock bool
}

// consume classifies a single trimmed go.mod line.
func (s *goModScanState) consume(line string) {
	switch {
	case strings.HasPrefix(line, "module "):
		s.modulePath = strings.TrimSpace(strings.TrimPrefix(line, "module"))
	case strings.HasPrefix(line, "go "):
		s.goVersion = strings.TrimSpace(strings.TrimPrefix(line, "go"))
	case strings.HasPrefix(line, "require ("):
		s.inRequireBlock = true
	case s.inRequireBlock && line == ")":
		s.inRequireBlock = false
	case s.inRequireBlock || strings.HasPrefix(line, "require "):
		parseRequireLine(line, s.directDeps)
	}
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

// scanASTImports extracts third-party imports and selected catalog stdlib imports.
func scanASTImports(ctx context.Context, rootDir, modulePath string) (map[string]struct{}, error) {
	thirdParty := make(map[string]struct{})
	fset := token.NewFileSet()
	root := filepath.Clean(rootDir)

	err := filepath.Walk(root, func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		if shouldSkipDir(info, path, root) {
			return filepath.SkipDir
		}
		if !isScannableGoFile(info) {
			return nil
		}
		collectFileImports(fset, path, modulePath, thirdParty)
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("failed to walk %q for Go imports: %w", root, err)
	}

	return thirdParty, nil
}

// isScannableGoFile reports whether info is a regular, non-test .go source file. Symlinks
// are excluded: their target may live outside the scanned repository.
func isScannableGoFile(info os.FileInfo) bool {
	if info == nil || info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return false
	}
	name := info.Name()
	return strings.HasSuffix(name, ".go") && !strings.HasSuffix(name, "_test.go")
}

// collectFileImports parses one file and records its third-party imports. A file that
// does not parse (generated or partially written code) contributes no imports.
func collectFileImports(fset *token.FileSet, path, modulePath string, thirdParty map[string]struct{}) {
	node, parseErr := parser.ParseFile(fset, path, nil, parser.ImportsOnly)
	if parseErr != nil {
		return
	}
	for _, imp := range node.Imports {
		rawPath := strings.Trim(imp.Path.Value, `"`)
		if isThirdPartyImport(rawPath, modulePath) || isSelectedStandardImport(rawPath) {
			thirdParty[rawPath] = struct{}{}
		}
	}
}

// shouldSkipDir reports whether the directory at path must be excluded from a walk rooted
// at rootDir. The walk root itself is never skipped: filepath.Walk visits it first, and
// skipping it (which a root of "." used to trigger, because its base name starts with a
// dot) aborts the entire traversal before a single file is seen.
func shouldSkipDir(info os.FileInfo, path, rootDir string) bool {
	if info == nil || !info.IsDir() {
		return false
	}
	rel, err := filepath.Rel(rootDir, path)
	if err != nil {
		return true
	}
	if rel == "." || rel == "" {
		return false
	}
	segments := strings.Split(filepath.ToSlash(rel), "/")
	bound := len(segments)
	if bound > maxPathSegments {
		return true
	}
	for i := 0; i < bound; i++ {
		if isExcludedDirSegment(segments[i]) {
			return true
		}
	}
	return false
}

// isExcludedDirSegment reports whether a single path segment names a directory that never
// contains first-party sources.
func isExcludedDirSegment(name string) bool {
	switch name {
	case "vendor", "node_modules", "scratch", "cache":
		return true
	}
	return strings.HasPrefix(name, ".")
}

// isThirdPartyImport determines if an import path is external to stdlib and the current
// module. The module comparison is boundary-aware: a sibling module that merely shares a
// textual prefix (github.com/acme/foo-plugins vs github.com/acme/foo) is third-party.
func isThirdPartyImport(importPath, modulePath string) bool {
	if modulePath != "" &&
		(importPath == modulePath || strings.HasPrefix(importPath, modulePath+"/")) {
		return false
	}
	firstSeg := strings.Split(importPath, "/")[0]
	return strings.Contains(firstSeg, ".")
}

// loadExistingDeclarations merges capabilities declared in an existing .needs.yaml or
// .standards.yaml into the freshly computed set. Declared entries are additive: replacing
// the computed set would freeze Capabilities.Required at its first written value.
func loadExistingDeclarations(repoPath string, repoNeeds *RepoNeeds) error {
	needsPath := filepath.Join(repoPath, ".needs.yaml")
	if util.FileExists(needsPath) {
		var existing RepoNeeds
		if err := readYAMLFile(needsPath, &existing); err != nil {
			return err
		}
		mergeCapabilities(repoNeeds, existing.Capabilities)
		return nil
	}

	standardsPath := filepath.Join(repoPath, ".standards.yaml")
	if util.FileExists(standardsPath) {
		var st struct {
			Needs CapabilityDeclaration `yaml:"needs"`
		}
		if err := readYAMLFile(standardsPath, &st); err != nil {
			return err
		}
		mergeCapabilities(repoNeeds, st.Needs)
	}
	return nil
}

// readYAMLFile reads and unmarshals a repository-local declaration file.
func readYAMLFile(path string, out any) error {
	// #nosec G304 -- path is filepath.Join(repoPath, "<constant filename>") for a
	// repository the caller already selected; no component comes from user input.
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("failed to read %q: %w", path, err)
	}
	if err := yaml.Unmarshal(data, out); err != nil {
		return fmt.Errorf("failed to parse %q: %w", path, err)
	}
	return nil
}

// mergeCapabilities folds declared capabilities into the computed declaration.
func mergeCapabilities(repoNeeds *RepoNeeds, declared CapabilityDeclaration) {
	for _, capKey := range declared.Required {
		repoNeeds.Capabilities.Required = appendUniqueCap(repoNeeds.Capabilities.Required, capKey)
	}
	for _, capKey := range declared.Optional {
		repoNeeds.Capabilities.Optional = appendUniqueCap(repoNeeds.Capabilities.Optional, capKey)
	}
}

// buildDependencyDemands maps discovered packages to capabilities and Golusoris
// replacements. AST imports are collapsed onto the module that owns them so that
// importing several packages of one module counts as a single dependency.
func buildDependencyDemands(directDeps map[string]string, astImports map[string]struct{}, repoNeeds *RepoNeeds) {
	pkgSet := make(map[string]string, len(directDeps)+len(astImports))
	for pkg, ver := range directDeps {
		pkgSet[pkg] = ver
	}
	for imp := range astImports {
		if isSelectedStandardImport(imp) {
			repoNeeds.StandardLibraryImports = append(repoNeeds.StandardLibraryImports, buildGoDemand(imp, ""))
			continue
		}
		root := ResolveModuleRoot(imp, directDeps)
		if _, ok := pkgSet[root]; !ok {
			pkgSet[root] = ""
		}
	}

	demands := make([]DependencyDemand, 0, len(pkgSet))
	for pkg, ver := range pkgSet {
		demands = append(demands, buildGoDemand(pkg, ver))
	}

	sort.Slice(demands, func(i, j int) bool {
		return demands[i].Package < demands[j].Package
	})
	repoNeeds.Dependencies = demands
	sort.Slice(repoNeeds.StandardLibraryImports, func(i, j int) bool {
		return repoNeeds.StandardLibraryImports[i].Package < repoNeeds.StandardLibraryImports[j].Package
	})
	for _, d := range repoNeeds.StandardLibraryImports {
		repoNeeds.Capabilities.Required = appendUniqueCap(repoNeeds.Capabilities.Required, d.Capability)
	}
	for _, d := range demands {
		repoNeeds.Capabilities.Required = appendUniqueCap(repoNeeds.Capabilities.Required, d.Capability)
	}
}

// buildGoDemand maps a single Go module path onto its catalog entry.
func buildGoDemand(pkg, ver string) DependencyDemand {
	demand := DependencyDemand{
		Package:   pkg,
		Version:   ver,
		Language:  "go",
		Ecosystem: "go",
	}
	entry, found := MatchPackage(pkg)
	if found {
		demand.Capability = entry.Capability
		demand.Status = entry.Status
		demand.GolusorisReplacement = entry.GolusorisReplacement
		demand.Notes = entry.Notes
		demand.Relationship = cloneRelationship(entry.Relationship)
		return demand
	}
	demand.Capability = CapabilityKey("custom." + cleanDepKey(pkg))
	demand.Status = StatusGap
	demand.Notes = "Third-party package without native Golusoris equivalent"
	return demand
}

// ResolveModuleRoot reduces an import path to the module that owns it. A module listed in
// go.mod wins (longest matching path); otherwise the conventional module root for the
// hosting domain is used, so that github.com/foo/bar/v4/sub resolves to
// github.com/foo/bar/v4 rather than counting as an independent dependency.
func ResolveModuleRoot(importPath string, directDeps map[string]string) string {
	best := ""
	for mod := range directDeps {
		if importPath != mod && !strings.HasPrefix(importPath, mod+"/") {
			continue
		}
		if len(mod) > len(best) {
			best = mod
		}
	}
	if best != "" {
		return best
	}
	return conventionalModuleRoot(importPath)
}

// hostModuleDepth returns the number of leading path segments that form a module root on
// a hosting domain. Domains that are not code-hosting forges use two segments
// (host/module), the shape of a vanity import path.
func hostModuleDepth(host string) int {
	forgeHosts := map[string]struct{}{
		"github.com": {}, "gitlab.com": {}, "bitbucket.org": {}, "codeberg.org": {},
		"gitee.com": {}, "git.sr.ht": {}, "golang.org": {},
	}
	if _, ok := forgeHosts[host]; ok {
		return 3
	}
	return 2
}

// conventionalModuleRoot applies the hosting-domain convention plus the /vN major-version
// suffix rule to an import path whose module is not declared in go.mod.
func conventionalModuleRoot(importPath string) string {
	segments := strings.Split(importPath, "/")
	depth := hostModuleDepth(segments[0])
	if len(segments) <= depth {
		return importPath
	}
	root := strings.Join(segments[:depth], "/")
	if isMajorVersionSegment(segments[depth]) {
		root += "/" + segments[depth]
	}
	return root
}

// isMajorVersionSegment reports whether a path segment is a Go major-version suffix (v2,
// v3, ...).
func isMajorVersionSegment(segment string) bool {
	if len(segment) < 2 || segment[0] != 'v' {
		return false
	}
	for i := 1; i < len(segment); i++ {
		if segment[i] < '0' || segment[i] > '9' {
			return false
		}
	}
	return true
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
		Basis:               FrameworkCatalogDeclared,
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
