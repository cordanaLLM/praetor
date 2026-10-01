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

// TestFragmentDirPresent_Positive: a repository that keeps the fragment directory, empty or
// holding a fragment, has one.
func TestFragmentDirPresent_Positive(t *testing.T) {
	repo := t.TempDir()
	if err := os.Mkdir(filepath.Join(repo, FragmentDir), 0o755); err != nil {
		t.Fatal(err)
	}
	if present, err := FragmentDirPresent(context.Background(), repo); err != nil || !present {
		t.Fatalf("empty fragment directory: present=%v err=%v", present, err)
	}
	if _, err := CreateFragment(repo, Fragment{Type: TypeFixed, Title: "Fix a defect"}); err != nil {
		t.Fatal(err)
	}
	if present, err := FragmentDirPresent(context.Background(), repo); err != nil || !present {
		t.Fatalf("fragment directory with a fragment: present=%v err=%v", present, err)
	}
}

// TestFragmentDirPresent_Negative: no directory of that name, and a context that cannot do I/O.
func TestFragmentDirPresent_Negative(t *testing.T) {
	repo := t.TempDir()
	if present, err := FragmentDirPresent(context.Background(), repo); err != nil || present {
		t.Fatalf("repository without the directory: present=%v err=%v", present, err)
	}
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
// is no fragment directory, since the fragment reader refuses to open either.
func TestFragmentDirPresent_Boundary(t *testing.T) {
	repo := t.TempDir()
	if err := os.WriteFile(filepath.Join(repo, FragmentDir), []byte("not a directory\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if present, err := FragmentDirPresent(context.Background(), repo); err != nil || present {
		t.Fatalf("file named %s: present=%v err=%v", FragmentDir, present, err)
	}
	linked := t.TempDir()
	if err := os.Symlink(t.TempDir(), filepath.Join(linked, FragmentDir)); err != nil {
		// Windows creates a symbolic link only with developer mode or the privilege (HISS-21).
		t.Skipf("symbolic links unavailable on this platform: %v", err)
	}
	if present, err := FragmentDirPresent(context.Background(), linked); err != nil || present {
		t.Fatalf("symbolic link named %s: present=%v err=%v", FragmentDir, present, err)
	}
}
