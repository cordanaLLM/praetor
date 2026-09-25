package needs

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/cordanaLLM/praetor/internal/util"
	"gopkg.in/yaml.v3"
)

func TestCatalogMatching(t *testing.T) {
	// Positive: Known package
	entry, found := MatchPackage("github.com/jackc/pgx/v5/pgxpool")
	if !found {
		t.Fatal("expected to find pgx in catalog")
	}
	if entry.Capability != "db.postgres" {
		t.Fatalf("expected db.postgres, got %s", entry.Capability)
	}
	if entry.Status != StatusCovered {
		t.Fatalf("expected StatusCovered, got %s", entry.Status)
	}
	if entry.GolusorisReplacement != "github.com/golusoris/golusoris/db/pgx" {
		t.Fatalf("unexpected replacement: %s", entry.GolusorisReplacement)
	}

	// Positive: Gin web framework
	entry, found = MatchPackage("github.com/gin-gonic/gin")
	if !found || entry.Capability != "http.router" {
		t.Fatalf("expected http.router for gin, got %v", entry)
	}

	// Positive: YAML serialization
	entry, found = MatchPackage("gopkg.in/yaml.v3")
	if !found || entry.Capability != "config.yaml" || entry.Status != StatusCovered || entry.GolusorisReplacement != "github.com/golusoris/golusoris/core/codec/yaml" {
		t.Fatalf("expected config.yaml covered by core/codec/yaml for yaml.v3, got %v", entry)
	}

	// Positive: MCP community server
	entry, found = MatchPackage("github.com/mark3labs/mcp-go/server")
	if !found || entry.Capability != "mcp.server" || entry.Status != StatusCovered {
		t.Fatalf("expected mcp.server for mark3labs/mcp-go, got %v", entry)
	}

	// Negative: Unknown package
	_, found = MatchPackage("github.com/unknown/obscure-package")
	if found {
		t.Fatal("expected unknown package not to be found")
	}
}

// TestMatchPackageBoundary pins that a catalog entry only matches on a path boundary: a
// different module that merely starts with the same characters must not inherit its
// capability, status and replacement.
func TestMatchPackageBoundary(t *testing.T) {
	cases := []struct {
		name       string
		importPath string
		wantFound  bool
		wantCap    CapabilityKey
	}{
		{"exact module", "github.com/uptrace/bun", true, "db.orm"},
		{"package inside module", "github.com/uptrace/bun/dialect/pgdialect", true, "db.orm"},
		{"sibling module sharing a prefix", "github.com/uptrace/bunrouter", false, ""},
		{"hyphen extension of an entry", "github.com/go-chi/chi-middleware", false, ""},
		{"empty import path", "", false, ""},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			entry, found := MatchPackage(tc.importPath)
			if found != tc.wantFound {
				t.Fatalf("MatchPackage(%q) found = %v, want %v (entry %+v)", tc.importPath, found, tc.wantFound, entry)
			}
			if found && entry.Capability != tc.wantCap {
				t.Fatalf("MatchPackage(%q) capability = %s, want %s", tc.importPath, entry.Capability, tc.wantCap)
			}
		})
	}
}

func TestResolveFrameworkModule(t *testing.T) {
	checkout := t.TempDir()
	if err := os.WriteFile(filepath.Join(checkout, "go.mod"), []byte("module github.com/acme/forkedfw\n\ngo 1.27\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	moduleless := t.TempDir()

	cases := []struct {
		name  string
		input string
		want  string
	}{
		{"empty falls back to the default", "", defaultFrameworkModule},
		{"checkout resolves through its go.mod", checkout, "github.com/acme/forkedfw"},
		{"checkout without go.mod falls back", moduleless, defaultFrameworkModule},
		{"module path is taken verbatim", "github.com/acme/otherkit", "github.com/acme/otherkit"},
		{"absolute path that does not exist falls back", "/nonexistent/dev/golusoris", defaultFrameworkModule},
		{"relative path falls back", "./golusoris", defaultFrameworkModule},
		{"bare word falls back", "golusoris", defaultFrameworkModule},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ResolveFrameworkModule(tc.input); got != tc.want {
				t.Fatalf("ResolveFrameworkModule(%q) = %q, want %q", tc.input, got, tc.want)
			}
		})
	}
}

func TestFrameworkInspection(t *testing.T) {
	ctx := context.Background()

	// Positive: Default framework inspection
	index, err := InspectFramework(ctx, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if index == nil {
		t.Fatal("expected index not to be nil")
	}
	if !index.IsCapabilityCovered("db.postgres") {
		t.Fatal("expected db.postgres to be covered")
	}
	if !index.IsCapabilityCovered("cache.redis") {
		t.Fatal("expected cache.redis to be covered")
	}

	// Negative: a capability no domain offers is not covered
	if index.IsCapabilityCovered("quantum.entanglement") {
		t.Fatal("expected an unknown capability not to be covered")
	}
	if index.ProvidesCapability("quantum.entanglement") {
		t.Fatal("expected an unknown capability not to be provided")
	}

	// Negative: Cancelled context
	cancCtx, cancel := context.WithCancel(ctx)
	cancel()
	_, err = InspectFramework(cancCtx, "")
	if err == nil {
		t.Fatal("expected error with cancelled context")
	}
}

// setupFrameworkCheckout builds a framework checkout carrying the given domain
// directories and a go.mod naming modulePath.
func setupFrameworkCheckout(t *testing.T, modulePath string, domains ...string) string {
	t.Helper()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module "+modulePath+"\n\ngo 1.27\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, d := range domains {
		if err := os.MkdirAll(filepath.Join(root, d), 0o750); err != nil {
			t.Fatal(err)
		}
	}
	// A dot directory and a plain file must both be ignored by the inspection.
	if err := os.MkdirAll(filepath.Join(root, ".github"), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("# fw\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return root
}

// TestFrameworkInspectionFromCheckout observes only exact catalog replacement packages.
func TestFrameworkInspectionFromCheckout(t *testing.T) {
	root := setupFrameworkCheckout(t, "github.com/acme/forkedfw", "db/pgx", "cache/redis", "quantum")
	writeFixture(t, root, "db/pgx/doc.go", "package pgx\ntype Available struct{}\n")
	writeFixture(t, root, "cache/redis/doc.go", "package redis\ntype Available struct{}\n")
	writeFixture(t, root, "quantum/doc.go", "package quantum\n")
	index, err := InspectFramework(t.Context(), root)
	if err != nil {
		t.Fatal(err)
	}
	if index.Name != "github.com/acme/forkedfw" || len(index.Packages) != 2 {
		t.Fatalf("unexpected observed catalog: %+v", index)
	}
	if _, ok := index.Packages["github.com/acme/forkedfw/db/pgx"]; !ok {
		t.Fatal("pgx package must use the checkout module identity")
	}
	if !index.IsCapabilityCovered("db.postgres") || !index.ProvidesCapability("cache.redis") {
		t.Fatal("exact catalog packages should be available")
	}
	for _, capability := range []CapabilityKey{"quantum.core", "quantum.entanglement", "http.router", "db.orm"} {
		if index.ProvidesCapability(capability) {
			t.Fatalf("undeclared capability %s", capability)
		}
	}
}

func TestFrameworkInspection_EmptyCheckout(t *testing.T) {
	index, err := InspectFramework(context.Background(), t.TempDir())
	if err != nil {
		t.Fatalf("inspect empty checkout failed: %v", err)
	}
	if len(index.Packages) != 0 || len(index.Capabilities) != 0 {
		t.Fatalf("expected an empty index, got %d packages", len(index.Packages))
	}
	if index.ProvidesCapability("db.postgres") {
		t.Fatal("an empty checkout provides nothing")
	}
}

func setupFixtureRepo(t *testing.T) string {
	t.Helper()
	tmpDir := t.TempDir()
	goModContent := "module example.com/testservice\n\ngo 1.24\n\nrequire (\n\tgithub.com/gin-gonic/gin v1.10.0\n\tgithub.com/jackc/pgx/v5 v5.7.2\n\tgithub.com/redis/rueidis v1.0.51\n\tgithub.com/unknown/gap-lib v1.0.0\n)\n"
	if err := os.WriteFile(filepath.Join(tmpDir, "go.mod"), []byte(goModContent), 0o600); err != nil {
		t.Fatal(err)
	}

	mainGoContent := "package main\n\nimport (\n\t\"fmt\"\n\t\"github.com/gin-gonic/gin\"\n\t\"github.com/jackc/pgx/v5\"\n\t\"github.com/redis/rueidis\"\n\t\"github.com/unknown/gap-lib\"\n)\n\nfunc main() {\n\tfmt.Println(\"test\")\n}\n"
	if err := os.WriteFile(filepath.Join(tmpDir, "main.go"), []byte(mainGoContent), 0o600); err != nil {
		t.Fatal(err)
	}
	return tmpDir
}

func TestScanRepoFixture(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	tmpDir := setupFixtureRepo(t)

	repoNeeds, err := ScanRepo(ctx, tmpDir)
	if err != nil {
		t.Fatalf("scan repo failed: %v", err)
	}
	if repoNeeds.Repository != "example.com/testservice" {
		t.Fatalf("unexpected repository: %s", repoNeeds.Repository)
	}
	if repoNeeds.Readiness.TotalThirdPartyDeps != 4 {
		t.Fatalf("expected 4 total deps, got %d", repoNeeds.Readiness.TotalThirdPartyDeps)
	}
	if repoNeeds.Readiness.CoveredDeps != 3 {
		t.Fatalf("expected 3 covered deps, got %d", repoNeeds.Readiness.CoveredDeps)
	}
	if repoNeeds.Readiness.GapDeps != 1 {
		t.Fatalf("expected 1 gap dep, got %d", repoNeeds.Readiness.GapDeps)
	}
	if repoNeeds.Readiness.Score != 75.0 {
		t.Fatalf("expected score 75.0, got %f", repoNeeds.Readiness.Score)
	}
}

// TestScanRepoManifestPersistence asserts what the manifest actually contains and that a
// rescan reads it back. Asserting only the recomputed readiness score would hold even if
// WriteNeedsManifest wrote nothing at all.
func TestScanRepoManifestPersistence(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	tmpDir := setupFixtureRepo(t)
	repoNeeds, err := ScanRepo(ctx, tmpDir)
	if err != nil {
		t.Fatalf("initial scan failed: %v", err)
	}
	repoNeeds.Capabilities.Required = []CapabilityKey{"db.postgres", "http.router"}

	if err := WriteNeedsManifest(tmpDir, repoNeeds); err != nil {
		t.Fatalf("failed to write manifest: %v", err)
	}

	assertPersistedNeedsManifest(t, filepath.Join(tmpDir, ".needs.yaml"), repoNeeds)

	// The declared capabilities are the part of the manifest a rescan reads back.
	rescan, err := ScanRepo(ctx, tmpDir)
	if err != nil {
		t.Fatalf("rescan failed: %v", err)
	}
	for _, declared := range repoNeeds.Capabilities.Required {
		if !slices.Contains(rescan.Capabilities.Required, declared) {
			t.Fatalf("rescan dropped declared capability %q: %+v", declared, rescan.Capabilities)
		}
	}
	if rescan.Readiness.Score != repoNeeds.Readiness.Score {
		t.Fatalf("expected score %f, got %f", repoNeeds.Readiness.Score, rescan.Readiness.Score)
	}
}

func assertPersistedNeedsManifest(t *testing.T, manifestPath string, repoNeeds *RepoNeeds) {
	t.Helper()
	data, err := os.ReadFile(manifestPath) // #nosec G304 -- test-local path from t.TempDir
	if err != nil {
		t.Fatalf("failed to read back the manifest: %v", err)
	}

	var persisted RepoNeeds
	if err := yaml.Unmarshal(data, &persisted); err != nil {
		t.Fatalf("manifest is not valid YAML: %v", err)
	}
	if persisted.Repository != repoNeeds.Repository {
		t.Errorf("manifest repository = %q, want %q", persisted.Repository, repoNeeds.Repository)
	}
	if len(persisted.Dependencies) != len(repoNeeds.Dependencies) {
		t.Fatalf("manifest holds %d dependencies, want %d", len(persisted.Dependencies), len(repoNeeds.Dependencies))
	}
	for i, dep := range repoNeeds.Dependencies {
		if persisted.Dependencies[i].Package != dep.Package || persisted.Dependencies[i].Status != dep.Status {
			t.Errorf("dependency %d = %+v, want %+v", i, persisted.Dependencies[i], dep)
		}
	}
	if persisted.Readiness != repoNeeds.Readiness {
		t.Errorf("manifest readiness = %+v, want %+v", persisted.Readiness, repoNeeds.Readiness)
	}

	info, err := os.Stat(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); util.ModeIsProtection() && perm != 0o600 {
		t.Errorf("expected the manifest to be owner-only, got %#o", perm)
	}
}

func TestWriteNeedsManifest_Negative(t *testing.T) {
	if err := WriteNeedsManifest(t.TempDir(), nil); err == nil {
		t.Fatal("expected an error for a nil manifest")
	}
	if err := WriteNeedsManifest(filepath.Join(t.TempDir(), "missing-dir"), &RepoNeeds{Version: 1}); err == nil {
		t.Fatal("expected an error when the repository directory does not exist")
	}
}

func TestScanRepoBoundaries(t *testing.T) {
	ctx := context.Background()

	// Boundary 1: Empty repo (no third-party dependencies)
	tmpEmpty := t.TempDir()
	goModEmpty := "module example.com/empty\n\ngo 1.24\n"
	if err := os.WriteFile(filepath.Join(tmpEmpty, "go.mod"), []byte(goModEmpty), 0o600); err != nil {
		t.Fatal(err)
	}

	needsEmpty, err := ScanRepo(ctx, tmpEmpty)
	if err != nil {
		t.Fatalf("empty scan failed: %v", err)
	}
	if needsEmpty.Readiness.Score != 100.0 {
		t.Fatalf("expected 100.0, got %f", needsEmpty.Readiness.Score)
	}

	// Boundary 2: 0% Readiness (only unknown gap libraries)
	tmpGap := t.TempDir()
	goModGap := "module example.com/gap\n\ngo 1.24\n\nrequire github.com/foo/bar v1.0.0\n"
	if err := os.WriteFile(filepath.Join(tmpGap, "go.mod"), []byte(goModGap), 0o600); err != nil {
		t.Fatal(err)
	}

	needsGap, err := ScanRepo(ctx, tmpGap)
	if err != nil {
		t.Fatalf("gap scan failed: %v", err)
	}
	if needsGap.Readiness.Score != 0.0 {
		t.Fatalf("expected 0.0, got %f", needsGap.Readiness.Score)
	}
}

// recordingRunner is a CommandRunner that records its invocations instead of executing
// them, which keeps the migration tests off git, the module proxy and the network.
type recordingRunner struct {
	calls  [][]string
	err    error
	failAt int
}

type migrationExitError int

func (e migrationExitError) Error() string { return "simulated command failure" }
func (e migrationExitError) ExitCode() int { return int(e) }

func (r *recordingRunner) run(ctx context.Context, _ string, name string, args ...string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	r.calls = append(r.calls, append([]string{name}, args...))
	if r.err != nil && (r.failAt == 0 || len(r.calls) == r.failAt) {
		return "", r.err
	}
	if name == "git" && len(args) >= 2 && args[0] == "rev-parse" {
		if args[1] == "--is-inside-work-tree" {
			return "true", nil
		}
		return "", migrationExitError(1)
	}
	return "", nil
}

func setupMigrationRepo(t *testing.T) string {
	t.Helper()
	tmpDir := t.TempDir()
	goMod := "module example.com/migratesvc\n\ngo 1.24\n\nrequire github.com/gin-gonic/gin v1.10.0\n"
	if err := os.WriteFile(filepath.Join(tmpDir, "go.mod"), []byte(goMod), 0o600); err != nil {
		t.Fatal(err)
	}
	src := "package main\n\nimport \"github.com/gin-gonic/gin\"\n\nfunc main() {}\n"
	if err := os.WriteFile(filepath.Join(tmpDir, "main.go"), []byte(src), 0o600); err != nil {
		t.Fatal(err)
	}
	return tmpDir
}

func TestPlanAndMigrationRewritePrimitives(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	tmpDir := setupMigrationRepo(t)

	plan, err := PlanMigration(ctx, tmpDir, "")
	if err != nil {
		t.Fatalf("plan migration failed: %v", err)
	}
	if len(plan.DroppedRequires) == 0 {
		t.Fatal("expected dropped requires")
	}
	if plan.Framework != defaultFrameworkModule {
		t.Fatalf("unexpected plan framework: %s", plan.Framework)
	}

	// Synthetic requirement exercises the writer; it is not verified release evidence.
	plan.AddedRequires = []string{defaultFrameworkModule + " v0.8.0"}
	runner := &recordingRunner{}
	res, err := rewriteMigrationFixture(ctx, tmpDir, plan, MigrationOptions{Runner: runner.run, SkipTidy: true})
	if err != nil {
		t.Fatalf("apply migration failed: %v", err)
	}
	if !res.Success || len(res.Warnings) != 0 {
		t.Fatalf("expected a clean migration, got success=%v warnings=%v", res.Success, res.Warnings)
	}
	if len(runner.calls) != 3 || strings.Join(runner.calls[2], " ") != "git switch -c "+migrationBranch {
		t.Fatalf("expected worktree/branch checks followed by safe branch creation, got %v", runner.calls)
	}
	if res.Branch != migrationBranch {
		t.Fatalf("unexpected branch: %s", res.Branch)
	}

	content, err := os.ReadFile(filepath.Join(tmpDir, "main.go")) // #nosec G304 -- test-local path
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(content), "github.com/golusoris/golusoris/httpx") {
		t.Fatal("expected import replacement in main.go")
	}

	goMod, err := os.ReadFile(filepath.Join(tmpDir, "go.mod")) // #nosec G304 -- test-local path
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(goMod), "github.com/gin-gonic/gin") {
		t.Fatal("expected the superseded require to be dropped from go.mod")
	}
	if !strings.Contains(string(goMod), defaultFrameworkModule) {
		t.Fatal("expected the framework require to be added to go.mod")
	}
	if _, statErr := os.Stat(filepath.Join(tmpDir, "MIGRATION.md")); statErr != nil {
		t.Fatalf("expected MIGRATION.md: %v", statErr)
	}
}

// TestPlanMigrationHonoursFramework pins that --framework reaches the plan.
func TestPlanMigrationHonoursFramework(t *testing.T) {
	ctx := context.Background()
	plan, err := PlanMigration(ctx, setupMigrationRepo(t), "github.com/acme/otherkit")
	if err != nil {
		t.Fatalf("plan migration failed: %v", err)
	}
	want := "github.com/acme/otherkit"
	if plan.Framework != want {
		t.Fatalf("plan framework = %q, want %q", plan.Framework, want)
	}
	if len(plan.AddedRequires) != 0 || len(plan.Replacements) != 0 || plan.CoverageBasis != FrameworkIdentityDeclared {
		t.Fatalf("identity-only selection must remain unresolved: %+v", plan)
	}
}

func TestApplyMigration_Negative(t *testing.T) {
	ctx := context.Background()
	tmpDir := setupMigrationRepo(t)

	if _, err := ApplyMigration(ctx, tmpDir, nil); !errors.Is(err, ErrNilMigrationPlan) {
		t.Fatalf("expected ErrNilMigrationPlan, got %v", err)
	}

	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := ApplyMigrationWithOptions(cancelled, tmpDir, &MigrationPlan{}, MigrationOptions{}); err == nil {
		t.Fatal("expected an error for a cancelled context")
	}

	plan, err := PlanMigration(ctx, tmpDir, "")
	if err != nil {
		t.Fatal(err)
	}
	failing := &recordingRunner{err: errors.New("git is unavailable")}
	res, err := rewriteMigrationFixture(ctx, tmpDir, plan, MigrationOptions{Runner: failing.run})
	if err == nil || res == nil || res.Success || res.Error == "" {
		t.Fatalf("expected result plus hard failure, got result=%+v err=%v", res, err)
	}
	if len(res.Warnings) != 1 || len(failing.calls) != 1 || len(res.FilesChanged) != 0 {
		t.Fatalf("expected first failure retained without later mutations, got result=%+v calls=%v", res, failing.calls)
	}
}

func TestMigrationRewrite_Boundary(t *testing.T) {
	ctx := context.Background()
	// Boundary: a repository with no go.mod and a plan with no replacements.
	tmpDir := t.TempDir()
	runner := &recordingRunner{}
	plan := &MigrationPlan{Repository: "example.com/bare", GuideMarkdown: "# guide\n"}

	res, err := rewriteMigrationFixture(ctx, tmpDir, plan, MigrationOptions{Runner: runner.run})
	if err != nil {
		t.Fatalf("apply on a bare repository failed: %v", err)
	}
	if len(res.FilesChanged) != 1 || filepath.Base(res.FilesChanged[0]) != "MIGRATION.md" {
		t.Fatalf("expected only the guide to change, got %v", res.FilesChanged)
	}

	// A replacement pointing outside the repository must be refused, not applied.
	outside := filepath.Join(t.TempDir(), "victim.go")
	if err := os.WriteFile(outside, []byte("package victim\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	escaping := &MigrationPlan{
		Repository: "example.com/bare",
		Replacements: []ReplacementAction{
			{File: outside, OldImport: "package", NewImport: "hacked"},
		},
	}
	res, err = rewriteMigrationFixture(ctx, tmpDir, escaping, MigrationOptions{Runner: runner.run})
	if err == nil || res == nil || res.Success {
		t.Fatalf("expected a hard confinement failure, got result=%+v err=%v", res, err)
	}
	if len(res.Warnings) == 0 {
		t.Fatal("expected the out-of-tree rewrite to be refused")
	}
	data, err := os.ReadFile(outside) // #nosec G304 -- test-local path
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "package victim\n" {
		t.Fatalf("a file outside the repository was rewritten: %q", string(data))
	}
}

func setupFleetRoot(t *testing.T) string {
	t.Helper()
	tmpRoot := t.TempDir()

	repo1 := filepath.Join(tmpRoot, "repo1")
	if err := os.MkdirAll(repo1, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo1, "go.mod"),
		[]byte("module r1\ngo 1.24\nrequire github.com/jackc/pgx v5.0.0\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	repo2 := filepath.Join(tmpRoot, "repo2")
	if err := os.MkdirAll(repo2, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo2, "go.mod"),
		[]byte("module r2\ngo 1.24\nrequire github.com/jackc/pgx v5.0.0\nrequire github.com/unknown/lib v1.0.0\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return tmpRoot
}

func TestAggregateFleet(t *testing.T) {
	ctx := context.Background()
	tmpRoot := setupFleetRoot(t)

	report, err := AggregateFleet(ctx, tmpRoot, "")
	if err != nil {
		t.Fatalf("aggregate fleet failed: %v", err)
	}
	if report.ScannedRepositories != 2 {
		t.Fatalf("expected 2 scanned repos, got %d", report.ScannedRepositories)
	}
	if report.FailedRepositories != 0 {
		t.Fatalf("expected no scan failures, got %d", report.FailedRepositories)
	}
	if !report.CoverageKnown {
		t.Fatal("expected the coverage of a successful scan to be known")
	}
	if report.DemandFrequency["db.postgres"] != 2 {
		t.Fatalf("expected db.postgres count 2, got %d", report.DemandFrequency["db.postgres"])
	}

	md := RenderFrameworkDemandMarkdown(report)
	if !strings.Contains(md, "# Framework Demand & Capability Report") {
		t.Fatal("markdown missing title")
	}

	// The rendered report must be byte-identical across runs over an unchanged fleet.
	second, err := AggregateFleet(ctx, tmpRoot, "")
	if err != nil {
		t.Fatal(err)
	}
	second.GeneratedAt = report.GeneratedAt
	if RenderFrameworkDemandMarkdown(second) != md {
		t.Fatal("the rendered report is not deterministic across runs")
	}
}

// TestAggregateFleetHonoursFrameworkCheckout pins that the framework the operator points
// at decides coverage: a checkout that ships nothing turns every demand into a gap.
func TestAggregateFleetHonoursFrameworkCheckout(t *testing.T) {
	ctx := context.Background()
	tmpRoot := setupFleetRoot(t)

	empty, err := AggregateFleet(ctx, tmpRoot, t.TempDir())
	if err != nil {
		t.Fatalf("aggregate against an empty framework failed: %v", err)
	}
	if empty.OverallFleetCoverage != 0 {
		t.Fatalf("expected 0%% coverage against an empty framework, got %.1f", empty.OverallFleetCoverage)
	}
	if len(empty.Gaps) == 0 {
		t.Fatal("expected every demand to be a gap against an empty framework")
	}

	dbOnly := setupFrameworkCheckout(t, "github.com/acme/forkedfw", "db/pgx")
	writeFixture(t, dbOnly, "db/pgx/doc.go", "package pgx\ntype Available struct{}\n")
	partial, err := AggregateFleet(ctx, tmpRoot, dbOnly)
	if err != nil {
		t.Fatalf("aggregate against a partial framework failed: %v", err)
	}
	if partial.Framework != "github.com/acme/forkedfw" {
		t.Fatalf("report names %q, not the inspected framework", partial.Framework)
	}
	if partial.OverallFleetCoverage <= empty.OverallFleetCoverage {
		t.Fatalf("a framework shipping db must cover more than an empty one: %.1f vs %.1f",
			partial.OverallFleetCoverage, empty.OverallFleetCoverage)
	}
}

// TestAggregateFleetWithHarvestDeduplicates covers the harvest branch, which no test
// exercised, and pins that a repository present in both sources is counted once.
func TestAggregateFleetWithHarvestDeduplicates(t *testing.T) {
	ctx := context.Background()
	tmpRoot := t.TempDir()

	repo := filepath.Join(tmpRoot, "testservice")
	if err := os.MkdirAll(repo, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "go.mod"),
		[]byte("module example.com/testservice\ngo 1.24\nrequire github.com/jackc/pgx v5.0.0\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	harvest := t.TempDir()
	invDir := filepath.Join(harvest, "dev-inventory")
	if err := os.MkdirAll(invDir, 0o750); err != nil {
		t.Fatal(err)
	}
	// brandnew carries a language signal in its name: a harvested repository with none is
	// skipped as unsupported rather than scored (BUG-864). The harvested testservice has
	// none either, and is still folded into the on-disk row instead of counted again.
	inventory := `[
		{"Name": "testservice", "Type": "Git", "Remote": "git@github.com:org/testservice.git"},
		{"Name": "brandnew-svelte", "Type": "Git", "Remote": "git@github.com:org/brandnew.git"}
	]`
	if err := os.WriteFile(filepath.Join(invDir, "dev-inventory.json"), []byte(inventory), 0o600); err != nil {
		t.Fatal(err)
	}

	report, err := AggregateFleetWithHarvest(ctx, tmpRoot, "", harvest)
	if err != nil {
		t.Fatalf("harvest aggregation failed: %v", err)
	}
	if report.ScannedRepositories != 2 {
		t.Fatalf("expected the duplicate to be folded into one row, got %d", report.ScannedRepositories)
	}
	if report.TotalRepositories != 2 {
		t.Fatalf("expected 2 total repositories, got %d", report.TotalRepositories)
	}
	if len(report.Leaderboard) != 2 {
		t.Fatalf("expected 2 leaderboard rows, got %d", len(report.Leaderboard))
	}
	if report.DemandFrequency["db.postgres"] != 1 {
		t.Fatalf("expected the deduplicated repo's demand to be counted once, got %d",
			report.DemandFrequency["db.postgres"])
	}

	// Negative: an unreadable harvest bundle is an error, not a silent empty merge.
	if _, err := AggregateFleetWithHarvest(ctx, tmpRoot, "", filepath.Join(t.TempDir(), "missing")); err == nil {
		t.Fatal("expected an error for a harvest path that is not a directory")
	}
	if _, err := AggregateFleetWithHarvest(ctx, tmpRoot, "", t.TempDir()); err == nil {
		t.Fatal("expected an error for a harvest bundle without an inventory")
	}
}

// TestAggregateFleetReportsTotalScanFailure pins that an aggregation in which nothing
// could be scanned is an error and renders as unknown, never as 100% with zero gaps.
func TestAggregateFleetReportsTotalScanFailure(t *testing.T) {
	fwIndex, err := InspectFramework(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}

	cancelled, cancel := context.WithCancel(context.Background())
	cancel()

	agg := newFleetAggregation("/fleet", fwIndex, 2)
	for i := 0; i < 2; i++ {
		dir := t.TempDir()
		writeFixture(t, dir, "go.mod", "module example.com/broken\ngo 1.24\n")
		writeFixture(t, dir, ".needs.yaml", "capabilities: [unterminated")
		if err := agg.scanRepoDir(context.Background(), dir); err != nil {
			t.Fatalf("ordinary scan failure must be retained in the report: %v", err)
		}
	}
	if err := agg.scanRepoDir(cancelled, t.TempDir()); !errors.Is(err, context.Canceled) {
		t.Fatalf("expected cancellation to stop aggregation, got %v", err)
	}
	compileGapsAndLeaderboard(agg.report, agg.gapPackages)

	if err := agg.result(); !errors.Is(err, ErrNoRepositoryScanned) {
		t.Fatalf("expected ErrNoRepositoryScanned, got %v", err)
	}
	if agg.report.FailedRepositories != 2 || len(agg.report.ScanErrors) != 2 {
		t.Fatalf("expected both failures to be recorded, got %d / %v",
			agg.report.FailedRepositories, agg.report.ScanErrors)
	}
	if agg.report.CoverageKnown || agg.report.OverallFleetCoverage != 0 {
		t.Fatalf("expected unknown coverage, got known=%v value=%.1f",
			agg.report.CoverageKnown, agg.report.OverallFleetCoverage)
	}

	md := RenderFrameworkDemandMarkdown(agg.report)
	if !strings.Contains(md, "unknown (no repository could be scanned)") {
		t.Errorf("report claims a coverage it does not have:\n%s", md)
	}
	if strings.Contains(md, "Zero gaps identified") {
		t.Errorf("report claims zero gaps after scanning nothing:\n%s", md)
	}

	// Boundary: an empty fleet root is not a failure.
	empty := newFleetAggregation("/fleet", fwIndex, 0)
	compileGapsAndLeaderboard(empty.report, empty.gapPackages)
	if err := empty.result(); err != nil {
		t.Fatalf("an empty fleet root must not be an error: %v", err)
	}
}

func TestRepoIdentityKey(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"example.com/testservice", "testservice"},
		{"git@github.com:org/testservice.git", "testservice"},
		{"https://github.com/org/TestService/", "testservice"},
		{"unknown", ""},
		{"", ""},
	}
	for _, tc := range cases {
		if got := repoIdentityKey(tc.in); got != tc.want {
			t.Errorf("repoIdentityKey(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestRenderFrameworkDemandMarkdown_NilReport(t *testing.T) {
	md := RenderFrameworkDemandMarkdown(nil)
	if !strings.Contains(md, "No report was produced.") {
		t.Fatalf("expected a nil report to render an explicit notice, got %q", md)
	}
}

func initGitFixture(t *testing.T, dir string) {
	t.Helper()
	confDir := t.TempDir()
	t.Setenv("GIT_CONFIG_GLOBAL", filepath.Join(confDir, "gitconfig"))
	t.Setenv("GIT_CONFIG_SYSTEM", filepath.Join(confDir, "gitconfig-system"))
	t.Setenv("GOPROXY", "off")
	t.Setenv("GOFLAGS", "-mod=mod")

	ctx := context.Background()
	run := func(args ...string) {
		t.Helper()
		out, err := util.RunCommand(ctx, dir, "git", args...)
		if err != nil {
			t.Fatalf("git %v failed: %v (%s)", args, err, out)
		}
	}
	run("init", "-b", "main")
	run("config", "user.name", "Standards Test Agent")
	run("config", "user.email", "agent@cordana.ai")
	run("add", "-A")
	run("commit", "-m", "initial commit")
}

func assertMigratedGoMod(t *testing.T, goModPath string) {
	t.Helper()
	data, err := os.ReadFile(goModPath)
	if err != nil {
		t.Fatal(err)
	}
	body := string(data)
	if strings.Contains(body, "github.com/gin-gonic/gin") {
		t.Fatalf("expected gin require to be dropped, got:\n%s", body)
	}
	if !strings.Contains(body, "module example.com/migratesvc") {
		t.Fatalf("module directive must survive the rewrite, got:\n%s", body)
	}
	if strings.Count(body, "github.com/golusoris/golusoris v0.8.0") != 1 {
		t.Fatalf("expected exactly one framework require, got:\n%s", body)
	}
}

func TestMigrationRewriteNegativeNonGitTarget(t *testing.T) {
	ctx := context.Background()
	tmpDir := setupMigrationRepo(t)

	plan, err := PlanMigration(ctx, tmpDir, "")
	if err != nil {
		t.Fatalf("plan migration failed: %v", err)
	}

	res, err := rewriteMigrationFixture(ctx, tmpDir, plan, MigrationOptions{})
	if !errors.Is(err, ErrNotGitRepo) {
		t.Fatalf("expected ErrNotGitRepo, got %v", err)
	}
	if res == nil || res.Success {
		t.Fatal("expected Success=false for a failed migration")
	}
	if res.Error == "" {
		t.Fatal("expected MigrationResult.Error to be populated")
	}

	content, err := os.ReadFile(filepath.Join(tmpDir, "main.go"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(content), "github.com/gin-gonic/gin") {
		t.Fatal("failed migration must not rewrite sources")
	}
}

func TestMigrationRewriteBoundaryExistingBranch(t *testing.T) {
	ctx := context.Background()
	tmpDir := setupMigrationRepo(t)
	initGitFixture(t, tmpDir)

	if out, err := util.RunCommand(ctx, tmpDir, "git", "branch", "refactor/golusoris-adoption"); err != nil {
		t.Fatalf("failed creating pre-existing branch: %v (%s)", err, out)
	}

	plan, err := PlanMigration(ctx, tmpDir, "")
	if err != nil {
		t.Fatalf("plan migration failed: %v", err)
	}

	res, err := rewriteMigrationFixture(ctx, tmpDir, plan, MigrationOptions{})
	if !errors.Is(err, ErrBranchExists) {
		t.Fatalf("expected ErrBranchExists, got %v", err)
	}
	if res == nil || res.Success {
		t.Fatal("expected Success=false when the adoption branch already exists")
	}
}
