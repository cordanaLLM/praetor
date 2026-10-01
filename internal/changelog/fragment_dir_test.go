// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package changelog

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// assertFragmentDirPresent fails t unless FragmentDirPresent reports want for repo without error.
func assertFragmentDirPresent(t *testing.T, repo string, want bool, what string) {
	t.Helper()
	present, err := FragmentDirPresent(context.Background(), repo)
	if err != nil || present != want {
		t.Fatalf("%s: present=%v err=%v, want present=%v", what, present, err, want)
	}
}

// TestFragmentDirPresent_Positive: a fragment directory holding a file git tracks counts,
// whether that file is a fragment, the placeholder a release render leaves, or another file.
func TestFragmentDirPresent_Positive(t *testing.T) {
	withFragment := t.TempDir()
	if _, err := CreateFragment(withFragment, Fragment{Type: TypeFixed, Title: "Fix a defect"}); err != nil {
		t.Fatal(err)
	}
	assertFragmentDirPresent(t, withFragment, true, "fragment directory with a fragment")
	for _, name := range []string{FragmentPlaceholder, "README.md"} {
		repo := t.TempDir()
		if err := os.Mkdir(filepath.Join(repo, FragmentDir), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(repo, FragmentDir, name), nil, 0o644); err != nil {
			t.Fatal(err)
		}
		assertFragmentDirPresent(t, repo, true, "fragment directory holding only "+name)
	}
}

// TestFragmentDirPresent_Negative: no directory of that name, an empty directory (which a fresh
// clone of the same commit does not have, since git keeps no empty directory), and a context
// that cannot do I/O.
func TestFragmentDirPresent_Negative(t *testing.T) {
	repo := t.TempDir()
	assertFragmentDirPresent(t, repo, false, "repository without the directory")
	if err := os.Mkdir(filepath.Join(repo, FragmentDir), 0o755); err != nil {
		t.Fatal(err)
	}
	assertFragmentDirPresent(t, repo, false, "empty fragment directory")
	var nilContext context.Context
	if _, err := FragmentDirPresent(nilContext, repo); err == nil {
		t.Fatal("a nil context must be an error")
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := FragmentDirPresent(canceled, repo); err == nil {
		t.Fatal("a canceled context must be an error")
	}
}

// TestFragmentDirPresent_Boundary: a file, or a symbolic link to a directory, carrying the name
// is no fragment directory, since the fragment reader refuses to open either; a directory that
// holds only a subdirectory holds nothing git would keep.
func TestFragmentDirPresent_Boundary(t *testing.T) {
	repo := t.TempDir()
	if err := os.WriteFile(filepath.Join(repo, FragmentDir), []byte("not a directory\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	assertFragmentDirPresent(t, repo, false, "file named "+FragmentDir)
	nested := t.TempDir()
	if err := os.MkdirAll(filepath.Join(nested, FragmentDir, "archive"), 0o755); err != nil {
		t.Fatal(err)
	}
	assertFragmentDirPresent(t, nested, false, "fragment directory holding only an empty subdirectory")
	linked := t.TempDir()
	target := t.TempDir()
	if err := os.WriteFile(filepath.Join(target, FragmentPlaceholder), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(linked, FragmentDir)); err != nil {
		// Windows creates a symbolic link only with developer mode or the privilege (HISS-21).
		t.Skipf("symbolic links unavailable on this platform: %v", err)
	}
	assertFragmentDirPresent(t, linked, false, "symbolic link named "+FragmentDir)
}
