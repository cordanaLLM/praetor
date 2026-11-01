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

func writeFile(t *testing.T, dir, rel, content string) {
	t.Helper()
	path := filepath.Join(dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func gitRevParse(t *testing.T, dir, ref string) string {
	t.Helper()
	return strings.TrimSpace(testsupport.RunFixtureGit(t, dir, []string{"rev-parse", ref}))
}

func runGitAllowConflict(t *testing.T, dir string, args ...string) {
	t.Helper()
	if _, err := util.RunGit(t.Context(), dir, args...); err != nil {
		t.Logf("ignoring expected merge conflict: %v", err)
	}
}
