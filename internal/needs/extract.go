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

	"github.com/cordanaLLM/praetor/internal/config"
	"github.com/cordanaLLM/praetor/internal/gomanifest"
	"github.com/cordanaLLM/praetor/internal/topology"
	"github.com/cordanaLLM/praetor/internal/util"
)

// maxPathSegments bounds the per-path segment loop in shouldSkipDir (HISS-02).
const maxPathSegments = 128

// ErrGoModMissing is returned when a Go analysis is requested for a directory that has
// no go.mod. Callers must not substitute a fabricated Go manifest for the missing file.
var ErrGoModMissing = errors.New("needs: go.mod not found")

// ScanRepo extracts framework capability needs and dependency mappings from the repository
// rooted at repoPath as one row: the project at the root and every nested sub-project the
// fleet walk assigns the repository (see discoverRepository and scanRepository), so a
// single-repository scan and a fleet aggregation score a repository identically. A
// repository that no registered language analyzer recognises is an error: silently
// falling back to a Go manifest would report an unanalysed repository as fully ready.
// registry supplies the analyzers and the framework targets; nil selects DefaultRegistry.
func ScanRepo(ctx context.Context, repoPath string, registry *AnalyzerRegistry) (*RepoNeeds, error) {
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	repo, err := discoverRepository(ctx, repoPath)
	if err != nil {
		return nil, fmt.Errorf("failed to discover the projects of repository %q: %w", repoPath, err)
	}
	return scanRepository(ctx, repo, registry)
}

// scanProjectDir runs every analyzer of registry that detects a project in dir itself.
func scanProjectDir(ctx context.Context, dir string, registry *AnalyzerRegistry) (*RepoNeeds, error) {
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	repoNeeds, err := registryOrDefault(registry).AnalyzePolyglot(ctx, dir)
	if err != nil {
		return nil, fmt.Errorf("failed to analyze project %q: %w", dir, err)
	}
	return repoNeeds, nil
}

// ScanRepoWithFramework reconciles catalog demands against the selected framework.
// It reports available mappings, never runtime compatibility or passing tests.
func ScanRepoWithFramework(ctx context.Context, repoPath string, framework *FrameworkIndex, registry *AnalyzerRegistry) (*RepoNeeds, error) {
	if ctx == nil || framework == nil {
		return nil, errors.New("context and framework index are required")
	}
	report, err := ScanRepo(ctx, repoPath, registry)
	if err != nil {
		return nil, err
	}
	applyFrameworkCoverage(framework, report, registry)
	return report, nil
}

// parseGoMod extracts the module path, go version, and direct dependencies from go.mod.
// Each line is classified through the shared lexical rules of internal/gomanifest, the
// ones the SBOM, docs-reference and toolchain scanners use, so a trailing comment, a
// quoted module path and the "//indirect" marker read the way the go command reads them.
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
	if path, ok := gomanifest.ModulePath(line); ok {
		s.modulePath = path
		return
	}
	if version, ok := gomanifest.GoDirectiveLine(line); ok {
		s.goVersion = version
		return
	}
	if requirement, ok := gomanifest.RequirementLine(line, &s.inRequireBlock); ok {
		recordDirectRequirement(requirement, s.directDeps)
	}
}

// recordDirectRequirement records the module and version of one require line, block
// keyword and delimiters already removed, unless the line carries the go command's
// indirect marker. A quoted module path or version is unquoted, as the go command reads
// it; a malformed quoted token records nothing.
func recordDirectRequirement(line string, directDeps map[string]string) {
	if gomanifest.IsIndirect(line) {
		return
	}
	if requirement, ok := gomanifest.ParseRequirement(line); ok {
		directDeps[requirement.Path] = requirement.Version
	}
}

// maxImportScanEntries bounds how many files and directories one import scan visits
// (HISS-02). A module tree holding more fails the scan with ErrDiscoveryBound instead of
// returning the imports of the part it reached.
const maxImportScanEntries = 1000000

// scanASTImports extracts third-party imports and selected catalog stdlib imports.
func scanASTImports(ctx context.Context, rootDir, modulePath string) (map[string]struct{}, error) {
	return scanASTImportsBounded(ctx, rootDir, modulePath, maxImportScanEntries)
}

// scanASTImportsBounded is scanASTImports with the entry bound as a parameter, so the
// bound itself can be exercised without a million-file fixture.
func scanASTImportsBounded(ctx context.Context, rootDir, modulePath string, limit int) (map[string]struct{}, error) {
	scan := &importScan{
		ctx:        ctx,
		root:       filepath.Clean(rootDir),
		modulePath: modulePath,
		limit:      limit,
		fset:       token.NewFileSet(),
		imports:    make(map[string]struct{}),
	}
	if err := filepath.Walk(scan.root, scan.visit); err != nil {
		return nil, fmt.Errorf("failed to walk %q for Go imports: %w", scan.root, err)
	}
	return scan.imports, nil
}

// importScan is one bounded walk of a module's Go sources, collecting their imports.
type importScan struct {
	ctx        context.Context
	root       string
	modulePath string
	limit      int
	visited    int
	fset       *token.FileSet
	imports    map[string]struct{}
}

// visit is the filepath.WalkFunc of an import scan.
func (s *importScan) visit(path string, info os.FileInfo, walkErr error) error {
	if walkErr != nil {
		return walkErr
	}
	if ctxErr := s.ctx.Err(); ctxErr != nil {
		return ctxErr
	}
	s.visited++
	if s.visited > s.limit {
		return fmt.Errorf("%w: import scan visited more than %d entries below %s", ErrDiscoveryBound, s.limit, s.root)
	}
	if s.skipsDir(info, path) {
		return filepath.SkipDir
	}
	if isScannableGoFile(info) {
		collectFileImports(s.fset, path, s.modulePath, s.imports)
	}
	return nil
}

// skipsDir reports whether the import scan must not enter the directory at path.
func (s *importScan) skipsDir(info os.FileInfo, path string) bool {
	return shouldSkipDir(info, path, s.root) ||
		isGoToolIgnoredDir(info, path, s.root) ||
		isNestedModuleBoundary(info, path, s.root)
}

// isGoToolIgnoredDir reports whether the directory at path, below the import scan's root,
// is one the go command never builds a package from when it expands "./...": a directory
// named testdata, or one whose name begins with "_" (see "go help packages"). Its sources
// are fixtures or parked code, not imports of the module. The root itself is never
// ignored: the caller named it.
func isGoToolIgnoredDir(info os.FileInfo, path, root string) bool {
	if info == nil || !info.IsDir() || filepath.Clean(path) == root {
		return false
	}
	name := info.Name()
	return name == "testdata" || strings.HasPrefix(name, "_")
}

// isNestedModuleBoundary reports whether the directory at path, below the import scan's
// root, starts another Go module (its own go.mod) or another repository (a nested
// checkout, as a submodule or an independent clone is). Its imports are not the root
// module's: a nested module is scanned as a sub-project of its own, and a nested checkout
// is a fleet repository with its own demand.
func isNestedModuleBoundary(info os.FileInfo, path, root string) bool {
	if info == nil || !info.IsDir() || filepath.Clean(path) == root {
		return false
	}
	return util.FileExists(filepath.Join(path, "go.mod")) || topology.HasValidGitRepo(path)
}

// isScannableGoFile reports whether info is a regular, non-test .go source file the go
// command would compile. Symlinks are excluded: their target may live outside the scanned
// repository. Names beginning with "_" or "." are excluded because the go command ignores
// them (see "go help packages").
func isScannableGoFile(info os.FileInfo) bool {
	if info == nil || info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return false
	}
	name := info.Name()
	if strings.HasPrefix(name, "_") || strings.HasPrefix(name, ".") {
		return false
	}
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
		if isExcludedDirSegment(segments[i], i) {
			return true
		}
	}
	return false
}

// isExcludedDirSegment reports whether the path segment at index depth (0 = directly
// under the walk root) names a directory that never contains first-party sources.
// vendor/, node_modules/ and dot-directories are excluded at any depth. scratch/ and
// cache/ are local work areas only at the walk root; deeper, as in src/cache or
// internal/cache, they are ordinary package directories and are scanned (BUG-864).
func isExcludedDirSegment(name string, depth int) bool {
	switch name {
	case "vendor", "node_modules":
		return true
	case "scratch", "cache":
		return depth == 0
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
// the computed set would freeze Capabilities.Required at its first written value. A
// .needs.yaml read through a deprecated key passes its deprecation on to the new row.
func loadExistingDeclarations(ctx context.Context, repoPath string, repoNeeds *RepoNeeds) error {
	needsPath := filepath.Join(repoPath, ".needs.yaml")
	if util.FileExists(needsPath) {
		var existing RepoNeeds
		if err := readYAMLFile(ctx, needsPath, &existing); err != nil {
			return err
		}
		mergeCapabilities(repoNeeds, existing.Capabilities)
		for _, deprecation := range existing.Deprecations {
			repoNeeds.Deprecations = appendUniqueStr(repoNeeds.Deprecations, deprecation)
		}
		return nil
	}

	standardsPath := filepath.Join(repoPath, ".standards.yaml")
	if util.FileExists(standardsPath) {
		var st struct {
			Needs CapabilityDeclaration `yaml:"needs"`
		}
		if err := readYAMLFile(ctx, standardsPath, &st); err != nil {
			return err
		}
		mergeCapabilities(repoNeeds, st.Needs)
	}
	return nil
}

// readYAMLFile reads one section of a repository-local declaration file through
// config.ReadYAMLDocument: a FIFO at the path is refused instead of blocking the scan past
// its deadline (BUG-822), and a second YAML document is refused rather than ignored (BUG-857).
// Keys outside the section are tolerated; the file's full schema belongs to its own loader.
func readYAMLFile(ctx context.Context, path string, out any) error {
	return config.ReadYAMLDocument(ctx, path, out, util.YAMLDocumentOptions{AllowEmpty: true})
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

// buildDependencyDemands classifies discovered packages into capabilities. AST imports are
// collapsed onto the module that owns them so that importing several packages of one module
// counts as a single dependency.
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

// buildGoDemand classifies a single Go module path through the catalog. Every demand starts
// as a gap: a framework contract decides what covers it (reconcileDependency).
func buildGoDemand(pkg, ver string) DependencyDemand {
	return classifyGoDemand(pkg, ver, "custom.", "Third-party package without a target framework equivalent")
}

// classifyGoDemand classifies a Go module path; a path the catalog does not list takes an
// external capability under externalPrefix, explained by externalNote.
func classifyGoDemand(pkg, ver, externalPrefix, externalNote string) DependencyDemand {
	demand := DependencyDemand{Package: pkg, Version: ver, Language: "go", Ecosystem: "go", Status: StatusGap}
	if entry, found := MatchPackage(pkg); found {
		demand.Capability, demand.Notes = entry.Capability, entry.Notes
		return demand
	}
	demand.Capability, demand.Notes = CapabilityKey(externalPrefix+cleanDepKey(pkg)), externalNote
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
	if util.IsGoMajorVersionElement(segments[depth]) {
		root += "/" + segments[depth]
	}
	return root
}

// calculateReadiness computes the framework adoption score and dependency counts. A row
// that names no framework has the basis not-configured, which renders as n/a
// (MappingAvailability).
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

	basis := FrameworkCatalogDeclared
	if repoNeeds.Framework == "" {
		basis = FrameworkNotConfigured
	}
	repoNeeds.Readiness = ReadinessMetrics{
		Basis:               basis,
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
	targetFile := filepath.Join(repoPath, NeedsManifestName)
	data, err := marshalNeedsManifest(repoNeeds)
	if err != nil {
		return err
	}
	if err := util.WriteFileSecure(targetFile, data, util.SecureFilePerm); err != nil {
		return fmt.Errorf("failed to write %s: %w", targetFile, err)
	}
	return nil
}
