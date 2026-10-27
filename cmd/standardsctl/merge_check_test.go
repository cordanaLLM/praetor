// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/testsupport"
)

// Order: engine-dropped-hunk-gate-20261027.
func TestMergeCheckCommand_NoMerges(t *testing.T) {
	dir := t.TempDir()
	testsupport.InitGitRepoWithOrigin(t, dir, "")
	testsupport.RunFixtureGit(t, dir,
		[]string{"config", "user.name", "Test"},
		[]string{"config", "user.email", "test@example.com"},
		[]string{"config", "init.defaultBranch", "main"},
		[]string{"config", "commit.gpgsign", "false"},
	)
	writeMergeFile(t, dir, "a.txt", "one\n")
	testsupport.RunFixtureGit(t, dir, []string{"add", "a.txt"}, []string{"commit", "-m", "chore: one"}, []string{"branch", "-M", "main"})

	writeMergeFile(t, dir, "a.txt", "two\n")
	testsupport.RunFixtureGit(t, dir, []string{"add", "a.txt"}, []string{"commit", "-m", "feat: two"})

	stdout, err := captureStdout(t, func() error {
		return dispatchCommandFrom(context.Background(), "merge-check", []string{"--dir=" + dir, "--range=main~1..main"})
	})
	if err != nil {
		t.Fatalf("expected nil error on clean range without merges, got: %v", err)
	}
	if !strings.Contains(stdout, "no merge commits in range") {
		t.Errorf("expected 'no merge commits in range', got %q", stdout)
	}
}

// Order: engine-dropped-hunk-gate-20261027.
func TestMergeCheckCommand_DroppedHunkFails(t *testing.T) {
	dir := t.TempDir()
	testsupport.InitGitRepoWithOrigin(t, dir, "")
	testsupport.RunFixtureGit(t, dir,
		[]string{"config", "user.name", "Test"},
		[]string{"config", "user.email", "test@example.com"},
		[]string{"config", "init.defaultBranch", "main"},
		[]string{"config", "commit.gpgsign", "false"},
	)
	writeMergeFile(t, dir, "alpha.txt", "line 1\nline 2\nline 3\nline 4\nline 5\n")
	testsupport.RunFixtureGit(t, dir, []string{"add", "alpha.txt"}, []string{"commit", "-m", "base"}, []string{"branch", "-M", "main"})

	// Branch X
	testsupport.RunFixtureGit(t, dir, []string{"checkout", "-b", "branch-x"})
	writeMergeFile(t, dir, "alpha.txt", "line 1\nline 2 modified by X\nline 3\nline 4\nline 5\n")
	testsupport.RunFixtureGit(t, dir, []string{"add", "alpha.txt"}, []string{"commit", "-m", "feat: X"})

	// Branch Y
	testsupport.RunFixtureGit(t, dir, []string{"checkout", "-b", "branch-y", "main"})
	writeMergeFile(t, dir, "alpha.txt", "line 1\nline 2\nline 3\nline 4\nline 5 modified by Y\n")
	testsupport.RunFixtureGit(t, dir, []string{"add", "alpha.txt"}, []string{"commit", "-m", "feat: Y"})

	// Merge taking Y whole file
	testsupport.RunFixtureGit(t, dir, []string{"checkout", "branch-x"})
	testsupport.RunFixtureGit(t, dir, []string{"merge", "--no-commit", "branch-y"})
	testsupport.RunFixtureGit(t, dir,
		[]string{"checkout", "branch-y", "--", "alpha.txt"},
		[]string{"add", "alpha.txt"},
		[]string{"commit", "-m", "merge: bad resolution"},
	)

	stdout, err := captureStdout(t, func() error {
		return dispatchCommandFrom(context.Background(), "merge-check", []string{"--dir=" + dir, "--range=main..HEAD"})
	})
	if err == nil {
		t.Fatalf("expected error from merge-check with dropped hunk, got nil")
	}
	if !strings.Contains(stdout, "alpha.txt") {
		t.Errorf("output missing alpha.txt: %s", stdout)
	}
	if !strings.Contains(stdout, "[FAIL] merge") {
		t.Errorf("output missing [FAIL] merge: %s", stdout)
	}
}

// Boundary: unrelated histories -> stated line, exit 0.
// Order: engine-dropped-hunk-gate-20261027.
func TestMergeCheckCommand_UnrelatedHistories_StatedLine(t *testing.T) {
	dir := t.TempDir()
	testsupport.InitGitRepoWithOrigin(t, dir, "")
	testsupport.RunFixtureGit(t, dir,
		[]string{"config", "user.name", "Test"},
		[]string{"config", "user.email", "test@example.com"},
		[]string{"config", "init.defaultBranch", "main"},
		[]string{"config", "commit.gpgsign", "false"},
	)
	writeMergeFile(t, dir, "a.txt", "one\n")
	testsupport.RunFixtureGit(t, dir, []string{"add", "a.txt"}, []string{"commit", "-m", "chore: one"}, []string{"branch", "-M", "main"})

	// Orphan branch with unrelated history
	testsupport.RunFixtureGit(t, dir, []string{"checkout", "--orphan", "other"})
	testsupport.RunFixtureGit(t, dir, []string{"rm", "-f", "a.txt"})
	writeMergeFile(t, dir, "b.txt", "two\n")
	testsupport.RunFixtureGit(t, dir, []string{"add", "b.txt"}, []string{"commit", "-m", "chore: two"})

	// Merge into main with --allow-unrelated-histories
	testsupport.RunFixtureGit(t, dir, []string{"checkout", "main"})
	testsupport.RunFixtureGit(t, dir, []string{"merge", "--allow-unrelated-histories", "--no-edit", "other"})

	stdout, err := captureStdout(t, func() error {
		return dispatchCommandFrom(context.Background(), "merge-check", []string{"--dir=" + dir, "--range=main~1..main"})
	})
	if err != nil {
		t.Fatalf("expected nil error on unrelated histories, got: %v", err)
	}
	if !strings.Contains(stdout, "no merge base, not judged") {
		t.Errorf("expected 'no merge base, not judged', got %q", stdout)
	}
	if strings.Contains(stdout, "[FAIL]") {
		t.Errorf("unexpected [FAIL] in output: %s", stdout)
	}
}

func writeMergeFile(t *testing.T, dir, rel, content string) {
	t.Helper()
	path := filepath.Join(dir, filepath.FromSlash(rel))
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
