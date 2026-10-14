package needs

import (
	"context"
	"path/filepath"
	"slices"
	"testing"
)

// Fixtures for #1234: a repository scan reads the repository's own tree, never a nested
// worktree or a path its ignore rules hide. identityCheckout builds a real git checkout whose
// root requirements.txt names fastapi.

// committedCheckout is identityCheckout with .gitignore holding rules and everything committed,
// so that a linked worktree can be added from it.
func committedCheckout(t *testing.T, dir, rules string) func(string, ...string) {
	t.Helper()
	run := identityCheckout(t, dir, "", "")
	if rules != "" {
		writeRepoFile(t, filepath.Join(dir, ".gitignore"), rules)
	}
	run(dir, "add", "-A")
	run(dir, "commit", "-q", "-m", "init")
	return run
}

// writeLeftoverWorktree writes a directory whose .git file names an administrative directory
// that no longer exists, as a worktree left behind after its main checkout pruned it, with a
// requirements.txt naming pkg.
func writeLeftoverWorktree(t *testing.T, dir, pkg string) {
	t.Helper()
	gone := filepath.Join(t.TempDir(), "gone", ".git", "worktrees", filepath.Base(dir))
	writeRepoFile(t, filepath.Join(dir, ".git"), "gitdir: "+filepath.ToSlash(gone)+"\n")
	writeRepoFile(t, filepath.Join(dir, "requirements.txt"), "fastapi>=0.111\n"+pkg+"==1.0\n")
}

// dependencyNames returns the package names of a row's demand, sorted.
func dependencyNames(row *RepoNeeds) []string {
	names := make([]string, 0, len(row.Dependencies))
	for _, dep := range row.Dependencies {
		names = append(names, dep.Package)
	}
	slices.Sort(names)
	return names
}

func mustScanRepo(t *testing.T, repo string) *RepoNeeds {
	t.Helper()
	row, err := ScanRepo(context.Background(), repo, nil)
	if err != nil {
		t.Fatalf("ScanRepo(%s) error = %v", repo, err)
	}
	return row
}

// TestScanRepoIgnoredNestedWorktreesLeaveScanUnchanged is the issue's fixture: a live linked
// worktree, a leftover worktree and a plain copy, all under an ignored worktrees/, each adding
// a dependency, leave the scan exactly as it was before they appeared. Before the fix the
// leftover worktree (flask) and the copy (django) were scanned as sub-projects.
func TestScanRepoIgnoredNestedWorktreesLeaveScanUnchanged(t *testing.T) {
	repo := filepath.Join(t.TempDir(), "app")
	run := committedCheckout(t, repo, "worktrees/\n")
	before := mustScanRepo(t, repo)

	run(repo, "worktree", "add", "-q", "worktrees/live", "-b", "live")
	writeRepoFile(t, filepath.Join(repo, "worktrees", "live", "requirements.txt"), "fastapi>=0.111\nnumpy==1.26.0\n")
	writeLeftoverWorktree(t, filepath.Join(repo, "worktrees", "stale"), "flask")
	writeRepoFile(t, filepath.Join(repo, "worktrees", "copy", "requirements.txt"), "django==5.0\n")
	after := mustScanRepo(t, repo)

	if got, want := dependencyNames(after), dependencyNames(before); !slices.Equal(got, want) || !slices.Equal(want, []string{"fastapi"}) {
		t.Errorf("dependencies with nested worktrees = %v, without = %v; want both [fastapi]", got, want)
	}
	if len(after.Subprojects) != 0 || len(after.FailedSubprojects) != 0 || len(after.UnscannedSubprojects) != 0 {
		t.Errorf("nested worktrees reached the row: subprojects %v failed %v unscanned %v",
			after.Subprojects, after.FailedSubprojects, after.UnscannedSubprojects)
	}
}

// TestScanRepoSkipsLeftoverWorktreeNoRuleIgnores: negative case for the .git entry alone. A
// leftover worktree no ignore rule hides is still another checkout's tree and is not scanned.
func TestScanRepoSkipsLeftoverWorktreeNoRuleIgnores(t *testing.T) {
	repo := filepath.Join(t.TempDir(), "app")
	committedCheckout(t, repo, "")
	writeLeftoverWorktree(t, filepath.Join(repo, "leftover"), "httpx")

	row := mustScanRepo(t, repo)
	if hasDependency(*row, "httpx") || len(row.Subprojects) != 0 {
		t.Errorf("leftover worktree scanned: deps %v subprojects %v", dependencyNames(row), row.Subprojects)
	}
}

// TestScanRepoSkipsIgnoredDirectoryWithoutGitEntry: negative case for the ignore rules alone.
// An ignored directory holding a manifest but no .git entry is not the repository's source.
func TestScanRepoSkipsIgnoredDirectoryWithoutGitEntry(t *testing.T) {
	repo := filepath.Join(t.TempDir(), "app")
	committedCheckout(t, repo, "/out/\n")
	writeRepoFile(t, filepath.Join(repo, "out", "bundle", "requirements.txt"), "django==5.0\n")

	row := mustScanRepo(t, repo)
	if hasDependency(*row, "django") || len(row.Subprojects) != 0 {
		t.Errorf("ignored directory scanned: deps %v subprojects %v", dependencyNames(row), row.Subprojects)
	}
}

// TestScanRepoKeepsTrackedAndUnignoredSubprojects: boundary. A tracked sub-project, an
// untracked one no rule matches although its name extends an ignored one, and a directory of
// the ignored name deeper than the anchored rule reaches are all still scanned.
func TestScanRepoKeepsTrackedAndUnignoredSubprojects(t *testing.T) {
	repo := filepath.Join(t.TempDir(), "app")
	writeRepoFile(t, filepath.Join(repo, "services", "api", "requirements.txt"), "uvicorn==0.30\n")
	committedCheckout(t, repo, "/worktrees/\n")
	writeRepoFile(t, filepath.Join(repo, "worktrees-notes", "requirements.txt"), "httpx==0.27\n")
	writeRepoFile(t, filepath.Join(repo, "src", "worktrees", "requirements.txt"), "pydantic==2.8\n")
	writeRepoFile(t, filepath.Join(repo, "worktrees", "requirements.txt"), "django==5.0\n")

	row := mustScanRepo(t, repo)
	if got, want := dependencyNames(row), []string{"fastapi", "httpx", "pydantic", "uvicorn"}; !slices.Equal(got, want) {
		t.Errorf("dependencies = %v, want %v", got, want)
	}
	if want := []string{"services/api", "src/worktrees", "worktrees-notes"}; !slices.Equal(row.Subprojects, want) {
		t.Errorf("subprojects = %v, want %v", row.Subprojects, want)
	}
}

// TestScanRepoWithoutGitStopsAtNestedCheckoutsOnly: boundary. A root git does not answer for
// has no ignore rules, so its ignored/ directory is scanned, yet the leftover worktree below
// it is still skipped by its .git entry.
func TestScanRepoWithoutGitStopsAtNestedCheckoutsOnly(t *testing.T) {
	root := filepath.Join(t.TempDir(), "plain")
	writeRepoFile(t, filepath.Join(root, "requirements.txt"), "fastapi>=0.111\n")
	writeRepoFile(t, filepath.Join(root, ".gitignore"), "ignored/\n")
	writeRepoFile(t, filepath.Join(root, "ignored", "requirements.txt"), "django==5.0\n")
	writeLeftoverWorktree(t, filepath.Join(root, "leftover"), "flask")

	ignored, err := repositoryIgnoredPaths(context.Background(), root)
	if err != nil || ignored != nil {
		t.Fatalf("repositoryIgnoredPaths(no work tree) = %v, %v; want none, no error", ignored, err)
	}
	row := mustScanRepo(t, root)
	if got, want := dependencyNames(row), []string{"django", "fastapi"}; !slices.Equal(got, want) {
		t.Errorf("dependencies = %v, want %v", got, want)
	}
}

// TestRepositoryIgnoredPathsCancelledIsError: negative. A listing git never gave is an error,
// not "nothing ignored", so a cancelled scan cannot count ignored trees.
func TestRepositoryIgnoredPathsCancelledIsError(t *testing.T) {
	repo := filepath.Join(t.TempDir(), "app")
	committedCheckout(t, repo, "worktrees/\n")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if ignored, err := repositoryIgnoredPaths(ctx, repo); err == nil {
		t.Fatalf("cancelled listing answered %v without error", ignored)
	}
	if _, err := ScanRepo(ctx, repo, nil); err == nil {
		t.Fatal("cancelled scan returned a row")
	}
}
