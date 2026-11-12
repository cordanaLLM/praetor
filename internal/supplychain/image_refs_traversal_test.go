// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package supplychain

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

type fakeDirEntry struct {
	name  string
	isDir bool
}

// Name returns the directory entry name.
func (f fakeDirEntry) Name() string { return f.name }

// IsDir reports whether the entry describes a directory.
func (f fakeDirEntry) IsDir() bool { return f.isDir }

// Type returns the entry file mode type bits.
func (f fakeDirEntry) Type() os.FileMode {
	if f.isDir {
		return os.ModeDir
	}
	return 0
}

// Info returns simulated FileInfo.
func (f fakeDirEntry) Info() (os.FileInfo, error) { return nil, nil }

// makeFakeEntries generates fake directory entries for limit tests.
func makeFakeEntries(count int, isDir bool) []os.DirEntry {
	entries := make([]os.DirEntry, count)
	for i := 0; i < count && i < maxScannedFiles+10; i++ {
		entries[i] = fakeDirEntry{name: fmt.Sprintf("entry%d", i), isDir: isDir}
	}
	return entries
}

// TestImageRefDirectoryWalk_EntriesLimit_3D verifies entry count limits and boundaries in directory walk.
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

// TestImageRefDirectoryWalk_DirsLimit_3D verifies subdirectory limits and ignored directory filtering.
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

// TestIterateScannedFiles_FilesLimit_3D verifies file count limits and file processing during scan iteration.
func TestIterateScannedFiles_FilesLimit_3D(t *testing.T) {
	t.Run("files exceeding limit returns error", func(t *testing.T) {
		files := make([]string, maxScannedFiles+1)
		for i := 0; i < len(files) && i < maxScannedFiles+10; i++ {
			files[i] = fmt.Sprintf("file%d.go", i)
		}
		err := iterateScannedFiles(t.Context(), t.TempDir(), files, func(string) bool { return true }, func(string, []byte, error) error {
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
		err := iterateScannedFiles(t.Context(), dir, []string{"a.go"}, func(string) bool { return true }, func(string, []byte, error) error {
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

// TestWalkDirectoryFiles_Positive verifies that walkDirectoryFiles discovers nested files while skipping ignored directories.
func TestWalkDirectoryFiles_Positive(t *testing.T) {
	dir := t.TempDir()
	// nosemgrep: go.lang.correctness.permissions.file_permission.incorrect-default-permission, go_file-permissions_rule-mkdir -- a directory the test writes files into needs the owner execute bit; 0o700 is least privilege
	if err := os.MkdirAll(filepath.Join(dir, "sub", "deep"), 0o700); err != nil {
		t.Fatal(err)
	}
	// nosemgrep: go.lang.correctness.permissions.file_permission.incorrect-default-permission, go_file-permissions_rule-mkdir -- a directory the test writes files into needs the owner execute bit; 0o700 is least privilege
	if err := os.MkdirAll(filepath.Join(dir, "node_modules", "pkg"), 0o700); err != nil {
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
	files, err := walkDirectoryFiles(t.Context(), dir, nil)
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

// TestWalkDirectoryFiles_Negative verifies that walkDirectoryFiles returns an error for a nonexistent directory.
func TestWalkDirectoryFiles_Negative(t *testing.T) {
	_, err := walkDirectoryFiles(t.Context(), filepath.Join(t.TempDir(), "nonexistent"), nil)
	if err == nil {
		t.Fatal("expected error for nonexistent directory, got nil")
	}
}

func setMaxScannedFilesForTest(t *testing.T, capVal int) {
	t.Helper()
	orig := maxScannedFiles
	maxScannedFiles = capVal
	t.Cleanup(func() { maxScannedFiles = orig })
}

func writeNonGitCandidateFixture(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	return dir
}

const testPinnedDigest = "1111111111111111111111111111111111111111111111111111111111111111"

// TestRepositoryFiles_Positive_CandidateBound proves non-candidates above maxScannedFiles do not fail git scans (HISS-15).
func TestRepositoryFiles_Positive_CandidateBound(t *testing.T) {
	setMaxScannedFilesForTest(t, 3)
	pinned := "golang:1.27-alpine@sha256:" + testPinnedDigest
	root := fixtureRepo(t, map[string]string{
		"Dockerfile":          "FROM " + pinned + "\n",
		"deploy.yaml":         "image: " + pinned + "\n",
		"README.md":           "doc\n",
		"docs/a.md":           "a\n",
		"docs/b.md":           "b\n",
		"notes.txt":           "n\n",
		"testdata/Dockerfile": "FROM alpine:latest\n",
	})
	got, err := RepositoryFiles(t.Context(), root, ScannedFile)
	if err != nil || !slices.Equal(got, []string{"Dockerfile", "deploy.yaml"}) {
		t.Fatalf("RepositoryFiles = %v, err=%v; want [Dockerfile deploy.yaml]", got, err)
	}
	if pins, pinErr := ScanRepositoryPins(t.Context(), root); pinErr != nil || len(pins) != 2 {
		t.Fatalf("ScanRepositoryPins = %v, err=%v; want 2 pins", pins, pinErr)
	}
	if findings, imageErr := ScanImageRefs(t.Context(), root); imageErr != nil || len(findings) != 0 {
		t.Fatalf("ScanImageRefs = %v, err=%v; want 0 findings", findings, imageErr)
	}
}

// TestRepositoryFiles_Boundary_CandidateBound proves candidates equal to maxScannedFiles pass git scans (HISS-15).
func TestRepositoryFiles_Boundary_CandidateBound(t *testing.T) {
	setMaxScannedFilesForTest(t, 3)
	root := fixtureRepo(t, map[string]string{
		"Dockerfile": "FROM golang:1.27-alpine@sha256:" + testPinnedDigest + "\n",
		"a.yaml":     "k: v\n",
		"b.yaml":     "k: v\n",
		"README.md":  "doc\n",
		"notes.txt":  "n\n",
	})
	got, err := RepositoryFiles(t.Context(), root, ScannedFile)
	if err != nil || len(got) != 3 {
		t.Fatalf("boundary RepositoryFiles = %v, err=%v; want 3 candidates", got, err)
	}
}

// TestRepositoryFiles_Negative_CandidateBound proves candidates above maxScannedFiles fail naming the bound (HISS-15).
func TestRepositoryFiles_Negative_CandidateBound(t *testing.T) {
	setMaxScannedFilesForTest(t, 3)
	root := fixtureRepo(t, map[string]string{
		"Dockerfile": "FROM golang:1.27-alpine@sha256:" + testPinnedDigest + "\n",
		"a.yaml":     "k: v\n",
		"b.yaml":     "k: v\n",
		"c.yaml":     "k: v\n",
	})
	_, err := ScanRepositoryPins(t.Context(), root)
	if err == nil || !strings.Contains(err.Error(), "3") || !strings.Contains(err.Error(), "scanned candidate") {
		t.Fatalf("expected error naming bound 3 and scanned candidates, got: %v", err)
	}
}

// TestWalkDirectoryFiles_Positive_CandidateBound proves non-git directory walks count only candidates (HISS-15).
func TestWalkDirectoryFiles_Positive_CandidateBound(t *testing.T) {
	setMaxScannedFilesForTest(t, 3)
	dir := writeNonGitCandidateFixture(t, map[string]string{
		"a.md": "a\n", "b.md": "b\n", "c.txt": "c\n", "d_test.go": "package p\n",
		"Dockerfile": "FROM ubuntu:latest\n", "compose.yaml": "services:\n  app:\n    image: redis:7\n",
	})
	got, err := walkDirectoryFiles(t.Context(), dir, ScannedImageRefFile)
	if err != nil || !slices.Equal(got, []string{"Dockerfile", "compose.yaml"}) {
		t.Fatalf("walkDirectoryFiles = %v, err=%v; want [Dockerfile compose.yaml]", got, err)
	}
	if findings, scanErr := ScanImageRefs(t.Context(), dir); scanErr != nil || len(findings) != 2 {
		t.Fatalf("ScanImageRefs = %v, err=%v; want 2 findings", findings, scanErr)
	}
}

// TestWalkDirectoryFiles_Boundary_CandidateBound proves non-git candidates equal to maxScannedFiles pass (HISS-15).
func TestWalkDirectoryFiles_Boundary_CandidateBound(t *testing.T) {
	setMaxScannedFilesForTest(t, 3)
	dir := writeNonGitCandidateFixture(t, map[string]string{
		"a.md": "a\n", "b.txt": "b\n", "Dockerfile": "FROM scratch\n", "a.yaml": "k: v\n", "b.yaml": "k: v\n",
	})
	got, err := walkDirectoryFiles(t.Context(), dir, ScannedImageRefFile)
	if err != nil || len(got) != 3 {
		t.Fatalf("boundary walkDirectoryFiles = %v, err=%v; want 3 candidates", got, err)
	}
}

// TestWalkDirectoryFiles_Negative_CandidateBound proves non-git candidates above maxScannedFiles fail naming the bound (HISS-15).
func TestWalkDirectoryFiles_Negative_CandidateBound(t *testing.T) {
	setMaxScannedFilesForTest(t, 3)
	dir := writeNonGitCandidateFixture(t, map[string]string{
		"Dockerfile": "FROM scratch\n", "a.yaml": "k: v\n", "b.yaml": "k: v\n", "c.yaml": "k: v\n",
	})
	_, err := ScanImageRefs(t.Context(), dir)
	if err == nil || !strings.Contains(err.Error(), "3") || !strings.Contains(err.Error(), "scanned candidate") {
		t.Fatalf("expected error naming bound 3 and scanned candidates, got: %v", err)
	}
}

// TestScanImageRefs_ContextCancellation_3D verifies that directory walks and file reads honor context cancellation.
func TestScanImageRefs_ContextCancellation_3D(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "Dockerfile"), []byte("FROM ubuntu:latest\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "docker-compose.yml"), []byte("services:\n  app:\n    image: redis:7\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	t.Run("pre-canceled context aborts non-git directory scan", func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		_, err := ScanImageRefs(ctx, dir)
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("expected context.Canceled from ScanImageRefs on canceled context, got: %v", err)
		}
	})

	t.Run("cancel mid-iteration stops reading remaining candidate files", func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		visited := 0
		err := iterateScannedFiles(ctx, dir, []string{"Dockerfile", "docker-compose.yml"}, ScannedImageRefFile, func(string, []byte, error) error {
			visited++
			cancel()
			return nil
		})
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("expected context.Canceled on mid-iteration cancel, got: %v", err)
		}
		if visited != 1 {
			t.Fatalf("expected iteration to stop after 1 file, visited %d", visited)
		}
	})

	t.Run("active context succeeds on non-git directory scan", func(t *testing.T) {
		findings, err := ScanImageRefs(t.Context(), dir)
		if err != nil {
			t.Fatalf("unexpected error with active context: %v", err)
		}
		if len(findings) != 2 {
			t.Fatalf("expected 2 findings, got %d: %+v", len(findings), findings)
		}
	})
}

// TestIterateScannedFiles_Deadline verifies injected deadlines even for paths skipped by the filter.
func TestIterateScannedFiles_Deadline(t *testing.T) {
	for _, tc := range []struct {
		name        string
		budget      time.Duration
		wantFilters int
	}{
		{name: "expired before scan", budget: 0, wantFilters: 0},
		{name: "expires during scan", budget: time.Second, wantFilters: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			ctx, cancel := context.WithTimeout(t.Context(), tc.budget)
			defer cancel()
			filtered := 0
			err := iterateScannedFiles(ctx, root, []string{"a.go", "b.go"}, func(string) bool {
				filtered++
				<-ctx.Done()
				return false
			}, func(string, []byte, error) error {
				t.Fatal("filtered file reached visitor")
				return nil
			})
			if !errors.Is(err, context.DeadlineExceeded) || filtered != tc.wantFilters {
				t.Fatalf("scan = %v, filtered %d; want DeadlineExceeded, filtered %d", err, filtered, tc.wantFilters)
			}
		})
	}
}

// TestReadScanDirectory_BoundedRead_3D verifies that readScanDirectory reads at most maxScannedFiles+1 entries.
func TestReadScanDirectory_BoundedRead_3D(t *testing.T) {
	dir := t.TempDir()
	const extra = 5
	for i := 0; i < maxScannedFiles+extra; i++ {
		if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("f%04d.txt", i)), nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	entries, err := readScanDirectory(t.Context(), dir, ".")
	if err != nil {
		t.Fatalf("readScanDirectory failed: %v", err)
	}
	if len(entries) > maxScannedFiles+1 {
		t.Fatalf("readScanDirectory allocated %d entries, want at most %d (maxScannedFiles+1)", len(entries), maxScannedFiles+1)
	}
}

// setupConfinementFixture creates a directory layout with in-root and outside files.
func setupConfinementFixture(t *testing.T) (rootDir, outsideDir string) {
	t.Helper()
	temp := t.TempDir()
	rootDir = filepath.Join(temp, "root")
	outsideDir = filepath.Join(temp, "outside")
	// nosemgrep: go.lang.correctness.permissions.file_permission.incorrect-default-permission, go_file-permissions_rule-mkdir -- a directory the test writes files into needs the owner execute bit; 0o700 is least privilege
	if err := os.MkdirAll(rootDir, 0o700); err != nil {
		t.Fatal(err)
	}
	// nosemgrep: go.lang.correctness.permissions.file_permission.incorrect-default-permission, go_file-permissions_rule-mkdir -- a directory the test writes files into needs the owner execute bit; 0o700 is least privilege
	if err := os.MkdirAll(outsideDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(outsideDir, "secret.txt"), []byte("data"), 0o600); err != nil {
		t.Fatal(err)
	}
	subDir := filepath.Join(rootDir, "inside")
	// nosemgrep: go.lang.correctness.permissions.file_permission.incorrect-default-permission, go_file-permissions_rule-mkdir -- a directory the test writes files into needs the owner execute bit; 0o700 is least privilege
	if err := os.MkdirAll(subDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(subDir, "file.txt"), []byte("content"), 0o600); err != nil {
		t.Fatal(err)
	}
	return rootDir, outsideDir
}

func testInRootSubdirectoryReads(t *testing.T, rootDir string) {
	t.Helper()
	entries, err := readScanDirectory(t.Context(), rootDir, "inside")
	if err != nil {
		t.Fatalf("expected successful read of in-root subdirectory, got: %v", err)
	}
	if len(entries) != 1 || entries[0].Name() != "file.txt" {
		t.Fatalf("unexpected entries for inside: %+v", entries)
	}
}

func testEmptyRelDirReadsRoot(t *testing.T, rootDir string) {
	t.Helper()
	entries, err := readScanDirectory(t.Context(), rootDir, "")
	if err != nil {
		t.Fatalf("expected successful read of empty relDir, got: %v", err)
	}
	if len(entries) != 1 || entries[0].Name() != "inside" {
		t.Fatalf("unexpected entries for empty relDir: %+v", entries)
	}
}

func testEscapeDotDotRefused(t *testing.T, rootDir string) {
	t.Helper()
	if _, err := readScanDirectory(t.Context(), rootDir, "../outside"); err == nil {
		t.Fatal("expected error for escaping relDir ../outside, got nil")
	}
}

func testSymlinkEscapeRefused(t *testing.T, rootDir, outsideDir string) {
	t.Helper()
	symlinkPath := filepath.Join(rootDir, "outside_symlink")
	if err := os.Symlink(outsideDir, symlinkPath); err != nil {
		t.Skipf("symlinks not supported on this platform: %v", err)
	}
	if _, err := readScanDirectory(t.Context(), rootDir, "outside_symlink"); err == nil {
		t.Fatal("expected error for symlink escaping root, got nil")
	}
}

// TestReadScanDirectory_Confinement_3D verifies that reading directories through root confines
// access within root: in-root subdirectories read successfully, while relative traversal (..)
// or symlinks escaping the root boundary are refused.
func TestReadScanDirectory_Confinement_3D(t *testing.T) {
	rootDir, outsideDir := setupConfinementFixture(t)
	t.Run("in-root subdirectory reads successfully", func(t *testing.T) {
		testInRootSubdirectoryReads(t, rootDir)
	})
	t.Run("empty relDir reads root directory", func(t *testing.T) {
		testEmptyRelDirReadsRoot(t, rootDir)
	})
	t.Run("relDir escaping root via dot-dot is refused", func(t *testing.T) {
		testEscapeDotDotRefused(t, rootDir)
	})
	t.Run("symlink escaping root is refused", func(t *testing.T) {
		testSymlinkEscapeRefused(t, rootDir, outsideDir)
	})
}
