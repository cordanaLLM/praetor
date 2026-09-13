package util

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestGitWorktreePresent(t *testing.T) {
	root := t.TempDir()
	nested := filepath.Join(root, "nested")
	if err := os.Mkdir(nested, 0755); err != nil {
		t.Fatal(err)
	}
	if present, err := GitWorktreePresent(t.Context(), nested); err != nil || present {
		t.Fatalf("unexpected metadata in empty tree: %v, %v", present, err)
	}
	if err := os.WriteFile(filepath.Join(root, ".git"), []byte("gitdir: linked-metadata\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if present, err := GitWorktreePresent(t.Context(), nested); err != nil || !present {
		t.Fatalf("linked-worktree metadata not detected through ancestor: %v, %v", present, err)
	}
	if _, err := GitWorktreePresent(t.Context(), filepath.Join(root, ".git")); err == nil {
		t.Fatal("file root must fail")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := GitWorktreePresent(ctx, root); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation did not propagate: %v", err)
	}
}
