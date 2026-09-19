package util

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestResolveExistingPath(t *testing.T) {
	root := t.TempDir()
	target := t.TempDir()
	if err := os.Symlink(target, filepath.Join(root, "link")); err != nil {
		t.Fatal(err)
	}
	// ResolveExistingPath resolves every symlink through the deepest existing ancestor, so
	// the expected value must be target's own fully resolved form, not its literal spelling.
	// On macOS t.TempDir() lives under /var, itself a symlink to /private/var, so target and
	// filepath.EvalSymlinks(target) differ there even before the "link" symlink this test
	// adds; comparing against the literal target failed this test on every macOS CI run
	// (#135). On Linux, which has no such ancestor symlink, EvalSymlinks(target) == target.
	resolvedTarget, err := filepath.EvalSymlinks(target)
	if err != nil {
		t.Fatal(err)
	}
	got, err := ResolveExistingPath(t.Context(), filepath.Join(root, "link", "missing", "leaf"))
	if err != nil || got != filepath.Join(resolvedTarget, "missing", "leaf") {
		t.Fatalf("resolved path: %q %v", got, err)
	}
	if err := os.Symlink("cycle", filepath.Join(root, "cycle")); err != nil {
		t.Fatal(err)
	}
	if _, err := ResolveExistingPath(t.Context(), filepath.Join(root, "cycle")); err == nil {
		t.Fatal("symlink cycle accepted")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := ResolveExistingPath(ctx, root); !errors.Is(err, context.Canceled) {
		t.Fatalf("lost cancellation: %v", err)
	}
	var absent context.Context
	if _, err := ResolveExistingPath(absent, root); err == nil {
		t.Fatal("nil context accepted")
	}
}
