package needs

import (
	"context"
	"errors"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// malformedPackageJSON is a template package.json no JSON parser accepts, as an examples/
// or fixtures/ directory ships.
const malformedPackageJSON = `{"name":"tmpl","dependencies":{{deps}}}`

// failedDirs returns the directories of a row's failed sub-projects.
func failedDirs(failures []SubprojectFailure) []string {
	dirs := make([]string, 0, len(failures))
	for _, failure := range failures {
		dirs = append(dirs, failure.Dir)
	}
	return dirs
}

// TestFailedSubprojectKeepsRepositoryRow: one malformed nested manifest is listed on the
// row, in the aggregate report, in scan output and as an epic blocker, while the root
// project and every other sub-project are still scored. Without the fix the whole row
// failed: AggregateFleet reported no repository scanned and ScanRepo errored.
func TestFailedSubprojectKeepsRepositoryRow(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	repo := filepath.Join(root, "app")
	makeCheckout(t, repo)
	writeGoProject(t, repo, "example.com/app", pgxModule)
	writeRepoFile(t, filepath.Join(repo, "core", "meson.build"), "dep = dependency('zlib')\n")
	writeRepoFile(t, filepath.Join(repo, "examples", "tmpl", "package.json"), malformedPackageJSON)

	report, rows := aggregateRows(t, root)
	row := rows[repo]
	if report.ScannedRepositories != 1 || report.FailedRepositories != 0 {
		t.Fatalf("scanned=%d failed=%d, want the row scored despite the malformed sub-project",
			report.ScannedRepositories, report.FailedRepositories)
	}
	if !slices.Equal(failedDirs(row.FailedSubprojects), []string{"examples/tmpl"}) || row.FailedSubprojects[0].Error == "" {
		t.Errorf("row failed sub-projects = %+v, want examples/tmpl with its error", row.FailedSubprojects)
	}
	if !slices.Equal(row.Subprojects, []string{"core"}) || !hasDependency(row, pgxModule) || !hasDependency(row, "zlib") {
		t.Errorf("row = subprojects %v deps %v, want core scanned and root plus core demand kept", row.Subprojects, row.Dependencies)
	}
	if !slices.Equal(failedDirs(report.FailedSubprojects), []string{filepath.Join(repo, "examples", "tmpl")}) {
		t.Errorf("report failed sub-projects = %+v", report.FailedSubprojects)
	}
	md := RenderFrameworkDemandMarkdown(report)
	if !strings.Contains(md, "Sub-projects Failed") || !strings.Contains(md, "`app/examples/tmpl`") {
		t.Errorf("markdown does not list the failed sub-project:\n%s", md)
	}
	if text := FormatLibraryRelationships(&row); !strings.Contains(text, "FAILED") || !strings.Contains(text, "examples/tmpl") {
		t.Errorf("scan output does not report the failed sub-project:\n%s", text)
	}

	single, err := ScanRepo(ctx, repo)
	if err != nil || !slices.Equal(failedDirs(single.FailedSubprojects), []string{"examples/tmpl"}) {
		t.Fatalf("ScanRepo = %+v, %v; want the row with examples/tmpl failed", single, err)
	}
	epic, err := GeneratePreMigrationEpic(ctx, repo, "")
	if err != nil {
		t.Fatalf("GeneratePreMigrationEpic() error = %v", err)
	}
	if !slices.ContainsFunc(epic.Blockers, func(b string) bool { return strings.Contains(b, "examples/tmpl") }) {
		t.Errorf("epic blockers %v do not name the failed sub-project", epic.Blockers)
	}
	epics, _, err := RegenerateFleetEpics(ctx, root, FleetEpicOptions{DryRun: true})
	if err != nil || len(epics) != 1 {
		t.Errorf("fleet epics = %d, err = %v; want the repository's epic", len(epics), err)
	}
}

// TestFailedRootProjectFailsRepository: a malformed manifest at the repository root still
// fails the row, as before sub-projects were folded in, and the aggregate counts it failed.
func TestFailedRootProjectFailsRepository(t *testing.T) {
	root := t.TempDir()
	repo := filepath.Join(root, "app")
	makeCheckout(t, repo)
	writeRepoFile(t, filepath.Join(repo, "package.json"), malformedPackageJSON)
	writeRepoFile(t, filepath.Join(repo, "core", "meson.build"), "dep = dependency('zlib')\n")

	if _, err := ScanRepo(context.Background(), repo); err == nil {
		t.Fatal("ScanRepo() succeeded on a malformed root manifest")
	}
	report, err := AggregateFleet(context.Background(), root, "")
	if !errors.Is(err, ErrNoRepositoryScanned) || report.FailedRepositories != 1 {
		t.Fatalf("AggregateFleet() failed=%d err=%v, want the root failure counted", report.FailedRepositories, err)
	}
}

// TestFailedSubprojectsUnderNonProjectRoot: with no project at the root, the row survives
// while one sub-project scans (boundary: exactly one), named after the root; when none
// scans the repository fails, and not as an ErrNoAnalyzer skip.
func TestFailedSubprojectsUnderNonProjectRoot(t *testing.T) {
	repo := filepath.Join(t.TempDir(), "vmafx")
	makeCheckout(t, repo)
	writeRepoFile(t, filepath.Join(repo, "a", "package.json"), malformedPackageJSON)
	writeRepoFile(t, filepath.Join(repo, "b", "meson.build"), "dep = dependency('zlib')\n")

	row, err := ScanRepo(context.Background(), repo)
	if err != nil {
		t.Fatalf("ScanRepo() error = %v", err)
	}
	if row.Repository != "vmafx" || !slices.Equal(row.Subprojects, []string{"b"}) ||
		!slices.Equal(failedDirs(row.FailedSubprojects), []string{"a"}) {
		t.Errorf("row = %s subprojects %v failed %+v", row.Repository, row.Subprojects, row.FailedSubprojects)
	}

	writeRepoFile(t, filepath.Join(repo, "b", "package.json"), malformedPackageJSON)
	_, err = ScanRepo(context.Background(), repo)
	if err == nil || errors.Is(err, ErrNoAnalyzer) || !strings.Contains(err.Error(), "a") {
		t.Fatalf("ScanRepo() error = %v, want a failure naming the sub-projects, not a skip", err)
	}
}

// TestFailedSubprojectCancellationIsNotRecorded: a cancelled context stops the scan with
// the context error instead of being recorded as failed sub-projects.
func TestFailedSubprojectCancellationIsNotRecorded(t *testing.T) {
	repo := t.TempDir()
	writeRepoFile(t, filepath.Join(repo, "a", "meson.build"), "dep = dependency('zlib')\n")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	fleet := &fleetRepo{root: repo, subprojects: []string{filepath.Join(repo, "a")}}
	if _, err := scanRepository(ctx, fleet); !errors.Is(err, context.Canceled) {
		t.Fatalf("scanRepository() error = %v, want context.Canceled", err)
	}
}

// makeWorktreeSubmodule checks out the submodule rel of superDir inside the linked worktree
// wtName the way git does: its git directory sits under <super>/.git/worktrees/<wt>/modules,
// which has no commondir file.
func makeWorktreeSubmodule(t *testing.T, superDir, worktree, wtName, rel string) string {
	t.Helper()
	dir := filepath.Join(worktree, rel)
	moduleDir := filepath.Join(superDir, ".git", "worktrees", wtName, "modules", filepath.FromSlash(rel))
	writeRepoFile(t, filepath.Join(moduleDir, "HEAD"), "ref: refs/heads/main\n")
	target, err := filepath.Rel(dir, moduleDir)
	if err != nil {
		t.Fatal(err)
	}
	writeRepoFile(t, filepath.Join(dir, ".git"), "gitdir: "+filepath.ToSlash(target)+"\n")
	return dir
}

// TestDiscoverFleetCollapsesSubmodulesOfLinkedWorktrees: a submodule checked out in a
// linked worktree is the same repository as the main checkout's submodule and collapses
// onto it, even when the worktree sorts first; a submodule only a worktree checked out
// stays one row.
func TestDiscoverFleetCollapsesSubmodulesOfLinkedWorktrees(t *testing.T) {
	root := t.TempDir()
	app := filepath.Join(root, "b-app")
	makeCheckout(t, app)
	writeGoProject(t, app, "example.com/app", pgxModule)
	mainLib := makeSubmodule(t, app, "lib")
	writeGoProject(t, mainLib, "example.com/lib", redisModule)
	wt := filepath.Join(root, "a-wt")
	makeLinkedWorktree(t, app, wt, "a-wt")
	writeGoProject(t, wt, "example.com/app", pgxModule)
	wtLib := makeWorktreeSubmodule(t, app, wt, "a-wt", "lib")
	writeGoProject(t, wtLib, "example.com/lib", redisModule)
	wtOnly := makeWorktreeSubmodule(t, app, wt, "a-wt", "extra")
	writeRepoFile(t, filepath.Join(wtOnly, "meson.build"), "dep = dependency('zlib')\n")

	layout, err := discoverFleet(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	roots := make([]string, 0, len(layout.repos))
	for _, repo := range layout.repos {
		roots = append(roots, repo.root)
	}
	want := []string{wtOnly, app, mainLib}
	if !slices.Equal(roots, want) {
		t.Fatalf("repos = %v, want %v", roots, want)
	}
	wantDups := []FleetDuplicate{{Dir: wt, Of: app}, {Dir: wtLib, Of: mainLib}}
	if !slices.Equal(layout.duplicates, wantDups) {
		t.Errorf("duplicates = %+v, want %+v", layout.duplicates, wantDups)
	}
}
