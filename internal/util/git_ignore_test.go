package util

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// ignoreRepo initialises a work tree whose .gitignore holds rules, or skips the test when git
// is unavailable (HISS-21: the helper asks git, and a host without git cannot answer).
func ignoreRepo(t *testing.T, rules string) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skipf("git unavailable: %v", err)
	}
	dir := t.TempDir()
	if _, err := RunGit(t.Context(), dir, "init", "-q"); err != nil {
		t.Fatalf("git init: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".gitignore"), []byte(rules), 0o600); err != nil {
		t.Fatal(err)
	}
	return dir
}

// TestGitIgnoredPaths_Positive_BareDirectoryIgnoreSwallowsNestedPaths is the kernel-tree
// shape: a bare .config rule excludes every path under .config, however deep.
func TestGitIgnoredPaths_Positive_BareDirectoryIgnoreSwallowsNestedPaths(t *testing.T) {
	dir := ignoreRepo(t, ".config\n")
	paths := []string{".config/archetypes/framework.yaml", ".config/archetypes/facets/security-high.yaml", "README.md"}
	got, err := GitIgnoredPaths(t.Context(), dir, paths, false)
	if err != nil {
		t.Fatal(err)
	}
	want := paths[:2]
	if !slices.Equal(got, want) {
		t.Fatalf("ignored = %q, want %q", got, want)
	}
}

// TestGitIgnoredPaths_Negative_UnmatchedAndTrackedPathsAreNotIgnored: a path no rule matches
// is not reported, and neither is a tracked one, whose changes git commits whatever its rules
// say, unless noIndex asks about the rules alone.
func TestGitIgnoredPaths_Negative_UnmatchedAndTrackedPathsAreNotIgnored(t *testing.T) {
	dir := ignoreRepo(t, "*.log\n")
	if err := os.WriteFile(filepath.Join(dir, "kept.log"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := RunGit(t.Context(), dir, "add", "-f", "kept.log"); err != nil {
		t.Fatalf("git add: %v", err)
	}
	got, err := GitIgnoredPaths(t.Context(), dir, []string{"src/main.go", "kept.log"}, false)
	if err != nil || len(got) != 0 {
		t.Fatalf("unmatched or tracked paths reported ignored: %q, %v", got, err)
	}
	got, err = GitIgnoredPaths(t.Context(), dir, []string{"kept.log"}, true)
	if err != nil || !slices.Equal(got, []string{"kept.log"}) {
		t.Fatalf("noIndex must report the rule match on a tracked path: %q, %v", got, err)
	}
}

// TestGitIgnoredPaths_Boundary_EmptyOversizedMalformedAndUnanswerable pins the edges: no
// paths asks git nothing, a query past the bound or with a NUL is refused before git runs, and
// a directory that is no work tree, or a host without git, is an error rather than "nothing
// ignored".
func TestGitIgnoredPaths_Boundary_EmptyOversizedMalformedAndUnanswerable(t *testing.T) {
	if got, err := GitIgnoredPaths(t.Context(), t.TempDir(), nil, false); got != nil || err != nil {
		t.Fatalf("empty query = %q, %v", got, err)
	}
	oversized := make([]string, maxGitIgnoreQueryPaths+1)
	for i := range oversized {
		oversized[i] = "p"
	}
	for _, paths := range [][]string{oversized, {"a\x00b"}, {""}} {
		if _, err := GitIgnoredPaths(t.Context(), t.TempDir(), paths, false); err == nil {
			t.Fatalf("malformed query of %d paths accepted", len(paths))
		}
	}
	if _, err := exec.LookPath("git"); err == nil {
		if _, err := GitIgnoredPaths(t.Context(), t.TempDir(), []string{"x"}, false); err == nil {
			t.Fatal("a directory outside any work tree answered as if git had checked it")
		}
	}
	t.Setenv("PATH", t.TempDir())
	if _, err := GitIgnoredPaths(t.Context(), t.TempDir(), []string{"x"}, false); err == nil ||
		!strings.Contains(err.Error(), "git") {
		t.Fatalf("missing git must be an error naming git, got %v", err)
	}
}

// TestRunGitProbeStatus_AcceptsMatchAndNoMatchOnly: 0 and 1 are answers, anything else is not.
func TestRunGitProbeStatus_AcceptsMatchAndNoMatchOnly(t *testing.T) {
	dir := ignoreRepo(t, "*.log\n")
	if _, status, err := RunGitProbeStatus(t.Context(), dir, 1<<16, "check-ignore", "-q", "a.log"); err != nil || status != 0 {
		t.Fatalf("match = %d, %v", status, err)
	}
	if _, status, err := RunGitProbeStatus(t.Context(), dir, 1<<16, "check-ignore", "-q", "a.txt"); err != nil || status != 1 {
		t.Fatalf("no match = %d, %v", status, err)
	}
	if _, status, err := RunGitProbeStatus(t.Context(), dir, 1<<16, "check-ignore", "--no-such-flag"); err == nil || status != -1 {
		t.Fatalf("usage failure must be an error, got %d, %v", status, err)
	}
}

func TestIsScratchDir(t *testing.T) {
	for _, name := range []string{".claude", ".standards", ".workingdir", ".workingdir2"} {
		if !IsScratchDir(name) {
			t.Fatalf("%s must be a scratch directory", name)
		}
	}
	// Exact names only: a lookalike, a path or a different case is not the directory.
	for _, name := range []string{"claude", ".Claude", ".workingdir3", ".claude/worktrees", "", "src"} {
		if IsScratchDir(name) {
			t.Fatalf("%q must not be a scratch directory", name)
		}
	}
}
