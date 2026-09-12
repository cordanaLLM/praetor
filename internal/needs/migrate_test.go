package needs

import (
	"bufio"
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/util"
)

func TestScanFileForReplacementsRewritesSubpackagesDeterministically(t *testing.T) {
	dir := t.TempDir()
	src := writeFixture(t, dir, "main.go", "package main\n\nimport (\n"+
		"\t\"github.com/jackc/pgx/v5\"\n\t\"github.com/jackc/pgx/v5/pgxpool\"\n)\n\n"+
		"const addr = \"redis:6379\"\n\nvar _, _ = pgx.Connect, pgxpool.New\n")

	replacements := map[string]string{
		"github.com/jackc/pgx/v5": "github.com/golusoris/golusoris/db/pgx",
		"redis":                   "github.com/golusoris/pykit/cache",
	}
	keys := sortedReplacementKeys(replacements)

	// The result must be identical on every pass: map iteration order used to decide
	// which of two overlapping keys won.
	var first []ReplacementAction
	for i := 0; i < 8; i++ {
		got := scanFileForReplacements(src, replacements, keys)
		if first == nil {
			first = got
			continue
		}
		if len(got) != len(first) {
			t.Fatalf("non-deterministic action count: %d vs %d", len(got), len(first))
		}
		for j := range got {
			if got[j] != first[j] {
				t.Fatalf("non-deterministic action %d: %+v vs %+v", j, got[j], first[j])
			}
		}
	}

	wanted := map[string]string{
		"github.com/jackc/pgx/v5":         "github.com/golusoris/golusoris/db/pgx",
		"github.com/jackc/pgx/v5/pgxpool": "github.com/golusoris/golusoris/db/pgx/pgxpool",
	}
	if len(first) != len(wanted) {
		t.Fatalf("expected %d actions, got %+v", len(wanted), first)
	}
	for _, act := range first {
		if wanted[act.OldImport] != act.NewImport {
			t.Errorf("unexpected rewrite %s -> %s", act.OldImport, act.NewImport)
		}
	}
}

func TestApplyFileImportReplacementLeavesStringLiteralsAlone(t *testing.T) {
	dir := t.TempDir()
	src := writeFixture(t, dir, "main.go",
		"package main\n\nimport cache `redis` // redis\n\nconst addr = \"redis:6379\"\nconst name = \"redis\"\n")

	if err := applyFileImportReplacement(dir, src, "redis", "github.com/golusoris/pykit/cache"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	data, err := os.ReadFile(src)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "\"redis:6379\"") {
		t.Fatalf("unrelated string literal was rewritten:\n%s", data)
	}
	if !strings.Contains(string(data), "const name = \"redis\"") {
		t.Fatalf("string value identical to the import path was rewritten:\n%s", data)
	}
	if !strings.Contains(string(data), "import cache \"github.com/golusoris/pykit/cache\" // redis") {
		t.Fatalf("import was not rewritten:\n%s", data)
	}
}

func TestMigrationRewritePrimitivesWithRealGit(t *testing.T) {
	ctx := context.Background()
	dir := setupMigrationRepo(t)
	initGitFixture(t, dir)
	plan, err := PlanMigration(ctx, dir, "")
	if err != nil {
		t.Fatal(err)
	}
	// Synthetic requirement exercises the writer; it is not verified release evidence.
	plan.AddedRequires = []string{defaultFrameworkModule + " v0.8.0"}
	result, err := rewriteMigrationFixture(ctx, dir, plan, MigrationOptions{SkipTidy: true})
	if err != nil || result == nil || !result.Success {
		t.Fatalf("expected a successful migration: result=%+v err=%v", result, err)
	}
	branch, err := util.RunCommand(ctx, dir, "git", "branch", "--show-current")
	if err != nil || strings.TrimSpace(branch) != migrationBranch {
		t.Fatalf("expected new migration branch, got %q: %v", branch, err)
	}
	assertMigratedGoMod(t, filepath.Join(dir, "go.mod"))
	if len(result.FilesChanged) != 3 {
		t.Fatalf("expected source, go.mod and guide mutations, got %v", result.FilesChanged)
	}
	for _, name := range []string{"main.go", "go.mod", "MIGRATION.md"} {
		path := filepath.Join(dir, name)
		info, err := os.Stat(path)
		if err != nil || info.Mode().Perm() != 0o600 {
			t.Fatalf("expected private migration file %q: info=%v err=%v", name, info, err)
		}
	}
}

func TestMigrationRewriteTidyFailureRetainsPartialResult(t *testing.T) {
	ctx := context.Background()
	dir := setupMigrationRepo(t)
	plan, err := PlanMigration(ctx, dir, "")
	if err != nil {
		t.Fatal(err)
	}
	// Synthetic requirement exercises the writer; it is not verified release evidence.
	plan.AddedRequires = []string{defaultFrameworkModule + " v0.8.0"}
	tidyErr := errors.New("tidy failed")
	runner := &recordingRunner{err: tidyErr, failAt: 4}
	result, err := rewriteMigrationFixture(ctx, dir, plan, MigrationOptions{Runner: runner.run})
	if !errors.Is(err, tidyErr) || result == nil || result.Success || result.Error == "" || len(result.Warnings) != 1 {
		t.Fatalf("expected a hard failure with diagnostics: result=%+v err=%v", result, err)
	}
	if len(runner.calls) != 4 || strings.Join(runner.calls[3], " ") != "go mod tidy" {
		t.Fatalf("expected tidy to be the failing mutation, got %v", runner.calls)
	}
	if len(result.FilesChanged) != 2 {
		t.Fatalf("expected source and go.mod in partial result, got %v", result.FilesChanged)
	}
	for _, name := range []string{"main.go", "go.mod"} {
		path := filepath.Join(dir, name)
		if !slices.Contains(result.FilesChanged, path) {
			t.Errorf("partial result omitted %s", path)
		}
		info, err := os.Stat(path)
		if err != nil || info.Mode().Perm() != 0o600 {
			t.Errorf("expected private permissions preserved for %s: info=%v err=%v", path, info, err)
		}
	}
	assertMigratedGoMod(t, filepath.Join(dir, "go.mod"))
	if _, err := os.Stat(filepath.Join(dir, "MIGRATION.md")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("guide must not be written after a hard failure, got %v", err)
	}
}

func TestUpdateGoModPreservesOtherDirectiveBlocks(t *testing.T) {
	dir := t.TempDir()
	module := "github.com/gin-gonic/gin"
	other := "replace (\n\t" + module + " => ../local-gin\n)\n\nexclude (\n\t" + module + " v1.9.0\n)\n"
	path := writeFixture(t, dir, "go.mod", "module example.com/app\n\ngo 1.24\n\nrequire (\n\t"+module+" v1.10.0\n)\n\n"+other)
	for i := 0; i < 2; i++ {
		if err := updateGoMod(path, nil, []string{module}); err != nil {
			t.Fatal(err)
		}
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "v1.10.0") || !strings.Contains(string(data), other) {
		t.Fatalf("migration must drop only the require entry and retain other directives:\n%s", data)
	}
}

func TestPlanMigrationDropsOnlyGoEcosystemDependencies(t *testing.T) {
	dir := t.TempDir()
	writeFixture(t, dir, "go.mod",
		"module github.com/acme/redis-proxy\n\ngo 1.24\n\nrequire github.com/gin-gonic/gin v1.10.0\n")
	writeFixture(t, dir, "requirements.txt", "redis==5.0.1\nclick==8.1.7\n")

	plan, err := PlanMigration(context.Background(), dir, "")
	if err != nil {
		t.Fatalf("plan migration failed: %v", err)
	}
	for _, dropped := range plan.DroppedRequires {
		if !strings.Contains(dropped, "/") {
			t.Fatalf("non-Go dependency %q must never become a go.mod drop key", dropped)
		}
	}
	if len(plan.DroppedRequires) != 1 || plan.DroppedRequires[0] != "github.com/gin-gonic/gin" {
		t.Fatalf("unexpected drop set %v", plan.DroppedRequires)
	}
}

func TestUpdateGoModKeepsDirectivesAndIsIdempotent(t *testing.T) {
	dir := t.TempDir()
	path := writeFixture(t, dir, "go.mod", "module github.com/acme/redis-proxy\n\ngo 1.24\n\n"+
		"require (\n\tgithub.com/gin-gonic/gin v1.10.0\n\tgithub.com/redis/rueidis v1.0.51\n)\n")

	added := []string{"github.com/golusoris/golusoris v0.8.0"}
	dropped := []string{"github.com/gin-gonic/gin", "redis"}
	for i := 0; i < 2; i++ {
		if err := updateGoMod(path, added, dropped); err != nil {
			t.Fatalf("pass %d failed: %v", i, err)
		}
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	body := string(data)
	if !strings.Contains(body, "module github.com/acme/redis-proxy") {
		t.Fatalf("module directive was dropped by a substring match:\n%s", body)
	}
	if !strings.Contains(body, "github.com/redis/rueidis") {
		t.Fatalf("unrelated require matching the bare name \"redis\" was dropped:\n%s", body)
	}
	if strings.Contains(body, "github.com/gin-gonic/gin") {
		t.Fatalf("gin require should have been dropped:\n%s", body)
	}
	if strings.Count(body, "github.com/golusoris/golusoris v0.8.0") != 1 {
		t.Fatalf("re-applying the migration duplicated the framework require:\n%s", body)
	}
}

func TestUpdateGoModNegativeScanErrorLeavesFileIntact(t *testing.T) {
	dir := t.TempDir()
	// A line longer than bufio.Scanner's 64 KiB buffer makes Scan() stop exactly like EOF.
	overlong := "// " + strings.Repeat("x", bufio.MaxScanTokenSize+16)
	original := "module example.com/big\n\ngo 1.24\n\n" + overlong + "\nrequire github.com/a/b v1.0.0\n"
	path := writeFixture(t, dir, "go.mod", original)

	err := updateGoMod(path, []string{"github.com/golusoris/golusoris v0.8.0"}, nil)
	if err == nil {
		t.Fatal("expected the truncated scan to be reported as an error")
	}
	if !errors.Is(err, bufio.ErrTooLong) {
		t.Fatalf("expected bufio.ErrTooLong, got %v", err)
	}

	data, readErr := os.ReadFile(path)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if string(data) != original {
		t.Fatal("go.mod must not be rewritten from a truncated scan")
	}
}

func TestFindFileImportReplacementsSkipsSymlinkedSources(t *testing.T) {
	outside := t.TempDir()
	target := writeFixture(t, outside, "main.go",
		"package main\n\nimport \"github.com/gin-gonic/gin\"\n\nvar _ = gin.Default\n")

	repo := t.TempDir()
	link := filepath.Join(repo, "shim.go")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlinks unavailable on this platform: %v", err)
	}

	replacements := map[string]string{"github.com/gin-gonic/gin": "github.com/golusoris/golusoris/httpx"}
	actions, err := findFileImportReplacements(context.Background(), repo, replacements)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(actions) != 0 {
		t.Fatalf("symlinked source outside the repository must not be planned: %+v", actions)
	}

	data, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "github.com/gin-gonic/gin") {
		t.Fatal("file outside the repository was modified through a symlink")
	}
}

func TestConfineToRepoNegativeEscapingTarget(t *testing.T) {
	repo := t.TempDir()
	outside := filepath.Join(t.TempDir(), "main.go")
	if _, err := confineToRepo(repo, outside); err == nil {
		t.Fatal("expected a confinement error for a file outside the repository")
	}
	inside := writeFixture(t, repo, "main.go", "package main\n")
	if _, err := confineToRepo(repo, inside); err != nil {
		t.Fatalf("expected an in-repository file to be accepted, got %v", err)
	}
}

// rewriteMigrationFixture exercises existing file/branch primitives directly.
// It intentionally is test-only: candidate plans are not admitted by public apply.
func rewriteMigrationFixture(ctx context.Context, repoPath string, plan *MigrationPlan, opts MigrationOptions) (*MigrationResult, error) {
	run := opts.Runner
	if run == nil {
		run = util.RunCommand
	}
	result := &MigrationResult{Repository: plan.Repository, Branch: migrationBranch}
	if err := createAdoptionBranch(ctx, repoPath, migrationBranch, run); err != nil {
		return failedMigration(result, err)
	}
	changed, err := applyPlannedRewrites(ctx, repoPath, plan, run, opts.SkipTidy)
	result.FilesChanged = changed
	if err != nil {
		return failedMigration(result, err)
	}
	result.Success = true
	return result, nil
}
