package worktree

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/cordanaLLM/praetor/internal/util"
)

// Positive: the worktree container is created below the repository on first use.
func TestWorktreeContainer_Positive_CreatedInsideRepository(t *testing.T) {
	repoDir := setupTestGitRepo(t)
	wt, err := NewManager(repoDir).Create(context.Background(), "task-container", "main")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if info, err := os.Lstat(filepath.Join(repoDir, filepath.FromSlash(WorktreeSubdir))); err != nil || !info.IsDir() {
		t.Fatalf("worktree container = %v, %v; want a directory inside the repository", info, err)
	}
	if wt.Path == "" {
		t.Fatal("Create returned no worktree path")
	}
}

// Negative: a .standards linked outside the repository is refused before any directory or
// worktree is created through it (BUG-826).
func TestWorktreeContainer_Negative_EscapingLinkRefused(t *testing.T) {
	repoDir := setupTestGitRepo(t)
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(repoDir, ".standards")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if _, err := NewManager(repoDir).Create(context.Background(), "task-escape", "main"); !errors.Is(err, util.ErrPathEscapesRoot) {
		t.Fatalf("escaping .standards = %v, want ErrPathEscapesRoot", err)
	}
	if entries, err := os.ReadDir(outside); err != nil || len(entries) != 0 {
		t.Fatalf("outside directory = %v, %v; want nothing created through the link", entries, err)
	}
}

// Boundary: a .standards that is a relative link staying inside the repository is followed;
// the confinement edge is the repository root.
func TestWorktreeContainer_Boundary_InRootLinkFollowed(t *testing.T) {
	repoDir := setupTestGitRepo(t)
	if err := os.MkdirAll(filepath.Join(repoDir, "state"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("state", filepath.Join(repoDir, ".standards")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if _, err := NewManager(repoDir).Create(context.Background(), "task-inroot", "main"); err != nil {
		t.Fatalf("in-repository linked .standards = %v, want the worktree created through it", err)
	}
	if _, err := os.Stat(filepath.Join(repoDir, "state", "worktrees", "task-inroot")); err != nil {
		t.Fatalf("worktree not created through the in-repository link: %v", err)
	}
}
