package adopt

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/util"
)

const worktreesIgnoreRule = "/.standards/worktrees/"

// TestAdoptionReconcilesManagedIgnoreRulesOnce adopts a repository whose ignore file
// already carries the private rule with further lines after it, which is exactly the
// shape the old suffix test mistook for absent, and asserts both managed rules appear
// exactly once no matter how often adoption runs.
func TestAdoptionReconcilesManagedIgnoreRulesOnce(t *testing.T) {
	for name, existing := range map[string]string{
		"rule-then-later-lines": "/.workingdir/\nbin/\ncoverage.txt\n",
		"no-trailing-newline":   "bin/\n/.workingdir/",
		"worktrees-already-set": worktreesIgnoreRule + "\n",
		"later-negations":       "/.workingdir/\n/.workingdir2/\n!/.workingdir/\n!/.workingdir/**\n!/.workingdir2/\n!/.workingdir2/**\n",
		"leading-spaces":        " /.workingdir/\n /.workingdir2/\n",
		"crlf-with-negation":    "/.workingdir/\r\n!/.workingdir/\r\n!/.workingdir/**\r\n",
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
			assertPrivateRootsIgnored(t, root)
			if name == "crlf-with-negation" && strings.Count(first, "\n") != strings.Count(first, "\r\n") {
				t.Fatalf("CRLF ignore file gained mixed endings: %q", first)
			}
		})
	}
}

func TestMergeGitIgnoreNegativeRejectsAmbiguousManagedMarkers(t *testing.T) {
	for _, input := range []string{
		gitIgnoreManagedBegin + "\nmissing end\n",
		gitIgnoreManagedEnd + "\n",
		" " + gitIgnoreManagedBegin + "\n" + gitIgnoreManagedEnd + "\n",
		gitIgnoreManagedBegin + "\n " + gitIgnoreManagedEnd + "\n",
		gitIgnoreManagedBegin + "\n" + gitIgnoreManagedBegin + "\n" + gitIgnoreManagedEnd + "\n",
		gitIgnoreManagedBegin + "\n" + gitIgnoreManagedEnd + "\n" +
			gitIgnoreManagedBegin + "\n" + gitIgnoreManagedEnd + "\n",
	} {
		if _, err := mergeGitIgnore(input); err == nil {
			t.Fatalf("ambiguous managed ignore markers accepted: %q", input)
		}
	}
}

func TestMergeGitIgnoreRejectsInconsistentLineEndings(t *testing.T) {
	for name, input := range map[string]string{
		"mixed":   "operator/\r\ncache/\n",
		"lone CR": "operator/\rcache/",
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := mergeGitIgnore(input); err == nil {
				t.Fatal("inconsistent .gitignore line endings accepted")
			}
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

func assertPrivateRootsIgnored(t *testing.T, root string) {
	t.Helper()
	for _, private := range []string{".workingdir/OPEN.md", ".workingdir2/evidence/private.md"} {
		if _, err := util.RunGit(t.Context(), root, "check-ignore", "--no-index", "--", private); err != nil {
			t.Fatalf("private path %s is not effectively ignored: %v", private, err)
		}
	}
}

// countIgnoreRule counts canonical Git patterns; leading spaces are pattern bytes.
func countIgnoreRule(text, rule string) int {
	count := 0
	for _, line := range strings.Split(text, "\n") {
		if strings.TrimSuffix(line, "\r") == rule {
			count++
		}
	}
	return count
}
