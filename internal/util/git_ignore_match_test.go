package util

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// TestGitIgnoreMatches_Positive_NamesTheRuleBehindEachPath: every ignored path comes back with
// the file, line and pattern that decide it; a path under an ignored directory names the rule
// that ignores the directory, and a nested .gitignore is named by its own path.
func TestGitIgnoreMatches_Positive_NamesTheRuleBehindEachPath(t *testing.T) {
	dir := ignoreRepo(t, "# build output\ndist/\n.config\n")
	if err := os.MkdirAll(filepath.Join(dir, "tools"), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "tools", ".gitignore"), []byte("*.js\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	paths := []string{"tools/figures/dist/loader.js", ".config/labels.yaml", "tools/player.js", "README.md"}
	got, err := GitIgnoreMatches(t.Context(), dir, paths, false)
	if err != nil {
		t.Fatal(err)
	}
	want := []GitIgnoreMatch{
		{Path: "tools/figures/dist/loader.js", Source: ".gitignore", Line: 2, Pattern: "dist/"},
		{Path: ".config/labels.yaml", Source: ".gitignore", Line: 3, Pattern: ".config"},
		{Path: "tools/player.js", Source: "tools/.gitignore", Line: 1, Pattern: "*.js"},
	}
	if !slices.Equal(got, want) {
		t.Fatalf("matches = %+v, want %+v", got, want)
	}
	if rule := got[0].Rule(); rule != ".gitignore:2 (dist/)" {
		t.Fatalf("Rule() = %q", rule)
	}
	if !got[0].DirectoryOnly() || got[1].DirectoryOnly() {
		t.Fatalf("DirectoryOnly: dist/ = %v, .config = %v", got[0].DirectoryOnly(), got[1].DirectoryOnly())
	}
}

// TestGitIgnoreMatches_Negative_NegatedTrackedAndUnmatchedPathsAreNotIgnored: verbose
// check-ignore also prints a path whose last rule is a negation; that path is not ignored and
// is dropped, as is a tracked path and one no rule matches.
func TestGitIgnoreMatches_Negative_NegatedTrackedAndUnmatchedPathsAreNotIgnored(t *testing.T) {
	dir := ignoreRepo(t, ".config\n!.config/\n*.log\n")
	if err := os.MkdirAll(filepath.Join(dir, ".config"), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "kept.log"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := RunGit(t.Context(), dir, "add", "-f", "kept.log"); err != nil {
		t.Fatalf("git add: %v", err)
	}
	got, err := GitIgnoreMatches(t.Context(), dir, []string{".config", ".config/labels.yaml", "kept.log", "src/main.go"}, false)
	if err != nil || len(got) != 0 {
		t.Fatalf("re-included, tracked or unmatched paths reported ignored: %+v, %v", got, err)
	}
	paths, err := GitIgnoredPaths(t.Context(), dir, []string{".config", "kept.log"}, true)
	if err != nil || !slices.Equal(paths, []string{"kept.log"}) {
		t.Fatalf("GitIgnoredPaths must agree on the negated directory: %q, %v", paths, err)
	}
}

// TestGitIgnoreMatches_Boundary_EmptyMalformedAndUnanswerable pins the edges: no paths asks git
// nothing, a malformed answer is refused rather than half read, a lone negation record is no
// match, and a host without git is an error naming git.
func TestGitIgnoreMatches_Boundary_EmptyMalformedAndUnanswerable(t *testing.T) {
	if got, err := GitIgnoreMatches(t.Context(), t.TempDir(), nil, true); got != nil || err != nil {
		t.Fatalf("empty query = %+v, %v", got, err)
	}
	for _, out := range []string{".gitignore\x001\x00dist/\x00", ".gitignore\x00one\x00dist/\x00a\x00"} {
		if _, err := parseIgnoreMatches([]byte(out)); err == nil {
			t.Fatalf("malformed answer %q accepted", out)
		}
	}
	got, err := parseIgnoreMatches([]byte(".gitignore\x002\x00!keep/\x00keep/a\x00"))
	if err != nil || len(got) != 0 {
		t.Fatalf("a lone negation record = %+v, %v", got, err)
	}
	if _, err := exec.LookPath("git"); err == nil {
		if _, err := GitIgnoreMatches(t.Context(), t.TempDir(), []string{"x"}, false); err == nil {
			t.Fatal("a directory outside any work tree answered as if git had checked it")
		}
	}
	t.Setenv("PATH", t.TempDir())
	if _, err := GitIgnoreMatches(t.Context(), t.TempDir(), []string{"x"}, false); err == nil ||
		!strings.Contains(err.Error(), "git") {
		t.Fatalf("missing git must be an error naming git, got %v", err)
	}
}
