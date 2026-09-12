package needs

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cordanaLLM/praetor/internal/util"
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
	if !found || entry.Capability != "config.yaml" || entry.Status != StatusGap {
		t.Fatalf("expected config.yaml gap for yaml.v3, got %v", entry)
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

	// Negative: Cancelled context
	cancCtx, cancel := context.WithCancel(ctx)
	cancel()
	_, err = InspectFramework(cancCtx, "")
	if err == nil {
		t.Fatal("expected error with cancelled context")
	}
}

func setupFixtureRepo(t *testing.T) string {
	t.Helper()
	tmpDir := t.TempDir()
	goModContent := "module example.com/testservice\n\ngo 1.24\n\nrequire (\n\tgithub.com/gin-gonic/gin v1.10.0\n\tgithub.com/jackc/pgx/v5 v5.7.2\n\tgithub.com/redis/rueidis v1.0.51\n\tgithub.com/unknown/gap-lib v1.0.0\n)\n"
	if err := os.WriteFile(filepath.Join(tmpDir, "go.mod"), []byte(goModContent), 0644); err != nil {
		t.Fatal(err)
	}

	mainGoContent := "package main\n\nimport (\n\t\"fmt\"\n\t\"github.com/gin-gonic/gin\"\n\t\"github.com/jackc/pgx/v5\"\n\t\"github.com/redis/rueidis\"\n\t\"github.com/unknown/gap-lib\"\n)\n\nfunc main() {\n\tfmt.Println(\"test\")\n}\n"
	if err := os.WriteFile(filepath.Join(tmpDir, "main.go"), []byte(mainGoContent), 0644); err != nil {
		t.Fatal(err)
	}
	return tmpDir
}

func TestScanRepoFixture(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	tmpDir := setupFixtureRepo(t)

	needs, err := ScanRepo(ctx, tmpDir)
	if err != nil {
		t.Fatalf("scan repo failed: %v", err)
	}
	if needs.Repository != "example.com/testservice" {
		t.Fatalf("unexpected repository: %s", needs.Repository)
	}
	if needs.Readiness.TotalThirdPartyDeps != 4 {
		t.Fatalf("expected 4 total deps, got %d", needs.Readiness.TotalThirdPartyDeps)
	}
	if needs.Readiness.CoveredDeps != 3 {
		t.Fatalf("expected 3 covered deps, got %d", needs.Readiness.CoveredDeps)
	}
	if needs.Readiness.GapDeps != 1 {
		t.Fatalf("expected 1 gap dep, got %d", needs.Readiness.GapDeps)
	}
	if needs.Readiness.Score != 75.0 {
		t.Fatalf("expected score 75.0, got %f", needs.Readiness.Score)
	}
}

func TestScanRepoManifestPersistence(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	tmpDir := setupFixtureRepo(t)
	needs, err := ScanRepo(ctx, tmpDir)
	if err != nil {
		t.Fatalf("initial scan failed: %v", err)
	}

	if err := WriteNeedsManifest(tmpDir, needs); err != nil {
		t.Fatalf("failed to write manifest: %v", err)
	}

	rescan, err := ScanRepo(ctx, tmpDir)
	if err != nil {
		t.Fatalf("rescan failed: %v", err)
	}
	if rescan.Readiness.Score != needs.Readiness.Score {
		t.Fatalf("expected score %f, got %f", needs.Readiness.Score, rescan.Readiness.Score)
	}
}

func TestScanRepoBoundaries(t *testing.T) {
	ctx := context.Background()

	// Boundary 1: Empty repo (no third-party dependencies)
	tmpEmpty := t.TempDir()
	goModEmpty := "module example.com/empty\n\ngo 1.24\n"
	if err := os.WriteFile(filepath.Join(tmpEmpty, "go.mod"), []byte(goModEmpty), 0644); err != nil {
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
	if err := os.WriteFile(filepath.Join(tmpGap, "go.mod"), []byte(goModGap), 0644); err != nil {
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

// initGitFixture turns dir into a self-contained git repository with one commit. Global
// and system git configuration are redirected into the test's own temp tree so the
// fixture never reads or writes the developer's $HOME.
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

func setupMigrationFixture(t *testing.T) string {
	t.Helper()
	tmpDir := t.TempDir()

	goMod := "module example.com/migratesvc\n\ngo 1.24\n\nrequire github.com/gin-gonic/gin v1.10.0\n"
	if err := os.WriteFile(filepath.Join(tmpDir, "go.mod"), []byte(goMod), 0o644); err != nil {
		t.Fatal(err)
	}

	src := "package main\n\nimport \"github.com/gin-gonic/gin\"\n\nfunc main() {}\n"
	if err := os.WriteFile(filepath.Join(tmpDir, "main.go"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	return tmpDir
}

func TestPlanAndApplyMigration(t *testing.T) {
	ctx := context.Background()
	tmpDir := setupMigrationFixture(t)
	initGitFixture(t, tmpDir)

	plan, err := PlanMigration(ctx, tmpDir, "")
	if err != nil {
		t.Fatalf("plan migration failed: %v", err)
	}
	if len(plan.DroppedRequires) == 0 {
		t.Fatal("expected dropped requires")
	}

	res, err := ApplyMigration(ctx, tmpDir, plan)
	if err != nil {
		t.Fatalf("apply migration failed: %v", err)
	}
	if !res.Success {
		t.Fatalf("expected successful migration, got error %q", res.Error)
	}
	if res.Branch != "refactor/golusoris-adoption" {
		t.Fatalf("unexpected branch %q", res.Branch)
	}

	content, err := os.ReadFile(filepath.Join(tmpDir, "main.go"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(content), "github.com/golusoris/golusoris/httpx") {
		t.Fatal("expected import replacement in main.go")
	}

	assertMigratedGoMod(t, filepath.Join(tmpDir, "go.mod"))

	guide, err := os.ReadFile(filepath.Join(tmpDir, "MIGRATION.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(guide), "Migration Guide") {
		t.Fatalf("unexpected MIGRATION.md content: %s", guide)
	}
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

func TestApplyMigrationNegativeNonGitTarget(t *testing.T) {
	ctx := context.Background()
	tmpDir := setupMigrationFixture(t)

	plan, err := PlanMigration(ctx, tmpDir, "")
	if err != nil {
		t.Fatalf("plan migration failed: %v", err)
	}

	res, err := ApplyMigration(ctx, tmpDir, plan)
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

func TestApplyMigrationBoundaryExistingBranch(t *testing.T) {
	ctx := context.Background()
	tmpDir := setupMigrationFixture(t)
	initGitFixture(t, tmpDir)

	if out, err := util.RunCommand(ctx, tmpDir, "git", "branch", "refactor/golusoris-adoption"); err != nil {
		t.Fatalf("failed creating pre-existing branch: %v (%s)", err, out)
	}

	plan, err := PlanMigration(ctx, tmpDir, "")
	if err != nil {
		t.Fatalf("plan migration failed: %v", err)
	}

	res, err := ApplyMigration(ctx, tmpDir, plan)
	if !errors.Is(err, ErrBranchExists) {
		t.Fatalf("expected ErrBranchExists, got %v", err)
	}
	if res == nil || res.Success {
		t.Fatal("expected Success=false when the adoption branch already exists")
	}
}

func TestAggregateFleet(t *testing.T) {
	ctx := context.Background()
	tmpRoot := t.TempDir()

	repo1 := filepath.Join(tmpRoot, "repo1")
	if err := os.MkdirAll(repo1, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo1, "go.mod"), []byte("module r1\ngo 1.24\nrequire github.com/jackc/pgx v5.0.0\n"), 0644); err != nil {
		t.Fatal(err)
	}

	repo2 := filepath.Join(tmpRoot, "repo2")
	if err := os.MkdirAll(repo2, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo2, "go.mod"), []byte("module r2\ngo 1.24\nrequire github.com/jackc/pgx v5.0.0\nrequire github.com/unknown/lib v1.0.0\n"), 0644); err != nil {
		t.Fatal(err)
	}

	report, err := AggregateFleet(ctx, tmpRoot, "")
	if err != nil {
		t.Fatalf("aggregate fleet failed: %v", err)
	}
	if report.ScannedRepositories != 2 {
		t.Fatalf("expected 2 scanned repos, got %d", report.ScannedRepositories)
	}
	if report.DemandFrequency["db.postgres"] != 2 {
		t.Fatalf("expected db.postgres count 2, got %d", report.DemandFrequency["db.postgres"])
	}

	md := RenderFrameworkDemandMarkdown(report)
	if !strings.Contains(md, "# Framework Demand & Capability Report") {
		t.Fatal("markdown missing title")
	}
}
