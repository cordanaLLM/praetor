package contextopt

import (
	"os"
	"path/filepath"
	"testing"
)

// macOS puts TMPDIR under /var/folders/..., and /var is a symlink to /private/var. The walk
// used to start at the volume root and inspect every component, so it rejected the very first
// one and every test that needed a temporary directory failed -- the entire macOS leg of the
// portability matrix, on every run for a day (#135).
//
// The shape is reproduced here rather than asserted, so the regression is caught on any host
// instead of only on the platform that has it.
func TestEnsureDirectory_Positive_AcceptsASymlinkedAncestor(t *testing.T) {
	root := t.TempDir()
	real := filepath.Join(root, "private", "var")
	if err := os.MkdirAll(filepath.Join(real, "folders", "xy", "T"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(real, filepath.Join(root, "var")); err != nil {
		t.Skipf("symlinks unavailable on this host: %v", err)
	}
	target := filepath.Join(root, "var", "folders", "xy", "T", "praetor", "child")
	if err := EnsureDirectory(t.Context(), target, 0o700); err != nil {
		t.Fatalf("a symlinked ancestor was rejected: %v", err)
	}
	info, err := os.Stat(target)
	if err != nil || !info.IsDir() {
		t.Fatalf("directory not created through the symlinked ancestor: %v", err)
	}
}

// The confinement property is unchanged: a symlink praetor would have to create through is
// still refused, because the ancestry probe uses Lstat and a symlink is never a valid base.
func TestEnsureDirectory_Negative_StillRefusesToCreateThroughASymlink(t *testing.T) {
	governed, outside := t.TempDir(), t.TempDir()
	if err := os.Symlink(outside, filepath.Join(governed, "linked")); err != nil {
		t.Skipf("symlinks unavailable on this host: %v", err)
	}
	if err := EnsureDirectory(t.Context(), filepath.Join(governed, "linked", "child"), 0o700); err == nil {
		t.Fatal("directory creation followed a symlink out of the governed tree")
	}
	if _, err := os.Stat(filepath.Join(outside, "child")); !os.IsNotExist(err) {
		t.Fatal("a directory was created outside the governed tree")
	}
}

// Boundary: an absent ancestor chain is created, and a path deeper than the bound is refused.
func TestEnsureDirectory_Boundary_DeepChainAndOverlongPath(t *testing.T) {
	root := t.TempDir()
	deep := filepath.Join(root, "a", "b", "c", "d", "e")
	if err := EnsureDirectory(t.Context(), deep, 0o700); err != nil {
		t.Fatalf("absent chain not created: %v", err)
	}
	overlong := root
	for i := 0; i < maxPathComponents+2; i++ {
		overlong = filepath.Join(overlong, "x")
	}
	if err := EnsureDirectory(t.Context(), overlong, 0o700); err == nil {
		t.Fatal("a path beyond the component bound was accepted")
	}
}
