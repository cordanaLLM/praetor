// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
// SPDX-License-Identifier: EUPL-1.2

package util

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestPinnedDirectoryPositive keeps mkdir/write rooted after the caller's directory is renamed.
func TestPinnedDirectoryPositive(t *testing.T) {
	root, outside := t.TempDir(), t.TempDir()
	moved := filepath.Join(t.TempDir(), "moved")
	parent := openTestRoot(t, filepath.Dir(root))
	base, err := parent.OpenRoot(filepath.Base(root))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := base.Close(); err != nil {
			t.Error(err)
		}
	})
	if err := MkdirIn(base, "nested", 0); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(root, moved); err != nil {
		t.Fatal(err)
	}
	if err := MkdirSecure(root, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := WriteFileExclusiveIn(base, "nested/log", []byte("private tail"), 0); err != nil {
		t.Fatal(err)
	}
	if got := readTestFile(t, filepath.Join(moved, "nested/log")); got != "private tail" {
		t.Fatalf("log = %q", got)
	}
	if err := WriteFileExclusiveIn(base, "nested/log", []byte("replace"), 0o600); !errors.Is(err, os.ErrExist) {
		t.Fatalf("exclusive write = %v", err)
	}
	if err := MkdirIn(base, ".", 0o700); err != nil {
		t.Fatalf("root boundary: %v", err)
	}
	assertEmptyDir(t, root)
	assertEmptyDir(t, outside)
}

// TestPinnedDirectoryNegative refuses escaping members, insecure modes and root-as-file paths.
func TestPinnedDirectoryNegative(t *testing.T) {
	root, outside := t.TempDir(), t.TempDir()
	base := openTestRoot(t, root)
	for _, rel := range []string{"../escape", outside} {
		if err := MkdirIn(base, rel, 0o700); err == nil {
			t.Errorf("mkdir accepted %q", rel)
		}
		if err := WriteFileExclusiveIn(base, rel, nil, 0o600); err == nil {
			t.Errorf("write accepted %q", rel)
		}
	}
	if err := MkdirIn(base, "bad", 0o777); !errors.Is(err, ErrInsecurePerm) {
		t.Fatalf("mkdir permissions: %v", err)
	}
	if err := WriteFileExclusiveIn(base, "bad", nil, 0o777); !errors.Is(err, ErrInsecurePerm) {
		t.Fatalf("write permissions: %v", err)
	}
	for _, rel := range []string{".", "", "nested/.."} {
		if err := WriteFileExclusiveIn(base, rel, nil, 0o600); !errors.Is(err, ErrRootItself) {
			t.Fatalf("root write %q: %v", rel, err)
		}
	}
	assertEmptyDir(t, root)
	assertEmptyDir(t, outside)
}

// TestPinnedDirectoryBoundary refuses a replaced child's identity and link before accepting its handle.
func TestPinnedDirectoryBoundary(t *testing.T) {
	root := t.TempDir()
	base := openTestRoot(t, root)
	if err := MkdirIn(base, "ledger", 0o700); err != nil {
		t.Fatal(err)
	}
	expected, err := base.Lstat("ledger")
	if err != nil {
		t.Fatal(err)
	}
	child, err := OpenDirectoryIn(base, "ledger", expected)
	if err != nil {
		t.Fatal(err)
	}
	if err := child.Close(); err != nil {
		t.Fatal(err)
	}
	if err := base.Rename("ledger", "saved"); err != nil {
		t.Fatal(err)
	}
	if err := MkdirIn(base, "ledger", 0o700); err != nil {
		t.Fatal(err)
	}
	assertPinnedReplacementRefused(t, base, expected)
	if err := base.Remove("ledger"); err != nil {
		t.Fatal(err)
	}
	if err := base.Symlink("saved", "ledger"); err != nil {
		t.Skipf("fixture symlink unavailable: %v", err)
	}
	assertPinnedReplacementRefused(t, base, expected)
}

// assertPinnedReplacementRefused requires the identity guard to name the changed directory.
func assertPinnedReplacementRefused(t *testing.T, base *os.Root, expected os.FileInfo) {
	t.Helper()
	if _, err := OpenDirectoryIn(base, "ledger", expected); err == nil || !strings.Contains(err.Error(), "replaced") {
		t.Fatalf("replacement accepted: %v", err)
	}
}
