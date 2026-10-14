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
// exactly once no matter how often adoption runs. A repository whose own negation un-ignores
// the legacy scratch root keeps it in Git (#1072): the block leaves legacyScratchIgnore out,
// says why, and the root stays visible, while the private working directory, negated too,
// is still ignored.
func TestAdoptionReconcilesManagedIgnoreRulesOnce(t *testing.T) {
	withheld := map[string]bool{"later-negations": true}
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
			report, err := Adopt(t.Context(), opts)
			if err != nil {
				t.Fatal(err)
			}
			first := mustRead(t, path)
			for _, rule := range managedIgnoreRules {
				want := 1
				if rule == legacyScratchIgnore && withheld[name] {
					want = 0
				}
				if n := countIgnoreRule(first, rule); n != want {
					t.Fatalf("rule %q appears %d times after one adoption, want %d:\n%s", rule, n, want, first)
				}
			}
			if note := findActionDetail(report.ActionDetails, gitIgnoreFile); withheld[name] != strings.Contains(note, "left "+legacyScratchIgnore+" out of the block") {
				t.Fatalf("withheld=%v but the %s entry says %q", withheld[name], gitIgnoreFile, note)
			}
			if _, err := Adopt(t.Context(), opts); err != nil {
				t.Fatal(err)
			}
			if second := mustRead(t, path); second != first {
				t.Fatalf("second adoption rewrote the ignore file:\n%s\n---\n%s", first, second)
			}
			assertGateWorktreeIgnored(t, root)
			if withheld[name] {
				assertPrivateRootsIgnored(t, root, ".workingdir/OPEN.md")
				if _, err := util.RunGit(t.Context(), root, "check-ignore", "--no-index", "--", ".workingdir2/evidence/kept.md"); err == nil {
					t.Fatal("a legacy scratch root the repository un-ignores is still ignored")
				}
			} else {
				assertPrivateRootsIgnored(t, root, ".workingdir/OPEN.md", ".workingdir2/evidence/private.md")
			}
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

func assertPrivateRootsIgnored(t *testing.T, root string, paths ...string) {
	t.Helper()
	for _, private := range paths {
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
