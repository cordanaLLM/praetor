// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package mergecheck

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/testsupport"
	"github.com/cordanaLLM/praetor/internal/util"
)

// initTestRepo creates and configures a temporary git repository for merge-check tests.
func initTestRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	testsupport.InitGitRepoWithOrigin(t, dir, "")
	testsupport.RunFixtureGit(t, dir,
		[]string{"config", "user.name", "Praetor Test"},
		[]string{"config", "user.email", "praetor-test@example.com"},
		[]string{"config", "init.defaultBranch", "main"},
		[]string{"config", "commit.gpgsign", "false"},
	)
	return dir
}

// assertFindingMatch verifies that a Finding matches expected path, merge commit, and parent hashes.
func assertFindingMatch(t *testing.T, f Finding, expectedPath, expectedMerge, expectedP1, expectedP2 string) {
	t.Helper()
	if f.FilePath != expectedPath {
		t.Errorf("expected finding for %s, got %s", expectedPath, f.FilePath)
	}
	if f.MergeSHA != expectedMerge {
		t.Errorf("expected merge SHA %s, got %s", expectedMerge, f.MergeSHA)
	}
	if f.Parent1 != expectedP1 || f.Parent2 != expectedP2 {
		t.Errorf("expected parents (%s, %s), got (%s, %s)", expectedP1, expectedP2, f.Parent1, f.Parent2)
	}
	formatted := FormatFinding(f)
	if !strings.Contains(formatted, expectedPath) {
		t.Errorf("formatted finding lacks %s: %s", expectedPath, formatted)
	}
	if !strings.Contains(formatted, shortSHA(expectedMerge)) {
		t.Errorf("formatted finding lacks merge sha12: %s", formatted)
	}
	if !strings.Contains(formatted, shortSHA(expectedP1)) || !strings.Contains(formatted, shortSHA(expectedP2)) {
		t.Errorf("formatted finding lacks parent shas: %s", formatted)
	}
}

// writeFile creates parent directories and writes content to a relative path in dir.
func writeFile(t *testing.T, dir, rel, content string) {
	t.Helper()
	path := filepath.Join(dir, filepath.FromSlash(rel))
	// nosemgrep: go.lang.correctness.permissions.file_permission.incorrect-default-permission -- a directory needs the owner search bit to hold entries
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

// gitRevParse resolves a git revision string to a full commit object ID.
func gitRevParse(t *testing.T, dir, ref string) string {
	t.Helper()
	return strings.TrimSpace(testsupport.RunFixtureGit(t, dir, []string{"rev-parse", ref}))
}

// runGitAllowConflict executes a git command in dir, ignoring any expected merge conflict errors.
func runGitAllowConflict(t *testing.T, dir string, args ...string) {
	t.Helper()
	if _, err := util.RunGit(t.Context(), dir, args...); err != nil {
		t.Logf("ignoring expected merge conflict: %v", err)
	}
}
