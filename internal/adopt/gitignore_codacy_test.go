// SPDX-FileCopyrightText: 2026 Lusoris <lusoris@pm.me>
// SPDX-License-Identifier: EUPL-1.2

package adopt

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/util"
)

// codacyExtensionAppends stubs the Codacy IDE extension's createOrUpdateRules logic:
// it appends the rule unless some .gitignore line equals relativePath after trim.
func codacyExtensionAppends(gitIgnoreContent, relativePath string) bool {
	lines := strings.Split(gitIgnoreContent, "\n")
	const maxLines = 1024
	for i := 0; i < len(lines) && i < maxLines; i++ {
		if strings.TrimSpace(lines[i]) == relativePath {
			return false
		}
	}
	return true
}

// Positive: the rendered v5 block holds the three Codacy rules exactly, and the Codacy
// extension's check finds .github/instructions/codacy.instructions.md without appending.
func TestCodacyIgnoreBlockRules_Positive(t *testing.T) {
	block := canonicalGitIgnoreBlockVersion.Render(false, false)

	const (
		wantCli          = "/.codacy/"
		wantInstructions = ".github/instructions/codacy.instructions.md"
		wantCursor       = ".cursor/rules/codacy.mdc"
	)

	for _, want := range []string{wantCli, wantInstructions, wantCursor} {
		if !strings.Contains(block, "\n"+want+"\n") {
			t.Fatalf("v5 managed block missing rule %q:\n%s", want, block)
		}
	}

	if codacyExtensionAppends(block, wantInstructions) {
		t.Fatalf("extension stub wanted to append %s to canonical block:\n%s", wantInstructions, block)
	}
}

// Negative: with a leading slash on the instructions rule, the extension stub cannot find
// the line and appends it (the defect that dirtied checkouts).
func TestCodacyIgnoreBlockRules_Negative(t *testing.T) {
	blockWithLeadingSlash := gitIgnoreManagedBegin + "\n/.github/instructions/codacy.instructions.md\n" + gitIgnoreManagedEnd + "\n"
	if !codacyExtensionAppends(blockWithLeadingSlash, "something-else") {
		t.Fatal("extension stub did not append for absent rule")
	}
	if !codacyExtensionAppends(blockWithLeadingSlash, ".github/instructions/codacy.instructions.md") {
		t.Fatal("extension stub should append when rule has leading slash (anchored)")
	}
}

// Positive: git check-ignore in a repository with the v5 block ignores the Codacy files
// and does not ignore compiled vendor files.
func TestCodacyGitCheckIgnore_Positive(t *testing.T) {
	root := newTestRepo(t, "codacy-check-ignore")
	mustWrite(t, filepath.Join(root, ".gitignore"), ManagedGitIgnoreBlock())

	ignored := []string{
		".codacy/codacy.yaml",
		".codacy/tools-configs/x",
		".github/instructions/codacy.instructions.md",
		".cursor/rules/codacy.mdc",
	}
	for i := 0; i < len(ignored) && i < 16; i++ {
		if _, err := util.RunGit(t.Context(), root, "check-ignore", "--no-index", "--", ignored[i]); err != nil {
			t.Fatalf("path %s must be ignored by v5 block: %v", ignored[i], err)
		}
	}

	notIgnored := []string{
		".github/copilot-instructions.md",
		".cursor/rules/hiss-invariants.mdc",
	}
	for i := 0; i < len(notIgnored) && i < 16; i++ {
		if _, err := util.RunGit(t.Context(), root, "check-ignore", "--no-index", "--", notIgnored[i]); err == nil {
			t.Fatalf("path %s must NOT be ignored by v5 block", notIgnored[i])
		}
	}
}

// Positive: a repository carrying the v4 block is recognized as historical and upgrades to v5.
func TestGitIgnoreV4HistoryUpgrade_Positive(t *testing.T) {
	root := newTestRepo(t, "v4-upgrade")
	v4Block := gitIgnoreBlockHistory[len(gitIgnoreBlockHistory)-1].Render(false, false)
	gitIgnorePath := filepath.Join(root, ".gitignore")
	mustWrite(t, gitIgnorePath, v4Block)

	match, ok := LookupManagedGitIgnoreTail(t.Context(), root, v4Block)
	if !ok || match.Current || match.Version != "v4" {
		t.Fatalf("LookupManagedGitIgnoreTail(v4) = %+v, ok=%v; want Current=false, Version=v4", match, ok)
	}

	merged, err := mergeGitIgnore(v4Block)
	if err != nil {
		t.Fatalf("mergeGitIgnore(v4) failed: %v", err)
	}
	matchV5, okV5 := LookupManagedGitIgnoreTail(t.Context(), root, merged)
	if !okV5 || !matchV5.Current || matchV5.Version != "v5" {
		t.Fatalf("LookupManagedGitIgnoreTail(merged) = %+v, ok=%v; want Current=true, Version=v5", matchV5, okV5)
	}
}
