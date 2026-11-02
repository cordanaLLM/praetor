// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// readGitHooksGuide reads docs/guides/git-hooks.md from the repository root.
func readGitHooksGuide(t *testing.T) string {
	t.Helper()
	repoRoot := filepath.Join("..", "..")
	path := filepath.Join(repoRoot, "docs", "guides", "git-hooks.md")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read git-hooks.md: %v", err)
	}
	return string(data)
}

// Positive: git-hooks.md documents shared stage budget for pre-commit, pre-push, post-commit,
// documents fixed 30s timeouts for commit-msg CLI verification commands, and clarifies All four Make invocations.
func TestDocsGitHooksStageBudget_Positive_DocumentsSharedBudgetStagesAndExcludesCompleteCommitMsg(t *testing.T) {
	content := readGitHooksGuide(t)
	mustHave := []string{
		"`pre-commit`, `pre-push`",
		"`post-commit`) execute under a shared stage budget",
		"In `commit-msg`, only the live-state",
		"audit runs through `cli`",
		"`state sync --verify .` and `forge check-message`",
		"fixed 30-second timeouts",
		"All four Make invocations take the program from `make_program`",
	}
	for _, want := range mustHave {
		if !strings.Contains(content, want) {
			t.Errorf("git-hooks.md missing expected documentation: %q", want)
		}
	}
}

// Negative: git-hooks.md must not claim that the complete commit-msg stage runs under the shared stage budget.
func TestDocsGitHooksStageBudget_Negative_RejectsUnqualifiedCommitMsgStageBudgetClaim(t *testing.T) {
	content := readGitHooksGuide(t)
	forbidden := "Every hook stage that builds or runs the repository's CLI (`pre-commit`, `commit-msg`,\n`pre-push`, `post-commit`) executes under a shared stage budget."
	if strings.Contains(content, forbidden) {
		t.Errorf("git-hooks.md contains inaccurate claim that complete commit-msg stage uses shared budget: %q", forbidden)
	}
}

// Boundary: verifies both the default 20m stage budget and fixed 30-second timeouts are documented.
func TestDocsGitHooksStageBudget_Boundary_VerifiesFixedThirtySecondTimeoutsDocumented(t *testing.T) {
	content := readGitHooksGuide(t)
	if !strings.Contains(content, "20 minutes (`20m`)") {
		t.Errorf("git-hooks.md missing default stage budget 20m")
	}
	if !strings.Contains(content, "PRAETOR_TEST_STAGE_TIMEOUT") {
		t.Errorf("git-hooks.md missing PRAETOR_TEST_STAGE_TIMEOUT")
	}
	if !strings.Contains(content, "fixed 30-second timeouts") {
		t.Errorf("git-hooks.md missing fixed 30-second timeouts for commit-msg commands")
	}
}
