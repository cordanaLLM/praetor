package devsync

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func writeTestFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func readTestFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// makeRepo creates a minimal repository: .git/HEAD plus one tracked file and one build cache.
func makeRepo(t *testing.T, dir string) {
	t.Helper()
	writeTestFile(t, filepath.Join(dir, ".git", "HEAD"), "ref: refs/heads/main\n")
	writeTestFile(t, filepath.Join(dir, "main.go"), "package main\n")
	writeTestFile(t, filepath.Join(dir, "node_modules", "dep", "index.js"), "cache\n")
}

// makeDevTree builds the dev folder used by the push and pull tests:
//
//	org/app      repository
//	org/notes.md loose file beside the repository
//	solo         repository at the top level
//	scratch/     folder without a repository
//	empty-org/x  repository in a folder holding nothing else
func makeDevTree(t *testing.T) string {
	t.Helper()
	dev := filepath.Join(t.TempDir(), "dev")
	makeRepo(t, filepath.Join(dev, "org", "app"))
	writeTestFile(t, filepath.Join(dev, "org", "notes.md"), "notes\n")
	makeRepo(t, filepath.Join(dev, "solo"))
	writeTestFile(t, filepath.Join(dev, "scratch", "idea.txt"), "idea\n")
	writeTestFile(t, filepath.Join(dev, "scratch", "build", "out.bin"), "cache\n")
	makeRepo(t, filepath.Join(dev, "empty-org", "x"))
	writeTestFile(t, filepath.Join(dev, "loose.txt"), "not archived\n")
	return dev
}

// makeSizeCapTree builds a dev folder for the --max-archive-size tests: two repositories with
// a large, deterministic gap between their measured sizes, so a cap between them skips exactly
// one without depending on the exact byte counts of unrelated fixtures.
//
//	small  repository holding a few bytes
//	big    repository holding a 4096-byte payload file
func makeSizeCapTree(t *testing.T) string {
	t.Helper()
	dev := filepath.Join(t.TempDir(), "dev")
	makeRepo(t, filepath.Join(dev, "small"))
	writeTestFile(t, filepath.Join(dev, "big", ".git", "HEAD"), "ref: refs/heads/main\n")
	writeTestFile(t, filepath.Join(dev, "big", "payload.bin"), strings.Repeat("x", 4096))
	return dev
}

// requireSymlinks skips a test on platforms where creating a symbolic link needs a privilege
// the test process may lack.
func requireSymlinks(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("creating symbolic links on Windows needs Developer Mode or an elevated token")
	}
}
