package topology

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/cordanaLLM/praetor/internal/testsupport"
)

// repositoryOf resolves path's checkout repository or fails the test.
func repositoryOf(t *testing.T, path string) CheckoutRepository {
	t.Helper()
	repo, err := ResolveCheckoutRepository(context.Background(), path)
	if err != nil {
		t.Fatalf("ResolveCheckoutRepository(%s) error = %v", path, err)
	}
	return repo
}

// writeLinkedWorktree lays out a linked worktree of mainDir the way git does.
func writeLinkedWorktree(t *testing.T, mainGitDir, worktree, name string) string {
	t.Helper()
	gitDir := filepath.Join(mainGitDir, "worktrees", name)
	writeCommonDirFixture(t, filepath.Join(gitDir, "HEAD"), "ref: refs/heads/"+name+"\n")
	writeCommonDirFixture(t, filepath.Join(gitDir, "commondir"), "../..\n")
	writeCommonDirFixture(t, filepath.Join(worktree, ".git"), "gitdir: "+gitDir+"\n")
	return gitDir
}

// writeModuleCheckout lays out a checkout at dir whose gitlink names moduleDir, which has
// no commondir file: a submodule, or a clone with a separate git directory.
func writeModuleCheckout(t *testing.T, dir, moduleDir string) {
	t.Helper()
	writeCommonDirFixture(t, filepath.Join(moduleDir, "HEAD"), "ref: refs/heads/main\n")
	writeCommonDirFixture(t, filepath.Join(dir, ".git"), "gitdir: "+moduleDir+"\n")
}

// TestResolveCheckoutRepositoryFoldsWorktreeSubmodules: the copy of a submodule a linked
// worktree checks out shares the main checkout's submodule key, and only the main
// checkout and the main checkout's submodule are primary. Without the fold the worktree's
// copy had a key of its own, and fleet discovery counted it as another repository.
func TestResolveCheckoutRepositoryFoldsWorktreeSubmodules(t *testing.T) {
	root := t.TempDir()
	mainDir := filepath.Join(root, "app")
	initTestGit(t, mainDir)
	mainGit := filepath.Join(mainDir, ".git")
	mainLib := filepath.Join(mainDir, "libs", "lib")
	writeModuleCheckout(t, mainLib, filepath.Join(mainGit, "modules", "libs", "lib"))
	wt := filepath.Join(root, "app-wt")
	wtGit := writeLinkedWorktree(t, mainGit, wt, "app-wt")
	wtLib := filepath.Join(wt, "libs", "lib")
	writeModuleCheckout(t, wtLib, filepath.Join(wtGit, "modules", "libs", "lib"))
	clone := filepath.Join(root, "clone")
	initTestGit(t, clone)

	app, worktree := repositoryOf(t, mainDir), repositoryOf(t, wt)
	lib, copyInWorktree := repositoryOf(t, mainLib), repositoryOf(t, wtLib)
	if app.Key != worktree.Key || lib.Key != copyInWorktree.Key {
		t.Errorf("keys app=%s wt=%s lib=%s wt-lib=%s, want worktree and submodule copies grouped",
			app.Key, worktree.Key, lib.Key, copyInWorktree.Key)
	}
	if lib.Key == app.Key || repositoryOf(t, clone).Key == app.Key {
		t.Error("a submodule or an independent clone shares the superproject's key")
	}
	if !app.Primary || worktree.Primary || !lib.Primary || copyInWorktree.Primary {
		t.Errorf("primary app=%v wt=%v lib=%v wt-lib=%v, want only the main checkout and its submodule",
			app.Primary, worktree.Primary, lib.Primary, copyInWorktree.Primary)
	}
}

// TestResolveCheckoutRepositoryNeverFoldsLookalikes: a worktrees/<name>/modules path is
// folded only when <name> is a linked worktree of the directory above it. A main checkout
// under such a path and a module under a worktrees/<name> without commondir keep their
// own keys; a directory without HEAD metadata is no checkout.
func TestResolveCheckoutRepositoryNeverFoldsLookalikes(t *testing.T) {
	root := t.TempDir()
	lookalike := filepath.Join(root, "worktrees", "x", "modules", "lib")
	initTestGit(t, lookalike)
	if got := repositoryOf(t, lookalike); got.Key != commonDirOf(t, lookalike) || !got.Primary {
		t.Errorf("main checkout under a worktrees/x/modules path = %+v, want its own primary key", got)
	}

	mainDir := filepath.Join(root, "app")
	initTestGit(t, mainDir)
	orphan := filepath.Join(root, "orphan")
	orphanModule := filepath.Join(mainDir, ".git", "worktrees", "gone", "modules", "lib")
	writeCommonDirFixture(t, filepath.Join(mainDir, ".git", "worktrees", "gone", "HEAD"), "ref: refs/heads/gone\n")
	writeModuleCheckout(t, orphan, orphanModule)
	if got := repositoryOf(t, orphan); got.Key != commonDirOf(t, orphan) {
		t.Errorf("module under a worktrees dir without commondir folded to %s", got.Key)
	}

	stray := filepath.Join(root, "stray")
	if err := os.MkdirAll(filepath.Join(stray, ".git"), 0o750); err != nil {
		t.Fatal(err)
	}
	if _, err := ResolveCheckoutRepository(context.Background(), stray); !errors.Is(err, ErrNotCheckout) {
		t.Errorf("ResolveCheckoutRepository(stray) error = %v, want ErrNotCheckout", err)
	}
}

// TestResolveCheckoutRepositoryBoundaries: a submodule of a submodule inside a linked
// worktree of that submodule folds at both levels; a clone with a separate git directory
// is primary; a worktree git directory whose commondir names a missing directory is an
// error, never a silent fallback; a cancelled context stops resolution.
func TestResolveCheckoutRepositoryBoundaries(t *testing.T) {
	root := t.TempDir()
	mainDir := filepath.Join(root, "app")
	initTestGit(t, mainDir)
	mainGit := filepath.Join(mainDir, ".git")
	wtGit := writeLinkedWorktree(t, mainGit, filepath.Join(root, "wt"), "wt")
	subInWT := filepath.Join(wtGit, "modules", "a")
	writeModuleCheckout(t, filepath.Join(root, "wt", "a"), subInWT)
	subWTGit := writeLinkedWorktree(t, subInWT, filepath.Join(root, "a-wt2"), "wt2")
	nested := filepath.Join(root, "a-wt2", "b")
	writeModuleCheckout(t, nested, filepath.Join(subWTGit, "modules", "b"))
	wantKey := filepath.Join(commonDirOf(t, mainDir), "modules", "a", "modules", "b")
	if got := repositoryOf(t, nested); got.Key != wantKey || got.Primary {
		t.Errorf("doubly nested submodule = %+v, want key %s, not primary", got, wantKey)
	}

	separate := filepath.Join(root, "separate")
	writeModuleCheckout(t, separate, filepath.Join(root, "store", "separate.git"))
	if !repositoryOf(t, separate).Primary {
		t.Error("a clone with a separate git directory must be primary")
	}

	broken := filepath.Join(root, "broken")
	brokenGit := filepath.Join(mainGit, "worktrees", "broken")
	writeCommonDirFixture(t, filepath.Join(brokenGit, "HEAD"), "ref: refs/heads/broken\n")
	writeCommonDirFixture(t, filepath.Join(brokenGit, "commondir"), "../../../../absent\n")
	writeModuleCheckout(t, broken, filepath.Join(brokenGit, "modules", "lib"))
	if _, err := ResolveCheckoutRepository(context.Background(), broken); err == nil {
		t.Error("a worktree commondir naming a missing directory must be an error")
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := ResolveCheckoutRepository(ctx, mainDir); !errors.Is(err, context.Canceled) {
		t.Errorf("cancelled ResolveCheckoutRepository error = %v", err)
	}
}

// TestResolveCheckoutRepositoryMatchesRealGit replays the layout real git writes: a
// submodule added in the main checkout and initialised again inside a linked worktree.
func TestResolveCheckoutRepositoryMatchesRealGit(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skipf("git unavailable: %v", err)
	}
	root := t.TempDir()
	env := testsupport.HermeticGitEnv(t)
	git := func(dir string, args ...string) {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, "git", append([]string{"-c", "protocol.file.allow=always", "-c", "init.defaultBranch=main"}, args...)...)
		cmd.Dir, cmd.Env = dir, env
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	for _, name := range []string{"lib", "app"} {
		dir := filepath.Join(root, name)
		git(root, "init", "-q", dir)
		writeCommonDirFixture(t, filepath.Join(dir, "README"), name+"\n")
		git(dir, "add", "README")
		git(dir, "commit", "-q", "-m", "init")
	}
	app := filepath.Join(root, "app")
	git(app, "submodule", "add", "-q", filepath.Join(root, "lib"), "libs/lib")
	git(app, "commit", "-q", "-m", "add lib")
	wt := filepath.Join(root, "app-wt")
	git(app, "worktree", "add", "-q", "-b", "wt", wt)
	git(wt, "submodule", "update", "--init", "-q")

	lib, copyInWorktree := repositoryOf(t, filepath.Join(app, "libs", "lib")), repositoryOf(t, filepath.Join(wt, "libs", "lib"))
	if lib.Key != copyInWorktree.Key || !lib.Primary || copyInWorktree.Primary {
		t.Errorf("real git: lib=%+v worktree copy=%+v, want one key with the main copy primary", lib, copyInWorktree)
	}
}
