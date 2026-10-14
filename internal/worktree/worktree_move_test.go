package worktree

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Positive (#1233): Move relocates a registered worktree so Git lists and resolves it at the
// new path.
func TestWorktreeMove_Positive_RelocatesRegisteredWorktree(t *testing.T) {
	root := setupTestGitRepo(t)
	from := filepath.Join(root, "old", "feature")
	runInDir(t, root, "worktree", "add", "-b", "feature", from)
	to := filepath.Join(root, "new", "feature")
	if err := os.MkdirAll(filepath.Dir(to), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := NewManager(root).Move(t.Context(), from, to); err != nil {
		t.Fatalf("move failed: %v", err)
	}
	if branch := strings.TrimSpace(runInDir(t, to, "rev-parse", "--abbrev-ref", "HEAD")); branch != "feature" {
		t.Fatalf("moved worktree resolves to %q", branch)
	}
	if _, err := os.Stat(from); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("old worktree path still exists: %v", err)
	}
}

// Negative: a path that is not a registered worktree and a nil manager are refused.
func TestWorktreeMove_Negative_UnregisteredPathAndNilManager(t *testing.T) {
	root := setupTestGitRepo(t)
	plain := filepath.Join(root, "plain")
	if err := os.Mkdir(plain, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := NewManager(root).Move(t.Context(), plain, filepath.Join(root, "moved")); err == nil {
		t.Fatal("moved a directory git does not register")
	}
	var nilManager *Manager
	if err := nilManager.Move(t.Context(), plain, filepath.Join(root, "moved")); !errors.Is(err, ErrNilManager) {
		t.Fatalf("nil manager: %v", err)
	}
}

// Boundary: git refuses a destination that already exists, and the worktree stays put.
func TestWorktreeMove_Boundary_ExistingDestinationRefused(t *testing.T) {
	root := setupTestGitRepo(t)
	from := filepath.Join(root, "wt")
	runInDir(t, root, "worktree", "add", "-b", "wt", from)
	to := filepath.Join(root, "taken")
	if err := os.WriteFile(to, []byte("keep\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := NewManager(root).Move(t.Context(), from, to); err == nil {
		t.Fatal("moved onto an existing path")
	}
	if data, err := os.ReadFile(to); err != nil || string(data) != "keep\n" {
		t.Fatalf("existing destination changed: %q, %v", data, err)
	}
	if branch := strings.TrimSpace(runInDir(t, from, "rev-parse", "--abbrev-ref", "HEAD")); branch != "wt" {
		t.Fatalf("worktree no longer resolves at its old path: %q", branch)
	}
}
