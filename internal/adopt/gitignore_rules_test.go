package adopt

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/util"
)

const worktreesIgnoreRule = "/.standards/worktrees/"

// TestMissingIgnoreRules covers the membership scan directly: rules that are present
// anywhere in the file (positive), rules that only look like the managed ones
// (negative), and the degenerate texts a repository can actually carry (boundary).
func TestMissingIgnoreRules(t *testing.T) {
	for name, tc := range map[string]struct {
		text string
		want []string
	}{
		"empty":                 {"", []string{"/.workingdir/", worktreesIgnoreRule}},
		"both-present":          {"/.workingdir/\n" + worktreesIgnoreRule + "\n", nil},
		"followed-by-two-lines": {"/.workingdir/\nbin/\ncoverage.txt\n", []string{worktreesIgnoreRule}},
		"worktrees-only":        {worktreesIgnoreRule + "\nbin/\n", []string{"/.workingdir/"}},
		"no-trailing-newline":   {"bin/\n/.workingdir/", []string{worktreesIgnoreRule}},
		"padded-lines":          {"  /.workingdir/  \n\t" + worktreesIgnoreRule + "\t\n", nil},
		"missing-leading-slash": {".workingdir/\n.standards/worktrees/\n", []string{"/.workingdir/", worktreesIgnoreRule}},
		"missing-trailing-slash": {
			"/.workingdir\n/.standards/worktrees\n",
			[]string{"/.workingdir/", worktreesIgnoreRule},
		},
		"commented-out": {"# /.workingdir/\n#" + worktreesIgnoreRule + "\n", []string{"/.workingdir/", worktreesIgnoreRule}},
	} {
		t.Run(name, func(t *testing.T) {
			got := missingIgnoreRules(tc.text)
			if len(got) != len(tc.want) {
				t.Fatalf("missing rules for %q: got %v, want %v", tc.text, got, tc.want)
			}
			for i, rule := range tc.want {
				if got[i] != rule {
					t.Fatalf("missing rule %d: got %q, want %q", i, got[i], rule)
				}
			}
		})
	}
}

// TestAdoptionReconcilesManagedIgnoreRulesOnce adopts a repository whose ignore file
// already carries the private rule with further lines after it, which is exactly the
// shape the old suffix test mistook for absent, and asserts both managed rules appear
// exactly once no matter how often adoption runs.
func TestAdoptionReconcilesManagedIgnoreRulesOnce(t *testing.T) {
	for name, existing := range map[string]string{
		"rule-then-later-lines": "/.workingdir/\nbin/\ncoverage.txt\n",
		"no-trailing-newline":   "bin/\n/.workingdir/",
		"worktrees-already-set": worktreesIgnoreRule + "\n",
		"empty-file":            "",
	} {
		t.Run(name, func(t *testing.T) {
			root := newTestRepo(t, name)
			path := filepath.Join(root, ".gitignore")
			mustWrite(t, path, existing)
			opts := AdoptOptions{Path: root, Profile: "framework", LockSourceRoot: newAdoptLockSource(t)}
			if _, err := Adopt(t.Context(), opts); err != nil {
				t.Fatal(err)
			}
			first := mustRead(t, path)
			for _, rule := range managedIgnoreRules {
				if n := countIgnoreRule(first, rule); n != 1 {
					t.Fatalf("rule %q appears %d times after one adoption:\n%s", rule, n, first)
				}
			}
			if _, err := Adopt(t.Context(), opts); err != nil {
				t.Fatal(err)
			}
			if second := mustRead(t, path); second != first {
				t.Fatalf("second adoption rewrote the ignore file:\n%s\n---\n%s", first, second)
			}
			assertGateWorktreeIgnored(t, root)
		})
	}
}

// assertGateWorktreeIgnored proves the reconciled rule is the one git honours for a
// leftover gate worktree, rather than a line that merely reads correctly.
func assertGateWorktreeIgnored(t *testing.T, root string) {
	t.Helper()
	leftover := ".standards/worktrees/wt-abandoned/AGENTS.md"
	if _, err := util.RunGit(t.Context(), root, "check-ignore", "--no-index", "--", leftover); err != nil {
		t.Fatalf("leftover gate worktree %s is not ignored: %v", leftover, err)
	}
}

// countIgnoreRule counts whole lines equal to rule, ignoring surrounding whitespace.
func countIgnoreRule(text, rule string) int {
	count := 0
	for _, line := range strings.Split(text, "\n") {
		if strings.TrimSpace(line) == rule {
			count++
		}
	}
	return count
}
