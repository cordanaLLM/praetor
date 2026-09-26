package topology

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func writeCommonDirFixture(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

// commonDirOf resolves path's common directory or fails the test.
func commonDirOf(t *testing.T, path string) string {
	t.Helper()
	common, err := GitCommonDir(context.Background(), path)
	if err != nil {
		t.Fatalf("GitCommonDir(%s) error = %v", path, err)
	}
	return common
}

// TestGitCommonDirGroupsWorktreesAndSeparatesSubmodules: a main checkout and its linked
// worktrees (relative and absolute commondir) share one common directory; a submodule
// and an independent clone each have their own.
func TestGitCommonDirGroupsWorktreesAndSeparatesSubmodules(t *testing.T) {
	root := t.TempDir()
	mainDir := filepath.Join(root, "main")
	initTestGit(t, mainDir)
	relWT := filepath.Join(root, "wt-rel")
	relGitDir := filepath.Join(mainDir, ".git", "worktrees", "rel")
	writeCommonDirFixture(t, filepath.Join(relGitDir, "HEAD"), "ref: refs/heads/rel\n")
	writeCommonDirFixture(t, filepath.Join(relGitDir, "commondir"), "../..\n")
	writeCommonDirFixture(t, filepath.Join(relWT, ".git"), "gitdir: "+relGitDir+"\n")
	absWT := filepath.Join(root, "wt-abs")
	absGitDir := filepath.Join(mainDir, ".git", "worktrees", "abs")
	writeCommonDirFixture(t, filepath.Join(absGitDir, "HEAD"), "ref: refs/heads/abs\n")
	writeCommonDirFixture(t, filepath.Join(absGitDir, "commondir"), filepath.Join(mainDir, ".git")+"\n")
	writeCommonDirFixture(t, filepath.Join(absWT, ".git"), "gitdir: "+absGitDir+"\n")
	sub := filepath.Join(mainDir, "sub")
	writeCommonDirFixture(t, filepath.Join(mainDir, ".git", "modules", "sub", "HEAD"), "ref: refs/heads/main\n")
	writeCommonDirFixture(t, filepath.Join(sub, ".git"), "gitdir: ../.git/modules/sub\n")
	clone := filepath.Join(root, "clone")
	initTestGit(t, clone)

	shared := commonDirOf(t, mainDir)
	for _, wt := range []string{relWT, absWT} {
		if got := commonDirOf(t, wt); got != shared {
			t.Errorf("worktree %s common dir = %s, want %s", wt, got, shared)
		}
	}
	if commonDirOf(t, sub) == shared || commonDirOf(t, clone) == shared {
		t.Error("a submodule or an independent clone shares the main checkout's common directory")
	}
}

// TestGitCommonDirRejectsNonCheckouts: a directory without HEAD metadata, an empty path
// and a dangling gitlink are not checkouts.
func TestGitCommonDirRejectsNonCheckouts(t *testing.T) {
	root := t.TempDir()
	stray := filepath.Join(root, "stray")
	if err := os.MkdirAll(filepath.Join(stray, ".git"), 0o750); err != nil {
		t.Fatal(err)
	}
	dangling := filepath.Join(root, "dangling")
	writeCommonDirFixture(t, filepath.Join(dangling, ".git"), "gitdir: "+filepath.Join(root, "absent")+"\n")
	for _, path := range []string{stray, dangling, "", filepath.Join(root, "missing")} {
		if _, err := GitCommonDir(context.Background(), path); !errors.Is(err, ErrNotCheckout) {
			t.Errorf("GitCommonDir(%q) error = %v, want ErrNotCheckout", path, err)
		}
	}
}

// TestGitCommonDirBoundaryCommondirFiles: an empty commondir file means the git dir is its
// own common directory; a commondir naming a missing directory is an error, never a
// silent fallback; a cancelled context stops resolution.
func TestGitCommonDirBoundaryCommondirFiles(t *testing.T) {
	root := t.TempDir()
	mainDir := filepath.Join(root, "main")
	initTestGit(t, mainDir)
	emptyWT := filepath.Join(root, "empty")
	emptyGitDir := filepath.Join(mainDir, ".git", "worktrees", "empty")
	writeCommonDirFixture(t, filepath.Join(emptyGitDir, "HEAD"), "ref: refs/heads/empty\n")
	writeCommonDirFixture(t, filepath.Join(emptyGitDir, "commondir"), "\n")
	writeCommonDirFixture(t, filepath.Join(emptyWT, ".git"), "gitdir: "+emptyGitDir+"\n")
	if got := commonDirOf(t, emptyWT); got == commonDirOf(t, mainDir) || filepath.Base(got) != "empty" {
		t.Errorf("empty commondir resolved to %s, want the worktree's own git dir", got)
	}

	badWT := filepath.Join(root, "bad")
	badGitDir := filepath.Join(mainDir, ".git", "worktrees", "bad")
	writeCommonDirFixture(t, filepath.Join(badGitDir, "HEAD"), "ref: refs/heads/bad\n")
	writeCommonDirFixture(t, filepath.Join(badGitDir, "commondir"), "../../../../absent\n")
	writeCommonDirFixture(t, filepath.Join(badWT, ".git"), "gitdir: "+badGitDir+"\n")
	if _, err := GitCommonDir(context.Background(), badWT); err == nil {
		t.Error("a commondir naming a missing directory must be an error")
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := GitCommonDir(ctx, mainDir); !errors.Is(err, context.Canceled) {
		t.Errorf("cancelled GitCommonDir error = %v", err)
	}
}
