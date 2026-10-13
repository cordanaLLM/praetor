// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package adopt

import (
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/hiss"
	"github.com/cordanaLLM/praetor/internal/util"
)

// nestedPrivatePaths are private state files below a nested tool's directory, where the
// root-anchored rules of blocks up to v2 did not reach (#1263).
var nestedPrivatePaths = []string{
	"tooling/.workingdir/notes.md",
	"tooling/clrs-generator/.workingdir/draft.md",
	"a/b/.workingdir2/scratch.md",
}

// nestedPublicPaths sit beside them and stay the repository's own.
var nestedPublicPaths = []string{
	"tooling/README.md",
	"tooling/.workingdir-notes/kept.md",
	"tooling/workingdir/kept.md",
}

// managedIgnoreRepo returns a Git work tree whose .gitignore is the managed block merged into
// an operator line, as adoption writes it.
func managedIgnoreRepo(t *testing.T) string {
	t.Helper()
	repoPath := t.TempDir()
	initTestGit(t, repoPath)
	merged, err := mergeGitIgnore("node_modules/\n")
	if err != nil {
		t.Fatal(err)
	}
	mustWrite(t, filepath.Join(repoPath, gitIgnoreFile), merged)
	return repoPath
}

// Positive: git check-ignore answers the private state of a nested tool as ignored, at every
// depth and for both private roots, and the root ledger as before.
func TestManagedGitIgnore_Positive_NestedPrivateStateIgnored(t *testing.T) {
	repoPath := managedIgnoreRepo(t)
	probes := append([]string{".workingdir/root.md", ".workingdir2/root.md"}, nestedPrivatePaths...)
	ignored, err := util.GitIgnoredPaths(t.Context(), repoPath, probes, true)
	if err != nil {
		t.Fatal(err)
	}
	for _, rel := range probes {
		if !slices.Contains(ignored, rel) {
			t.Errorf("git does not ignore %s under the managed block", rel)
		}
	}
	for _, rel := range nestedPrivatePaths {
		if !privateByDesign(rel) {
			t.Errorf("privateByDesign(%q) = false; adoption would count a write there as committable", rel)
		}
	}
}

// Negative: a planted nested private file stays out of the file lists the gates read from Git:
// the HISS scan's visible tree and the untracked listing a documentation lint walks. The files
// beside it, whose names only resemble the private roots, stay in both.
func TestManagedGitIgnore_Negative_NestedPrivateFileStaysOutOfTheGates(t *testing.T) {
	repoPath := managedIgnoreRepo(t)
	for _, rel := range append(slices.Clone(nestedPrivatePaths), nestedPublicPaths...) {
		mustWrite(t, filepath.Join(repoPath, filepath.FromSlash(rel)), "# planted\n")
	}
	tree := hiss.GitVisiblePaths(t.Context(), repoPath)
	if tree == nil {
		t.Fatal("git listed no visible paths")
	}
	listing, err := util.RunGitProbe(t.Context(), repoPath, 1<<20, "ls-files", "--others", "--exclude-standard", "-z")
	if err != nil {
		t.Fatal(err)
	}
	untracked := strings.Split(strings.TrimSuffix(string(listing.Stdout), "\x00"), "\x00")
	for _, rel := range nestedPrivatePaths {
		if tree.HasFile(rel) || slices.Contains(untracked, rel) {
			t.Errorf("%s reaches the gates: visible=%t untracked=%t", rel, tree.HasFile(rel), slices.Contains(untracked, rel))
		}
	}
	for _, rel := range nestedPublicPaths {
		if !tree.HasFile(rel) || !slices.Contains(untracked, rel) {
			t.Errorf("%s was hidden from the gates, but is no private root", rel)
		}
	}
}

// Boundary: a root-anchored v2 block, in LF and CRLF, is replaced by the current block in its
// own line endings and reads as current afterwards; an operator's unanchored rule for a private
// root is not repeated, and the operator's other lines stay in order.
func TestManagedGitIgnore_Boundary_V2BlockUpgradedWithoutDuplicates(t *testing.T) {
	root := t.TempDir()
	var v2 gitIgnoreBlockVersion
	for _, version := range gitIgnoreBlockHistory {
		if version.Version == "v2" {
			v2 = version
		}
	}
	if v2.Version == "" || !slices.Contains(v2.Rules, "/.workingdir/") {
		t.Fatal("no root-anchored v2 block in gitIgnoreBlockHistory")
	}
	old := "dist/\n\n" + v2.Render(true, false)
	if match, ok := LookupManagedGitIgnoreTail(root, old); !ok || match.Current || match.Version != "v2" {
		t.Fatalf("the v2 block reads as %+v, want a known, outdated v2", match)
	}
	for style, text := range map[string]string{"lf": old, "crlf": strings.ReplaceAll(old, "\n", "\r\n")} {
		merged, err := mergeGitIgnore(text)
		if err != nil {
			t.Fatal(err)
		}
		want := "dist/\n\n" + ManagedGitIgnoreBlock()
		if style == "crlf" {
			want = strings.ReplaceAll(want, "\n", "\r\n")
		}
		if merged != want {
			t.Errorf("%s: the v2 block was not replaced by the current one:\n%q", style, merged)
		}
		if match, ok := LookupManagedGitIgnoreTail(root, merged); !ok || !match.Current {
			t.Errorf("%s: the upgraded block reads as %+v, want current", style, match)
		}
	}

	operator := "node_modules/\n.workingdir/\ncoverage/\n.workingdir2/\n"
	merged, err := mergeGitIgnore(operator)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(merged, "\n")
	for _, rule := range []string{workingDirIgnore, legacyScratchIgnore} {
		if count := strings.Count("\n"+merged, "\n"+rule+"\n"); count != 1 {
			t.Errorf("%s appears %d times after the merge:\n%s", rule, count, merged)
		}
	}
	if !slices.Equal(lines[:3], []string{"node_modules/", "coverage/", ""}) {
		t.Errorf("the operator's other lines were not kept in order:\n%s", merged)
	}
}
