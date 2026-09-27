package needs

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeFixture(t *testing.T, dir, name, content string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("failed creating %q: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("failed writing %q: %v", path, err)
	}
	return path
}

func TestShouldSkipDir3D(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, root, filepath.Join("vendor", "x.go"), "package x\n")
	writeFixture(t, root, filepath.Join("pkg", "x.go"), "package x\n")

	rootInfo, err := os.Lstat(root)
	if err != nil {
		t.Fatal(err)
	}
	// Positive: a real excluded directory is skipped.
	vendorInfo, err := os.Lstat(filepath.Join(root, "vendor"))
	if err != nil {
		t.Fatal(err)
	}
	if !shouldSkipDir(vendorInfo, filepath.Join(root, "vendor"), root) {
		t.Error("expected vendor/ to be skipped")
	}
	// Negative: an ordinary package directory is kept.
	pkgInfo, err := os.Lstat(filepath.Join(root, "pkg"))
	if err != nil {
		t.Fatal(err)
	}
	if shouldSkipDir(pkgInfo, filepath.Join(root, "pkg"), root) {
		t.Error("expected pkg/ to be walked")
	}
	// Boundary: the walk root itself is never skipped, including the "." spelling whose
	// base name starts with a dot.
	if shouldSkipDir(rootInfo, root, root) {
		t.Error("walk root must never be skipped")
	}
	if shouldSkipDir(rootInfo, ".", ".") {
		t.Error(`walk root "." must never be skipped`)
	}
}

// TestShouldSkipDirScopesScratchAndCacheToWalkRoot pins BUG-864: scratch/ and cache/ are
// local work areas only directly under the walk root; nested, they are package names.
func TestShouldSkipDirScopesScratchAndCacheToWalkRoot(t *testing.T) {
	root := t.TempDir()
	cases := map[string]bool{
		"scratch":                            true,
		"cache":                              true,
		".workingdir":                        true,
		filepath.Join("pkg", "vendor"):       true,
		filepath.Join("web", "node_modules"): true,
		filepath.Join("src", ".hidden"):      true,
		filepath.Join("src", "cache"):        false,
		filepath.Join("internal", "scratch"): false,
		filepath.Join("scratch", "cache"):    true,
	}
	for rel, want := range cases {
		dir := filepath.Join(root, rel)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		info, err := os.Lstat(dir)
		if err != nil {
			t.Fatal(err)
		}
		if got := shouldSkipDir(info, dir, root); got != want {
			t.Errorf("shouldSkipDir(%s) = %v, want %v", rel, got, want)
		}
	}
}

// TestScanRepoScansNestedCachePackage checks the scan end to end: imports in
// internal/cache are demand, imports in the root-level scratch/ are not.
func TestScanRepoScansNestedCachePackage(t *testing.T) {
	dir := t.TempDir()
	writeFixture(t, dir, "go.mod", "module example.com/cached\n\ngo 1.24\n")
	writeFixture(t, dir, filepath.Join("internal", "cache", "cache.go"),
		"package cache\n\nimport \"github.com/redis/rueidis\"\n\nvar _ rueidis.Client\n")
	writeFixture(t, dir, filepath.Join("scratch", "try.go"),
		"package scratch\n\nimport \"github.com/gin-gonic/gin\"\n\nvar _ = gin.Default\n")

	repoNeeds, err := ScanRepo(context.Background(), dir, nil)
	if err != nil {
		t.Fatalf("ScanRepo() error = %v", err)
	}
	packages := make([]string, 0, len(repoNeeds.Dependencies))
	for _, dep := range repoNeeds.Dependencies {
		packages = append(packages, dep.Package)
	}
	if len(packages) != 1 || packages[0] != "github.com/redis/rueidis" {
		t.Fatalf("dependencies = %v, want only the internal/cache import", packages)
	}
}

func TestScanRepoFromRelativeDotPath(t *testing.T) {
	dir := t.TempDir()
	writeFixture(t, dir, "go.mod", "module example.com/dotscan\n\ngo 1.24\n")
	writeFixture(t, dir, "main.go",
		"package main\n\nimport \"github.com/gin-gonic/gin\"\n\nvar _ = gin.Default\n")

	t.Chdir(dir)
	repoNeeds, err := ScanRepo(context.Background(), ".", nil)
	if err != nil {
		t.Fatalf("scan from \".\" failed: %v", err)
	}
	if repoNeeds.Readiness.TotalThirdPartyDeps != 1 {
		t.Fatalf("expected the AST import to be discovered from \".\", got %d deps",
			repoNeeds.Readiness.TotalThirdPartyDeps)
	}
	if repoNeeds.Dependencies[0].Package != "github.com/gin-gonic/gin" {
		t.Fatalf("unexpected dependency %q", repoNeeds.Dependencies[0].Package)
	}
}

func TestIsThirdPartyImport3D(t *testing.T) {
	cases := []struct {
		name       string
		importPath string
		modulePath string
		want       bool
	}{
		{"own module root", "github.com/acme/foo", "github.com/acme/foo", false},
		{"own subpackage", "github.com/acme/foo/bar", "github.com/acme/foo", false},
		{"sibling sharing a prefix", "github.com/acme/foo-plugins/auth", "github.com/acme/foo", true},
		{"stdlib", "strings", "github.com/acme/foo", false},
		{"empty module prefix keeps externals", "go.uber.org/zap", "", true},
		{"short directory name is not a prefix", "golang.org/x/sync", "", true},
	}
	for _, tc := range cases {
		if got := isThirdPartyImport(tc.importPath, tc.modulePath); got != tc.want {
			t.Errorf("%s: isThirdPartyImport(%q, %q) = %v, want %v",
				tc.name, tc.importPath, tc.modulePath, got, tc.want)
		}
	}
}

func TestResolveModuleRoot3D(t *testing.T) {
	directDeps := map[string]string{"github.com/jackc/pgx/v5": "v5.7.2"}
	cases := []struct {
		name string
		imp  string
		want string
	}{
		{"declared module root", "github.com/jackc/pgx/v5", "github.com/jackc/pgx/v5"},
		{"declared subpackage", "github.com/jackc/pgx/v5/pgxpool", "github.com/jackc/pgx/v5"},
		{"undeclared host convention", "github.com/foo/bar/v4/sub", "github.com/foo/bar/v4"},
		{"undeclared two-segment host", "go.uber.org/zap/zapcore", "go.uber.org/zap"},
		{"vanity two-segment path", "gopkg.in/yaml.v3", "gopkg.in/yaml.v3"},
		{"golang.org/x three segments", "golang.org/x/sync/errgroup", "golang.org/x/sync"},
		// A sibling sharing the declared module's prefix is not owned by it, and a trailing
		// separator falls back to the host convention.
		{"sibling of a declared module", "github.com/jackc/pgx/v5x/sub", "github.com/jackc/pgx"},
		{"trailing separator", "github.com/jackc/pgx/v5/", "github.com/jackc/pgx/v5"},
	}
	for _, tc := range cases {
		if got := ResolveModuleRoot(tc.imp, directDeps); got != tc.want {
			t.Errorf("%s: ResolveModuleRoot(%q) = %q, want %q", tc.name, tc.imp, got, tc.want)
		}
	}
}

func TestScanRepoCollapsesSubpackageImports(t *testing.T) {
	dir := t.TempDir()
	writeFixture(t, dir, "go.mod",
		"module example.com/sub\n\ngo 1.24\n\nrequire github.com/jackc/pgx/v5 v5.7.2\n")
	writeFixture(t, dir, "main.go", "package main\n\nimport (\n\t\"github.com/jackc/pgx/v5/pgxpool\"\n"+
		"\t\"github.com/jackc/pgx/v5/pgconn\"\n)\n\nvar _, _ = pgxpool.New, pgconn.Config{}\n")

	repoNeeds, err := ScanRepo(context.Background(), dir, acmeRegistry(t))
	if err != nil {
		t.Fatalf("scan failed: %v", err)
	}
	if repoNeeds.Readiness.TotalThirdPartyDeps != 1 {
		t.Fatalf("expected 1 module dependency, got %d (%v)",
			repoNeeds.Readiness.TotalThirdPartyDeps, repoNeeds.Dependencies)
	}
	if repoNeeds.Readiness.Score != 100.0 {
		t.Fatalf("expected 100%% readiness for one covered module, got %f", repoNeeds.Readiness.Score)
	}
}

func TestBuildDependencyDemandsCustomCapabilityUsesFullPath(t *testing.T) {
	repoNeeds := &RepoNeeds{}
	directDeps := map[string]string{
		"github.com/golang-migrate/migrate/v4": "v4.17.0",
		"github.com/googleapis/gax-go/v2":      "v2.12.0",
	}
	buildDependencyDemands(directDeps, map[string]struct{}{}, repoNeeds)

	seen := make(map[CapabilityKey]struct{})
	for _, d := range repoNeeds.Dependencies {
		if _, dup := seen[d.Capability]; dup {
			t.Fatalf("unrelated modules collapsed onto capability %q", d.Capability)
		}
		seen[d.Capability] = struct{}{}
		if d.Ecosystem != "go" || d.Language != "go" {
			t.Errorf("expected Go ecosystem on %q, got %q/%q", d.Package, d.Language, d.Ecosystem)
		}
	}
	if len(seen) != 2 {
		t.Fatalf("expected 2 distinct capabilities, got %d", len(seen))
	}
}

func TestScanRepoNegativeUnanalyzableRepo(t *testing.T) {
	dir := t.TempDir()
	writeFixture(t, dir, ".standards.yaml", "repository:\n  name: docs\n  owner: acme\n")

	_, err := ScanRepo(context.Background(), dir, nil)
	if err == nil {
		t.Fatal("expected an error for a repository no analyzer recognises")
	}
	if !errors.Is(err, ErrNoAnalyzer) {
		t.Fatalf("expected ErrNoAnalyzer, got %v", err)
	}
}

func TestScanRepoNegativeBrokenPackageJSON(t *testing.T) {
	dir := t.TempDir()
	writeFixture(t, dir, "package.json", "{ this is not json ")

	repoNeeds, err := ScanRepo(context.Background(), dir, nil)
	if err == nil {
		t.Fatalf("expected an error, got a fabricated manifest: %+v", repoNeeds)
	}
	if strings.Contains(err.Error(), "go.mod") {
		t.Fatalf("error must come from the Node analyzer, not a Go fallback: %v", err)
	}
}

func TestScanASTImportsNegativeCancelledContext(t *testing.T) {
	dir := t.TempDir()
	writeFixture(t, dir, "main.go", "package main\n\nimport \"github.com/gin-gonic/gin\"\n\nvar _ = gin.Default\n")

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	imports, err := scanASTImports(ctx, dir, "example.com/x")
	if err == nil {
		t.Fatalf("expected a context error, got a truncated success with %d imports", len(imports))
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
}

func TestLoadExistingDeclarationsMergesInsteadOfReplacing(t *testing.T) {
	dir := t.TempDir()
	writeFixture(t, dir, ".needs.yaml",
		"capabilities:\n  required:\n    - ui.framework\n  optional:\n    - telemetry.logging\n")

	repoNeeds := &RepoNeeds{Capabilities: CapabilityDeclaration{Required: []CapabilityKey{"http.client"}}}
	if err := loadExistingDeclarations(t.Context(), dir, repoNeeds); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(repoNeeds.Capabilities.Required) != 2 {
		t.Fatalf("expected computed and declared capabilities to merge, got %v",
			repoNeeds.Capabilities.Required)
	}
	if len(repoNeeds.Capabilities.Optional) != 1 {
		t.Fatalf("expected the declared optional capability, got %v", repoNeeds.Capabilities.Optional)
	}
}

func TestLoadExistingDeclarationsNegativeMalformedYAML(t *testing.T) {
	dir := t.TempDir()
	writeFixture(t, dir, ".needs.yaml", "capabilities: [this: is: not: valid\n")

	if err := loadExistingDeclarations(t.Context(), dir, &RepoNeeds{}); err == nil {
		t.Fatal("expected a parse error for malformed .needs.yaml")
	}
}

func TestParseGoMod3D(t *testing.T) {
	dir := t.TempDir()
	// Positive: module, go version and a require block.
	path := writeFixture(t, dir, "go.mod",
		"module example.com/svc\n\ngo 1.24\n\nrequire (\n\tgithub.com/a/b v1.0.0\n"+
			"\tgithub.com/c/d v2.0.0 // indirect\n)\n")
	modulePath, goVer, deps, err := parseGoMod(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if modulePath != "example.com/svc" || goVer != "1.24" {
		t.Fatalf("unexpected module %q / go %q", modulePath, goVer)
	}
	if len(deps) != 1 || deps["github.com/a/b"] != "v1.0.0" {
		t.Fatalf("unexpected direct deps: %v", deps)
	}

	// Negative: a missing go.mod must not look like a successful empty module.
	if _, _, _, err := parseGoMod(filepath.Join(dir, "absent", "go.mod")); !errors.Is(err, ErrGoModMissing) {
		t.Fatalf("expected ErrGoModMissing, got %v", err)
	}

	// Boundary: a go.mod with only a module directive yields no dependencies.
	bare := writeFixture(t, dir, filepath.Join("bare", "go.mod"), "module example.com/bare\n")
	_, _, bareDeps, err := parseGoMod(bare)
	if err != nil || len(bareDeps) != 0 {
		t.Fatalf("expected an empty dependency set, got %v (err %v)", bareDeps, err)
	}
}

// TestParseGoModReadsDirectivesLikeTheGoCommand: trailing comments on the module and go
// lines, a quoted module path and the "//indirect" spelling are read the way
// golang.org/x/mod/modfile reads them. The former prefix scanner kept the comment inside
// the module path and the go version, and counted a "//indirect" requirement as direct.
func TestParseGoModReadsDirectivesLikeTheGoCommand(t *testing.T) {
	dir := t.TempDir()
	path := writeFixture(t, dir, "go.mod",
		"module \"example.com/svc\" // Deprecated: use example.com/svc/v2\n\n"+
			"go 1.27 // raised for range-over-func\n\nrequire (\n"+
			"\tgithub.com/a/b v1.0.0 // kept for the CLI\n"+
			"\tgithub.com/c/d/v2 v2.0.0 //indirect\n"+
			"\tgithub.com/e/f v1.2.0 // indirect; pulled in by github.com/a/b\n)\n")
	modulePath, goVer, deps, err := parseGoMod(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if modulePath != "example.com/svc" || goVer != "1.27" {
		t.Fatalf("module %q / go %q, want example.com/svc / 1.27", modulePath, goVer)
	}
	if len(deps) != 1 || deps["github.com/a/b"] != "v1.0.0" {
		t.Fatalf("direct deps = %v, want only github.com/a/b v1.0.0", deps)
	}
}

// TestParseGoModNegativeMentionOfIndirectStaysDirect: a comment that merely contains the
// word "indirect" does not mark the requirement indirect; the former substring check
// dropped such a direct dependency from the report.
func TestParseGoModNegativeMentionOfIndirectStaysDirect(t *testing.T) {
	path := writeFixture(t, t.TempDir(), "go.mod",
		"module example.com/svc\n\nrequire github.com/a/b v1.0.0 // indirectly exercised by e2e\n")
	_, _, deps, err := parseGoMod(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if deps["github.com/a/b"] != "v1.0.0" {
		t.Fatalf("direct deps = %v, want github.com/a/b v1.0.0", deps)
	}
}

// TestParseGoModBoundaryCommentOnlyDirectives: a module or go directive whose only
// argument is a comment declares nothing, instead of a comment posing as a module path.
func TestParseGoModBoundaryCommentOnlyDirectives(t *testing.T) {
	path := writeFixture(t, t.TempDir(), "go.mod", "module // TODO\n\ngo // TODO\n")
	modulePath, goVer, deps, err := parseGoMod(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if modulePath != "" || goVer != "" || len(deps) != 0 {
		t.Fatalf("module %q / go %q / deps %v, want all empty", modulePath, goVer, deps)
	}
}

// TestParseGoModQuotedRequirementsAndTabbedGoDirective: go.mod may quote a require path
// or version and separate the go keyword from its version with a tab; the go command
// reads both, so the scan records the bare path and the version instead of a path with
// its quotes attached or no Go version at all.
func TestParseGoModQuotedRequirementsAndTabbedGoDirective(t *testing.T) {
	path := writeFixture(t, t.TempDir(), "go.mod",
		"module example.com/svc\n\ngo\t1.27\n\nrequire \"github.com/a/b\" v1.0.0\n\nrequire (\n"+
			"\t\"github.com/c/d/v2\" \"v2.1.0\"\n\t\"github.com/e/f\" v1.2.0 // indirect\n)\n")
	_, goVer, deps, err := parseGoMod(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := map[string]string{"github.com/a/b": "v1.0.0", "github.com/c/d/v2": "v2.1.0"}
	if goVer != "1.27" || len(deps) != len(want) || deps["github.com/a/b"] != want["github.com/a/b"] ||
		deps["github.com/c/d/v2"] != want["github.com/c/d/v2"] {
		t.Fatalf("go %q / direct deps %v, want 1.27 / %v", goVer, deps, want)
	}
}

// TestParseGoModNegativeMalformedQuotedRequirement: a require path the go command refuses
// -- an unterminated or single-quoted string, or a quote inside an unquoted path -- is
// not recorded as a dependency under a garbled name.
func TestParseGoModNegativeMalformedQuotedRequirement(t *testing.T) {
	path := writeFixture(t, t.TempDir(), "go.mod",
		"module example.com/svc\n\nrequire (\n\t\"github.com/a/b v1.0.0\n"+
			"\t'github.com/c/d' v1.0.0\n\tgithub.com/e/\"f\" v1.0.0\n\tgithub.com/g/h v1.1.0\n)\n")
	_, _, deps, err := parseGoMod(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(deps) != 1 || deps["github.com/g/h"] != "v1.1.0" {
		t.Fatalf("direct deps = %v, want only github.com/g/h v1.1.0", deps)
	}
}

// TestParseGoModBoundaryEmptyQuotedRequirement: an empty quoted path or version names no
// module, and a quoted path without a version is incomplete.
func TestParseGoModBoundaryEmptyQuotedRequirement(t *testing.T) {
	path := writeFixture(t, t.TempDir(), "go.mod",
		"module example.com/svc\n\nrequire \"\" v1.0.0\n\nrequire github.com/a/b \"\"\n\nrequire \"github.com/c/d\"\n")
	_, _, deps, err := parseGoMod(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(deps) != 0 {
		t.Fatalf("direct deps = %v, want none", deps)
	}
}

// TestScanASTImportsSkipsGoToolIgnoredSources: testdata/ and _-prefixed directories and
// _- and .-prefixed files are never compiled by the go command ("go help packages"), so
// their imports are fixtures, not demand. A scan rooted in a directory named testdata
// still reads it.
func TestScanASTImportsSkipsGoToolIgnoredSources(t *testing.T) {
	dir := t.TempDir()
	writeFixture(t, dir, "main.go", "package main\n\nimport _ \"github.com/gin-gonic/gin\"\n")
	writeFixture(t, dir, filepath.Join("testdata", "fixture.go"), "package fixture\n\nimport _ \"github.com/spf13/cobra\"\n")
	writeFixture(t, dir, filepath.Join("pkg", "testdata", "golden.go"), "package golden\n\nimport _ \"github.com/spf13/viper\"\n")
	writeFixture(t, dir, filepath.Join("_parked", "old.go"), "package old\n\nimport _ \"github.com/urfave/cli\"\n")
	writeFixture(t, dir, "_scratch.go", "package main\n\nimport _ \"github.com/sirupsen/logrus\"\n")
	writeFixture(t, dir, ".hidden.go", "package main\n\nimport _ \"github.com/pkg/errors\"\n")

	imports, err := scanASTImports(t.Context(), dir, "example.com/x")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, ok := imports["github.com/gin-gonic/gin"]; !ok || len(imports) != 1 {
		t.Fatalf("imports = %v, want only github.com/gin-gonic/gin", imports)
	}

	rooted, err := scanASTImports(t.Context(), filepath.Join(dir, "testdata"), "example.com/x")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, ok := rooted["github.com/spf13/cobra"]; !ok {
		t.Fatalf("a scan rooted at testdata/ must read it, got %v", rooted)
	}
}

// TestScanASTImportsBoundaryEntryLimit: a tree that fits the entry bound exactly is
// scanned in full; one entry more fails with ErrDiscoveryBound rather than returning the
// imports of the part that was reached (HISS-02).
func TestScanASTImportsBoundaryEntryLimit(t *testing.T) {
	dir := t.TempDir()
	writeFixture(t, dir, "a.go", "package a\n\nimport _ \"github.com/gin-gonic/gin\"\n")
	writeFixture(t, dir, "b.go", "package a\n\nimport _ \"github.com/spf13/cobra\"\n")
	const entries = 3 // the root directory and its two files

	imports, err := scanASTImportsBounded(t.Context(), dir, "example.com/x", entries)
	if err != nil || len(imports) != 2 {
		t.Fatalf("at the bound: imports = %v, err = %v; want both imports", imports, err)
	}
	imports, err = scanASTImportsBounded(t.Context(), dir, "example.com/x", entries-1)
	if !errors.Is(err, ErrDiscoveryBound) || imports != nil {
		t.Fatalf("past the bound: imports = %v, err = %v; want ErrDiscoveryBound and no imports", imports, err)
	}
}
