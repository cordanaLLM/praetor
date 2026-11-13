// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package state

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func archiveWorktreeFixture(t *testing.T) (string, string) {
	t.Helper()
	root := t.TempDir()
	stateFixtureGit(t, root, "init")
	stateFixtureGit(t, root, "commit", "--allow-empty", "-m", "fixture")
	linked := filepath.Join(t.TempDir(), "linked worktree")
	stateFixtureGit(t, root, "worktree", "add", "-b", "fixture/linked", linked)
	if err := InitWorkingDir(root); err != nil {
		t.Fatal(err)
	}
	return root, linked
}

func TestArchiveLiveWorktree(t *testing.T) {
	for _, marker := range []string{"- [x]", "| DONE", "- [DONE"} {
		t.Run(marker, func(t *testing.T) {
			root, linked := archiveWorktreeFixture(t)
			old := fmt.Sprintf("%s id: old | path: %s | branch: old | last: before | next: old\n", marker, linked)
			newest := fmt.Sprintf("%s id: live | path: %s | branch: fixture/linked | last: shipped | next: cleanup\n", marker, linked)
			open := filepath.Join(root, WorkingDirName, "OPEN.md")
			writeIntegrityFile(t, open, old+newest+"- [x] unrelated\n")
			count, err := ArchiveCompletedTasks(root, "fixture")
			if err != nil || count != 2 {
				t.Fatalf("archive = %d, %v; want two removed rows", count, err)
			}
			assertIntegrityFile(t, open, newest)
			backlog, err := os.ReadFile(filepath.Join(root, WorkingDirName, "BACKLOG.md"))
			if err != nil || !strings.Contains(string(backlog), old) || !strings.Contains(string(backlog), "unrelated") {
				t.Fatalf("removed records missing from backlog: %s, %v", backlog, err)
			}
			count, err = ArchiveCompletedTasks(root, "fixture")
			if err != nil || count != 0 {
				t.Fatalf("repeat archive = %d, %v", count, err)
			}
			assertIntegrityFile(t, open, newest)
		})
	}
}

func TestArchiveWorktreeExistingRow(t *testing.T) {
	root, linked := archiveWorktreeFixture(t)
	kept := fmt.Sprintf("- [ ] id: live | path: %s | branch: fixture/linked | last: shipped | next: cleanup\n", linked)
	done := fmt.Sprintf("- [x] id: old | path: %s | branch: old | last: before | next: old\n", linked)
	open := filepath.Join(root, WorkingDirName, "OPEN.md")
	writeIntegrityFile(t, open, done+kept)
	if count, err := ArchiveCompletedTasks(root, "fixture"); err != nil || count != 1 {
		t.Fatalf("archive = %d, %v", count, err)
	}
	assertIntegrityFile(t, open, kept)
}

func TestArchiveRemovedWorktree(t *testing.T) {
	root, linked := archiveWorktreeFixture(t)
	stateFixtureGit(t, root, "worktree", "remove", linked)
	open := filepath.Join(root, WorkingDirName, "OPEN.md")
	writeIntegrityFile(t, open, fmt.Sprintf("- [x] id: gone | path: %s | branch: fixture/linked | last: done | next: none\n", linked))
	if count, err := ArchiveCompletedTasks(root, "fixture"); err != nil || count != 1 {
		t.Fatalf("archive = %d, %v", count, err)
	}
	assertIntegrityFile(t, open, "")
}

func TestArchiveNoDoneRowsUnchanged(t *testing.T) {
	const content = "# Open\n- [ ] id: active | path: absent | branch: fixture | last: editing | next: test\n"
	root := t.TempDir()
	stateFixtureGit(t, root, "init")
	if err := InitWorkingDir(root); err != nil {
		t.Fatal(err)
	}
	open := filepath.Join(root, WorkingDirName, "OPEN.md")
	writeIntegrityFile(t, open, content)
	if count, err := ArchiveCompletedTasks(root, "fixture"); err != nil || count != 0 {
		t.Fatalf("archive = %d, %v", count, err)
	}
	assertIntegrityFile(t, open, content)
}

func TestArchiveFencedWorktreeRows(t *testing.T) {
	root, linked := archiveWorktreeFixture(t)
	fenced := fmt.Sprintf("```\n- [ ] example %s\n| DONE example |\n- [DONE example]\n```\n", linked)
	retained := fmt.Sprintf("- [x] id: live | path: %s | branch: fixture/linked | last: shipped | next: cleanup\n", linked)
	open := filepath.Join(root, WorkingDirName, "OPEN.md")
	writeIntegrityFile(t, open, fenced+retained+"- [x] other\n")
	if count, err := ArchiveCompletedTasks(root, "fixture"); err != nil || count != 1 {
		t.Fatalf("archive = %d, %v", count, err)
	}
	assertIntegrityFile(t, open, fenced+retained)
}

func TestArchiveMissingRegisteredWorktree(t *testing.T) {
	root, linked := archiveWorktreeFixture(t)
	if err := os.RemoveAll(linked); err != nil {
		t.Fatal(err)
	}
	open := filepath.Join(root, WorkingDirName, "OPEN.md")
	writeIntegrityFile(t, open, fmt.Sprintf("- [x] gone %s\n", linked))
	if count, err := ArchiveCompletedTasks(root, "fixture"); err != nil || count != 1 {
		t.Fatalf("archive = %d, %v", count, err)
	}
	assertIntegrityFile(t, open, "")
}

func TestArchiveGitFailurePreservesRows(t *testing.T) {
	root, linked := archiveWorktreeFixture(t)
	open := filepath.Join(root, WorkingDirName, "OPEN.md")
	content := fmt.Sprintf("- [x] retain %s\n", linked)
	writeIntegrityFile(t, open, content)
	writeIntegrityFile(t, filepath.Join(root, ".git", "config"), "[broken\n")
	if count, err := ArchiveCompletedTasks(root, "fixture"); err == nil || count != 0 {
		t.Fatalf("archive accepted broken Git metadata: %d, %v", count, err)
	}
	assertIntegrityFile(t, open, content)
}

func TestNamesWorktreePath(t *testing.T) {
	const path = "/repo/linked worktree"
	for _, tc := range []struct {
		row  string
		want bool
	}{
		{"path: `" + path + "` | branch: topic", true},
		{"path: " + path, true},
		{"path: " + path + "-sibling", false},
		{"path: " + path + "/child", false},
		{"path: /prefix" + path, false},
		{"path: " + path + "-sibling | previous: " + path, true},
		{"nothing", false},
	} {
		if got := namesWorktreePath(tc.row, path); got != tc.want {
			t.Errorf("namesWorktreePath(%q) = %v; want %v", tc.row, got, tc.want)
		}
	}
}
