// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package contextopt

import (
	"os"
	"path/filepath"
	"testing"
)

// symlinkedAncestor builds real/<name> and returns a path that reaches it through a symlinked
// ancestor, which is the shape macOS ships: /var is a symlink to /private/var, so every path
// under the platform's own temporary directory has one (#109).
func symlinkedAncestor(t *testing.T) (linked, real string) {
	t.Helper()
	base := t.TempDir()
	real = filepath.Join(base, "private", "tree")
	if err := os.MkdirAll(filepath.Join(real, "child"), 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(base, "var")
	if err := os.Symlink(filepath.Join(base, "private"), link); err != nil {
		t.Skipf("symlinks unavailable here: %v", err)
	}
	return filepath.Join(link, "tree"), real
}

// Positive: a confinement root reached through a symlinked ancestor opens. Before #109 this
// failed, and with it every path under macOS's temp directory.
func TestOpenDirectoryAcceptsASymlinkedAncestor(t *testing.T) {
	linked, _ := symlinkedAncestor(t)
	root, err := OpenDirectory(t.Context(), linked)
	if err != nil {
		t.Fatalf("a symlinked ancestor must not reject the root: %v", err)
	}
	if err := root.Close(); err != nil {
		t.Fatal(err)
	}
}

// Negative: a symlink *below* the root is still refused. This is the direction that matters --
// repository content is attacker-influenceable, the operator's filesystem above the root is not.
func TestOpenDirectoryInRejectsASymlinkInsideTheRoot(t *testing.T) {
	linked, real := symlinkedAncestor(t)
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(real, "escape")); err != nil {
		t.Skipf("symlinks unavailable here: %v", err)
	}

	if _, err := OpenDirectoryIn(t.Context(), linked, "escape"); err == nil {
		t.Error("a symlinked component under the root must be rejected")
	}
	// Boundary: the real sibling directory beside it still opens, so the rule rejects symlinks
	// rather than rejecting everything after one is present.
	root, err := OpenDirectoryIn(t.Context(), linked, "child")
	if err != nil {
		t.Fatalf("a real directory under the root must open: %v", err)
	}
	if err := root.Close(); err != nil {
		t.Fatal(err)
	}
}

// Negative: the root is a boundary, not a suggestion.
func TestOpenDirectoryInRefusesToLeaveTheRoot(t *testing.T) {
	linked, _ := symlinkedAncestor(t)
	for _, rel := range []string{"..", filepath.Join("child", "..", ".."), string(filepath.Separator) + "etc"} {
		if _, err := OpenDirectoryIn(t.Context(), linked, rel); err == nil {
			t.Errorf("relative path %q must not escape the root", rel)
		}
	}
}

// Negative: a file is not a confinement root.
func TestOpenDirectoryRejectsAFileAsARoot(t *testing.T) {
	_, real := symlinkedAncestor(t)
	file := filepath.Join(real, "a-file")
	if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := OpenDirectory(t.Context(), file); err == nil {
		t.Error("a regular file must not open as a confinement root")
	}
}
