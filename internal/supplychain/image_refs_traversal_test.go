// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package supplychain

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type fakeDirEntry struct {
	name  string
	isDir bool
}

func (f fakeDirEntry) Name() string { return f.name }
func (f fakeDirEntry) IsDir() bool  { return f.isDir }
func (f fakeDirEntry) Type() os.FileMode {
	if f.isDir {
		return os.ModeDir
	}
	return 0
}
func (f fakeDirEntry) Info() (os.FileInfo, error) { return nil, nil }

func makeFakeEntries(count int, isDir bool) []os.DirEntry {
	entries := make([]os.DirEntry, count)
	for i := 0; i < count && i < maxScannedFiles+10; i++ {
		entries[i] = fakeDirEntry{name: fmt.Sprintf("entry%d", i), isDir: isDir}
	}
	return entries
}

func TestImageRefDirectoryWalk_EntriesLimit_3D(t *testing.T) {
	t.Run("entries exceeding maxScannedFiles returns error", func(t *testing.T) {
		walk := &imageRefDirectoryWalk{}
		entries := makeFakeEntries(maxScannedFiles+1, false)
		err := walk.appendEntries(".", entries)
		if err == nil {
			t.Fatalf("expected error for %d entries, got nil", len(entries))
		}
		if !strings.Contains(err.Error(), "entries") {
			t.Fatalf("unexpected error message: %v", err)
		}
	})

	t.Run("entries at boundary maxScannedFiles succeeds", func(t *testing.T) {
		walk := &imageRefDirectoryWalk{}
		entries := makeFakeEntries(maxScannedFiles, false)
		if err := walk.appendEntries(".", entries); err != nil {
			t.Fatalf("expected success for %d entries, got: %v", len(entries), err)
		}
		if len(walk.files) != maxScannedFiles {
			t.Fatalf("expected %d files, got %d", maxScannedFiles, len(walk.files))
		}
	})
}

func TestImageRefDirectoryWalk_DirsLimit_3D(t *testing.T) {
	t.Run("subdirectories reaching limit returns error", func(t *testing.T) {
		dirs := make([]string, maxScannedFiles)
		for i := 0; i < maxScannedFiles; i++ {
			dirs[i] = fmt.Sprintf("dir%d", i)
		}
		walk := &imageRefDirectoryWalk{dirs: dirs}
		entries := []os.DirEntry{fakeDirEntry{name: "sub", isDir: true}}
		err := walk.appendEntries(".", entries)
		if err == nil {
			t.Fatalf("expected error when dirs reach %d, got nil", maxScannedFiles)
		}
		if !strings.Contains(err.Error(), "directories") {
			t.Fatalf("unexpected error message: %v", err)
		}
	})

	t.Run("ignored directory names are not appended", func(t *testing.T) {
		walk := &imageRefDirectoryWalk{}
		entries := []os.DirEntry{
			fakeDirEntry{name: ".git", isDir: true},
			fakeDirEntry{name: ".workingdir", isDir: true},
			fakeDirEntry{name: "node_modules", isDir: true},
			fakeDirEntry{name: "vendor", isDir: true},
		}
		if err := walk.appendEntries(".", entries); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(walk.dirs) != 0 {
			t.Fatalf("expected 0 dirs, got %d", len(walk.dirs))
		}
	})
}

func TestIterateScannedFiles_FilesLimit_3D(t *testing.T) {
	t.Run("files exceeding limit returns error", func(t *testing.T) {
		files := make([]string, maxScannedFiles+1)
		for i := 0; i < len(files) && i < maxScannedFiles+10; i++ {
			files[i] = fmt.Sprintf("file%d.go", i)
		}
		err := iterateScannedFiles(t.TempDir(), files, func(string) bool { return true }, func(string, []byte, error) error {
			return nil
		})
		if err == nil {
			t.Fatalf("expected error for %d files, got nil", len(files))
		}
		if !strings.Contains(err.Error(), "files") {
			t.Fatalf("unexpected error message: %v", err)
		}
	})

	t.Run("files within limit processed", func(t *testing.T) {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "a.go"), []byte("package a"), 0o600); err != nil {
			t.Fatal(err)
		}
		visited := 0
		err := iterateScannedFiles(dir, []string{"a.go"}, func(string) bool { return true }, func(string, []byte, error) error {
			visited++
			return nil
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if visited != 1 {
			t.Fatalf("expected 1 visited file, got %d", visited)
		}
	})
}

func TestWalkDirectoryFiles_Positive(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "sub", "deep"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "node_modules", "pkg"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "root.txt"), []byte("a"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "sub", "deep", "file.go"), []byte("b"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "node_modules", "pkg", "ignored.js"), []byte("c"), 0o600); err != nil {
		t.Fatal(err)
	}
	files, err := walkDirectoryFiles(dir)
	if err != nil {
		t.Fatalf("unexpected walk error: %v", err)
	}
	expected := map[string]bool{"root.txt": true, "sub/deep/file.go": true}
	if len(files) != len(expected) {
		t.Fatalf("expected %d files, got %d: %v", len(expected), len(files), files)
	}
	for i := 0; i < len(files) && i < 10; i++ {
		if !expected[files[i]] {
			t.Errorf("unexpected file in walk: %s", files[i])
		}
	}
}

func TestWalkDirectoryFiles_Negative(t *testing.T) {
	_, err := walkDirectoryFiles(filepath.Join(t.TempDir(), "nonexistent"))
	if err == nil {
		t.Fatal("expected error for nonexistent directory, got nil")
	}
}
