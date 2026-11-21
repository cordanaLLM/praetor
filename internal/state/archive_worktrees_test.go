// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package state

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/cordanaLLM/praetor/internal/contextopt"
	"github.com/cordanaLLM/praetor/internal/testsupport"
	"github.com/cordanaLLM/praetor/internal/worktree"
)

// archiveWorktreeFixture creates a repository with one linked worktree whose path holds a space.
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

// TestArchiveLiveWorktree keeps one row for a live worktree under each done marker and archives the rest.
func TestArchiveLiveWorktree(t *testing.T) {
	for _, marker := range []string{"- [x]", "| DONE |", "- [DONE — fixture]"} {
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
			backlog, err := contextopt.ReadSnapshot(t.Context(), filepath.Join(root, WorkingDirName, "BACKLOG.md"))
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

// TestArchiveLeadingSpaceWorktree retains a rooted field under every completion marker.
func TestArchiveLeadingSpaceWorktree(t *testing.T) {
	for _, format := range []string{
		"- [x] %s\n", "- [X] %s\n", "| DONE | %s |\n", "| DONE — fixture | %s |\n",
		"[DONE] %s\n", "[DONE fixture] %s\n", "[DONE — fixture] %s\n",
		"- [DONE] %s\n", "- [DONE fixture] %s\n", "- [DONE — fixture] %s\n",
	} {
		t.Run(format, func(t *testing.T) {
			root := t.TempDir()
			if err := InitWorkingDir(root); err != nil {
				t.Fatal(err)
			}
			path := filepath.FromSlash("/ leading worktree")
			ops := archiveWorktreeOps{
				list: func(context.Context, string) ([]worktree.WorktreeInfo, error) {
					return []worktree.WorktreeInfo{{Path: path}}, nil
				},
				inspect: func(_ context.Context, name string) (archivePath, error) {
					return archivePath{path: name, exists: name == path}, nil
				},
			}
			row := fmt.Sprintf(format, "/ leading worktree")
			open := filepath.Join(root, WorkingDirName, openLedgerName)
			writeIntegrityFile(t, open, row+"- [x] unrelated\n")
			if count, err := archiveCompletedTasks(t.Context(), root, "fixture", ops); err != nil || count != 1 {
				t.Fatalf("leading-space archive = %d, %v; want one removed row", count, err)
			}
			assertIntegrityFile(t, open, row)
		})
	}
}

// TestArchiveWorktreeExistingRow prefers an unarchived row that already names the live worktree.
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

// TestArchiveRemovedWorktree archives a done row whose worktree was removed.
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

// TestArchiveNoDoneRowsUnchanged leaves an OPEN.md without done rows byte for byte.
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

// TestArchiveFencedWorktreeRows ignores rows inside code fences when counting done rows.
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

// TestArchiveMissingRegisteredWorktree archives a row whose registered worktree no longer exists on disk.
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

// TestArchiveGitFailurePreservesRows refuses to archive when the worktree list cannot be read.
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

// TestNamesWorktreePath matches whole path tokens and rejects siblings, children and prefixes.
func TestNamesWorktreePath(t *testing.T) {
	const path = "/repo/linked worktree"
	for _, tc := range []struct {
		row  string
		want bool
	}{
		{"path: `" + path + "` | branch: topic", true},
		{"path: " + path, true},
		{"path:" + path, true},
		{"branch: feature" + path, false},
		{"url: https:/" + path, false},
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

// TestArchiveEscapedPipeWorktree retains a live path recorded with an escaped table delimiter.
func TestArchiveEscapedPipeWorktree(t *testing.T) {
	for _, wrapper := range []string{"%s", "`%s`", "\"%s\"", "(%s)", "[%s]"} {
		t.Run(wrapper, func(t *testing.T) {
			root := t.TempDir()
			if err := InitWorkingDir(root); err != nil {
				t.Fatal(err)
			}
			path := filepath.FromSlash("/repo/a|b")
			ops := defaultArchiveWorktreeOps()
			ops.list = func(context.Context, string) ([]worktree.WorktreeInfo, error) {
				return []worktree.WorktreeInfo{{Path: path}}, nil
			}
			ops.inspect = func(_ context.Context, field string) (archivePath, error) {
				return archivePath{path: field, exists: field == path}, nil
			}
			kept := "| DONE | path: " + fmt.Sprintf(wrapper, `/repo/a\|b`) + " |\n"
			open := filepath.Join(root, WorkingDirName, openLedgerName)
			writeIntegrityFile(t, open, kept+"- [x] unrelated\n")
			if count, err := archiveCompletedTasks(t.Context(), root, "fixture", ops); err != nil || count != 1 {
				t.Fatalf("escaped-pipe archive = %d, %v", count, err)
			}
			assertIntegrityFile(t, open, kept)
		})
	}
}

// TestArchiveTableHeaderNotRetained keeps the completed task row of a live worktree when the
// table header repeats the worktree path: headers and separators are never task rows.
func TestArchiveTableHeaderNotRetained(t *testing.T) {
	root := t.TempDir()
	if err := InitWorkingDir(root); err != nil {
		t.Fatal(err)
	}
	path := filepath.FromSlash("/repo/topic")
	ops := defaultArchiveWorktreeOps()
	ops.list = func(context.Context, string) ([]worktree.WorktreeInfo, error) {
		return []worktree.WorktreeInfo{{Path: path}}, nil
	}
	ops.inspect = func(_ context.Context, field string) (archivePath, error) {
		return archivePath{path: field, exists: field == path}, nil
	}
	table := "| /repo/topic | Status |\n| --- | --- |\n| /repo/topic | DONE |\n"
	open := filepath.Join(root, WorkingDirName, openLedgerName)
	writeIntegrityFile(t, open, table+"- [x] unrelated\n")
	if count, err := archiveCompletedTasks(t.Context(), root, "fixture", ops); err != nil || count != 1 {
		t.Fatalf("header archive = %d, %v; want only the unrelated row removed", count, err)
	}
	assertIntegrityFile(t, open, table)
}

// TestArchiveStructuralMarkers refuses completion words outside their status position.
func TestArchiveStructuralMarkers(t *testing.T) {
	root := t.TempDir()
	if err := InitWorkingDir(root); err != nil {
		t.Fatal(err)
	}
	pending := "- [ ] explain [DONE later and | DONE later\n" +
		"Prose mentions [DONE and | DONE\n" +
		"- [ ] example `| DONE`\n" +
		"| Status | Description |\n" +
		"| --- | --- |\n" +
		"| TODO | DONE |\n" +
		"| TODO | `[DONE]` |\n" +
		"| `DONE` | code example |\n" +
		"| DONEish | boundary |\n" +
		"| Item | Description | Status |\n| --- | --- | --- |\n" +
		"| pending | example `| DONE | demo` | TODO |\n"
	done := "| shipped | real task | DONE |\n| shipped | real task | DONE — shipped |\n- [DONE — fixture] bracket task\n"
	open := filepath.Join(root, WorkingDirName, openLedgerName)
	writeIntegrityFile(t, open, pending+done)
	if count, err := ArchiveCompletedTasks(root, "fixture"); err != nil || count != 3 {
		t.Fatalf("archive = %d, %v; want three structural markers", count, err)
	}
	assertIntegrityFile(t, open, pending)
}

// TestArchiveEscapedPipeStatus keeps escaped description pipes out of the status column.
func TestArchiveEscapedPipeStatus(t *testing.T) {
	root := t.TempDir()
	if err := InitWorkingDir(root); err != nil {
		t.Fatal(err)
	}
	header := "| Item | Description | Status |\n| --- | --- | --- |\n"
	pending := "| task | text \\| DONE | TODO |\n"
	done := "| task | text \\| TODO | DONE |\n"
	// The header itself can contain an escaped pipe before Status, too.
	escapedHeader := "| Item \\| Label | Description | Status |\n| --- | --- | --- |\n"
	open := filepath.Join(root, WorkingDirName, openLedgerName)
	writeIntegrityFile(t, open, header+pending+done+escapedHeader+pending+done)
	if count, err := ArchiveCompletedTasks(root, "fixture"); err != nil || count != 2 {
		t.Fatalf("escaped pipe archive = %d, %v; want two real DONE rows", count, err)
	}
	assertIntegrityFile(t, open, header+pending+escapedHeader+pending)
}

// TestArchiveWorktreePathCharacters preserves literal suffixes while unwrapping matched pairs.
func TestArchiveWorktreePathCharacters(t *testing.T) {
	for _, tc := range []struct{ field, path string }{
		{"/repo/topic]", "/repo/topic]"}, {"/repo/(x)", "/repo/(x)"}, {"/repo/'q'", "/repo/'q'"},
		{"`/repo/topic`", "/repo/topic"}, {"\"/repo/topic\"", "/repo/topic"}, {"'/repo/topic'", "/repo/topic"},
		{"(/repo/topic)", "/repo/topic"}, {"[/repo/topic]", "/repo/topic"},
		{"(`/repo/topic`)", "/repo/topic"}, {"[ '/repo/topic]' ]", "/repo/topic]"},
	} {
		t.Run(tc.field, func(t *testing.T) {
			root := t.TempDir()
			if err := InitWorkingDir(root); err != nil {
				t.Fatal(err)
			}
			path := filepath.FromSlash(tc.path)
			ops := archiveWorktreeOps{
				list: func(context.Context, string) ([]worktree.WorktreeInfo, error) {
					return []worktree.WorktreeInfo{{Path: path}}, nil
				},
				inspect: func(_ context.Context, name string) (archivePath, error) {
					return archivePath{path: name, exists: name == path}, nil
				},
			}
			row := "- [x] path:  " + tc.field + "  | branch: fixture\n"
			open := filepath.Join(root, WorkingDirName, openLedgerName)
			writeIntegrityFile(t, open, row+"- [x] unrelated\n")
			if count, err := archiveCompletedTasks(t.Context(), root, "fixture", ops); err != nil || count != 1 {
				t.Fatalf("path character archive = %d, %v", count, err)
			}
			assertIntegrityFile(t, open, row)
		})
	}
}

// TestArchiveEquivalentPaths retains trailing separators and symlinked ancestor spellings.
func TestArchiveEquivalentPaths(t *testing.T) {
	root, linked := archiveWorktreeFixture(t)
	alias := filepath.Join(t.TempDir(), "alias")
	if err := os.Symlink(filepath.Dir(linked), alias); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	for _, path := range []string{linked + string(filepath.Separator), filepath.Join(alias, filepath.Base(linked))} {
		row := fmt.Sprintf("- [x] id: live | path: %s | branch: fixture\n", path)
		open := filepath.Join(root, WorkingDirName, openLedgerName)
		writeIntegrityFile(t, open, row)
		if count, err := ArchiveCompletedTasks(root, "fixture"); err != nil || count != 0 {
			t.Fatalf("archive equivalent %q = %d, %v", path, count, err)
		}
		assertIntegrityFile(t, open, row)
	}
}

// TestArchiveAliasLongSpelling retains the same live row verbatim through short and overlong aliases.
func TestArchiveAliasLongSpelling(t *testing.T) {
	root, linked := archiveWorktreeFixture(t)
	alias := filepath.Join(t.TempDir(), "alias")
	if err := os.Symlink(filepath.Dir(linked), alias); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	for _, repeats := range []int{1, 2048} {
		for _, format := range []string{"- [x] id: live | path: %s | next: pending commit\n", "| DONE | %s | next: pending commit |\n"} {
			t.Run(fmt.Sprintf("%d/%s", repeats, format), func(t *testing.T) {
				path := filepath.Join(alias, filepath.Base(linked)) + "/" + strings.Repeat("./", repeats)
				kept := fmt.Sprintf(format, path)
				open := filepath.Join(root, WorkingDirName, openLedgerName)
				writeIntegrityFile(t, open, kept+"- [x] unrelated\n")
				if count, err := ArchiveCompletedTasks(root, "fixture"); err != nil || count != 1 {
					t.Fatalf("alias archive = %d, %v; want one removed row", count, err)
				}
				assertIntegrityFile(t, open, kept)
			})
		}
	}
}

// TestArchiveLongSymlinkLoop preserves every row when the cleaned alias cannot be resolved.
func TestArchiveLongSymlinkLoop(t *testing.T) {
	root := t.TempDir()
	loop := filepath.Join(root, "loop")
	if err := os.Symlink(loop, loop); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	_, statErr := os.Stat(loop)
	if statErr == nil {
		t.Fatal("symlink loop stat unexpectedly succeeded")
	}
	testArchiveLongPathError(t, root, loop, statErr)
}

// testArchiveLongPathError checks fail-closed archive publication through a redundant spelling.
func testArchiveLongPathError(t *testing.T, root, path string, want error) {
	t.Helper()
	if err := InitWorkingDir(root); err != nil {
		t.Fatal(err)
	}
	body := "- [x] path: " + path + "/" + strings.Repeat("./", 2048) + " | next: pending commit\n- [x] unrelated\n"
	open := filepath.Join(root, WorkingDirName, openLedgerName)
	backlog := filepath.Join(root, WorkingDirName, "BACKLOG.md")
	writeIntegrityFile(t, open, body)
	writeIntegrityFile(t, backlog, "unchanged backlog\n")
	ops := defaultArchiveWorktreeOps()
	ops.list = func(context.Context, string) ([]worktree.WorktreeInfo, error) { return nil, nil }
	var pathErr *os.PathError
	if !errors.As(want, &pathErr) {
		t.Fatalf("expected native PathError: %v", want)
	}
	if count, err := archiveCompletedTasks(t.Context(), root, "fixture", ops); count != 0 || !errors.Is(err, pathErr.Err) {
		t.Fatalf("long spelling archive = %d, %v; want %v and no removed rows", count, err, pathErr.Err)
	}
	assertIntegrityFile(t, open, body)
	assertIntegrityFile(t, backlog, "unchanged backlog\n")
}

// requireArchivePathArgumentLimit skips pre-stat ceiling assertions on hosts where stat decides total length.
func requireArchivePathArgumentLimit(t *testing.T) int {
	t.Helper()
	limit := archivePathArgumentLimit()
	if limit == 0 {
		t.Skip("host has no verified pre-stat total ceiling; native stat decides")
	}
	return limit
}

// TestArchiveCleanedTotalLimit avoids inspection of an impossible whole path with no live worktree.
func TestArchiveCleanedTotalLimit(t *testing.T) {
	requireArchivePathArgumentLimit(t)
	root := t.TempDir()
	if err := InitWorkingDir(root); err != nil {
		t.Fatal(err)
	}
	path := "/" + strings.Repeat("a/", 17000) + "a"
	for _, format := range []string{"- [x] path: %s\n", "| DONE | %s |\n"} {
		open := filepath.Join(root, WorkingDirName, openLedgerName)
		writeIntegrityFile(t, open, fmt.Sprintf(format, path)+"- [ ] pending\n")
		calls := 0
		ops := archiveWorktreeOps{
			list: func(context.Context, string) ([]worktree.WorktreeInfo, error) { return nil, nil },
			inspect: func(context.Context, string) (archivePath, error) {
				calls++
				return archivePath{}, errors.New("impossible path reached stat")
			},
		}
		if count, err := archiveCompletedTasks(t.Context(), root, "fixture", ops); err != nil || count != 1 || calls != 0 {
			t.Fatalf("total limit archive = %d, %v, inspections = %d", count, err, calls)
		}
		assertIntegrityFile(t, open, "- [ ] pending\n")
	}
}

// TestArchiveWindowsSpellings exercises native case and current-drive root-relative canonicalization.
func TestArchiveWindowsSpellings(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows filesystem case and root-relative semantics require Windows")
	}
	root, linked := archiveWorktreeFixture(t)
	t.Chdir(root)
	for _, path := range []string{strings.ToUpper(linked), strings.ToLower(linked), strings.TrimPrefix(linked, filepath.VolumeName(linked))} {
		for _, repeats := range []int{1, 2048, archivePathArgumentLimit()/2 + 1} {
			for _, format := range []string{"- [x] path: %s | next: pending commit\n", "| DONE | %s | next: pending commit |\n"} {
				kept := fmt.Sprintf(format, path+"/"+strings.Repeat("./", repeats))
				open := filepath.Join(root, WorkingDirName, openLedgerName)
				writeIntegrityFile(t, open, kept+"- [x] unrelated\n")
				if count, err := ArchiveCompletedTasks(root, "fixture"); err != nil || count != 1 {
					t.Fatalf("Windows spelling archive = %d, %v", count, err)
				}
				assertIntegrityFile(t, open, kept)
			}
		}
	}
}

// TestArchiveWindowsNativeTooLong classifies the real Windows stat error for an oversized component.
func TestArchiveWindowsNativeTooLong(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("native filesystem error for an oversized component requires Windows")
	}
	root := t.TempDir()
	path := filepath.Join(root, strings.Repeat("a", 300))
	_, statErr := os.Stat(path)
	logArchiveNativeStat(t, statErr)
	if statErr == nil || !archivePathAbsent(statErr, runtime.GOOS) {
		t.Fatalf("native stat = %v; want no existing path", statErr)
	}
	result, err := lookupArchivePath(t.Context(), path)
	if err != nil || result.exists || result.notDirectory {
		t.Fatalf("Windows too-long lookup = %+v, %v", result, err)
	}
}

// TestArchivePathFields prevents a longer field from masking the completed live row.
func TestArchivePathFields(t *testing.T) {
	root, linked := archiveWorktreeFixture(t)
	done := fmt.Sprintf("- [x] path: %s | done\n", linked)
	pending := fmt.Sprintf("- [ ] path: %s extra | pending\n", linked)
	open := filepath.Join(root, WorkingDirName, openLedgerName)
	writeIntegrityFile(t, open, done+pending)
	if count, err := ArchiveCompletedTasks(root, "fixture"); err != nil || count != 0 {
		t.Fatalf("path field archive = %d, %v", count, err)
	}
	assertIntegrityFile(t, open, done+pending)
	if namesWorktreePath("path: /repo/topic extra | pending", "/repo/topic") {
		t.Fatal("prefix matched a longer path field")
	}
}

// TestArchiveLongProseFields keeps prose after a slash while discharging completed rows.
func TestArchiveLongProseFields(t *testing.T) {
	prose := "Resolve X (BUG-1): the builder returns ErrA / ErrB per target and the command exits non-zero; " + strings.Repeat("details ", 60)
	slashProse := "Resolve X (BUG-2): " + "/" + strings.Repeat("a/", 2047) + "a"
	for _, sample := range []string{prose, strings.ReplaceAll(prose, "/", `\`), slashProse} {
		for _, format := range []string{"- [ ] %s\n", "| TODO | %s |\n"} {
			t.Run(format, func(t *testing.T) { testArchiveLongProseField(t, format, sample) })
		}
	}
}

// testArchiveLongProseField checks one prose sample and row format against completed task archiving.
func testArchiveLongProseField(t *testing.T, format, sample string) {
	t.Helper()
	root := t.TempDir()
	if err := InitWorkingDir(root); err != nil {
		t.Fatal(err)
	}
	kept := fmt.Sprintf(format, sample)
	done := "- [x] completed one\n- [DONE — fixture] completed two\n"
	open := filepath.Join(root, WorkingDirName, openLedgerName)
	writeIntegrityFile(t, open, kept+done)
	if count, err := ArchiveCompletedTasks(root, "fixture"); err != nil || count != 2 {
		t.Fatalf("long prose archive = %d, %v; want two removed rows", count, err)
	}
	assertIntegrityFile(t, open, kept)
	backlog, err := contextopt.ReadSnapshot(t.Context(), filepath.Join(root, WorkingDirName, "BACKLOG.md"))
	if err != nil || !strings.Contains(string(backlog), done) {
		t.Fatalf("completed rows missing from backlog: %s, %v", backlog, err)
	}
}

// TestArchiveUTF8ComponentWorktree retains completed bullet and table rows for a 128-character two-byte component.
func TestArchiveUTF8ComponentWorktree(t *testing.T) {
	for _, format := range []string{"- [x] id: live | path: %s | branch: fixture\n", "| DONE | %s |\n"} {
		t.Run(format, func(t *testing.T) {
			root, err := filepath.EvalSymlinks(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			if err := InitWorkingDir(root); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(root, strings.Repeat("é", 128))
			// nosemgrep: go.lang.correctness.permissions.file_permission.incorrect-default-permission, go_file-permissions_rule-mkdir -- owner-only worktree fixture directory when the host filesystem permits a 256-byte UTF-8 name
			if err := os.Mkdir(path, 0o700); errors.Is(err, syscall.ENAMETOOLONG) {
				t.Skipf("filesystem cannot create a 128-character two-byte name: %v", err)
			} else if err != nil {
				t.Fatal(err)
			}
			ops := defaultArchiveWorktreeOps()
			ops.list = func(context.Context, string) ([]worktree.WorktreeInfo, error) {
				return []worktree.WorktreeInfo{{Path: path}}, nil
			}
			kept := fmt.Sprintf(format, path)
			open := filepath.Join(root, WorkingDirName, openLedgerName)
			writeIntegrityFile(t, open, kept+"- [x] unrelated\n")
			if count, err := archiveCompletedTasks(t.Context(), root, "fixture", ops); err != nil || count != 1 {
				t.Fatalf("utf8 component archive = %d, %v; want one removed row", count, err)
			}
			assertIntegrityFile(t, open, kept)
		})
	}
	t.Run("real-directory", testArchiveUTF8ComponentRealDirectory)
}

// testArchiveUTF8ComponentRealDirectory exercises real filesystem stat on hosts that allow 128 two-byte characters.
func testArchiveUTF8ComponentRealDirectory(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := InitWorkingDir(root); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, strings.Repeat("é", 128))
	// nosemgrep: go.lang.correctness.permissions.file_permission.incorrect-default-permission, go_file-permissions_rule-mkdir -- owner-only directory probes host UTF-16 vs byte component limits
	if err := os.Mkdir(path, 0o700); errors.Is(err, syscall.ENAMETOOLONG) {
		t.Skipf("filesystem rejects 256-byte UTF-8 component: %v", err)
	} else if err != nil {
		t.Fatal(err)
	}
	ops := defaultArchiveWorktreeOps()
	ops.list = func(context.Context, string) ([]worktree.WorktreeInfo, error) {
		return []worktree.WorktreeInfo{{Path: path}}, nil
	}
	kept := fmt.Sprintf("- [x] id: live | path: %s | branch: fixture\n", path)
	open := filepath.Join(root, WorkingDirName, openLedgerName)
	writeIntegrityFile(t, open, kept+"- [x] unrelated\n")
	if count, err := archiveCompletedTasks(t.Context(), root, "fixture", ops); err != nil || count != 1 {
		t.Fatalf("real utf8 directory archive = %d, %v; want one removed row", count, err)
	}
	assertIntegrityFile(t, open, kept)
}

// TestArchiveRedundantLongWorktreePath retains a live row exceeding the native total bound only before cleaning.
func TestArchiveRedundantLongWorktreePath(t *testing.T) {
	for _, format := range []string{"- [x] id: live | path: %s | branch: fixture/linked\n", "| DONE | %s |\n"} {
		t.Run(format, func(t *testing.T) {
			root, linked := archiveWorktreeFixture(t)
			resolved, err := filepath.EvalSymlinks(linked)
			if err != nil {
				t.Fatal(err)
			}
			redundant := resolved + "/" + strings.Repeat("./", archivePathArgumentLimit()/2+1)
			kept := fmt.Sprintf(format, redundant)
			open := filepath.Join(root, WorkingDirName, openLedgerName)
			writeIntegrityFile(t, open, kept+"- [x] unrelated\n")
			if count, err := ArchiveCompletedTasks(root, "fixture"); err != nil || count != 1 {
				t.Fatalf("redundant worktree archive = %d, %v; want one removed row", count, err)
			}
			assertIntegrityFile(t, open, kept)
		})
	}
}

// TestArchiveCleanedUnmatchedPathDischarged inspects feasible cleaned fields and retains only the live worktree.
func TestArchiveCleanedUnmatchedPathDischarged(t *testing.T) {
	root := t.TempDir()
	if err := InitWorkingDir(root); err != nil {
		t.Fatal(err)
	}
	live := filepath.FromSlash("/repo/live-worktree")
	unmatchedComponent := filepath.FromSlash("/repo/" + strings.Repeat("a", 256))
	unmatchedTotal := filepath.FromSlash("/repo/absent") + "/" + strings.Repeat("./", 2048)
	kept := fmt.Sprintf("- [x] id: live | path: %s | branch: fixture\n", live+"/"+strings.Repeat("./", 2048))
	discharged := fmt.Sprintf("- [x] id: gone-component | path: %s | branch: fixture\n| DONE | %s |\n", unmatchedComponent, unmatchedTotal)
	open := filepath.Join(root, WorkingDirName, openLedgerName)
	writeIntegrityFile(t, open, discharged+kept)
	inspections := 0
	ops := archiveWorktreeOps{
		list: func(context.Context, string) ([]worktree.WorktreeInfo, error) {
			return []worktree.WorktreeInfo{{Path: live}}, nil
		},
		inspect: func(_ context.Context, path string) (archivePath, error) {
			inspections++
			return archivePath{path: path, exists: path == live}, nil
		},
	}
	if count, err := archiveCompletedTasks(t.Context(), root, "fixture", ops); err != nil || count != 2 {
		t.Fatalf("cleaned unmatched archive = %d, %v; want two discharged rows", count, err)
	}
	if inspections != 5 {
		t.Fatalf("inspections = %d; want 3 unique cleaned candidates and 2 live-worktree checks", inspections)
	}
	assertIntegrityFile(t, open, kept)
}

// TestArchivePathFieldLengthBounds applies only the total limit after lexical cleaning.
func TestArchivePathFieldLengthBounds(t *testing.T) {
	limit := requireArchivePathArgumentLimit(t)
	boundary := "/" + strings.Repeat("a/", (limit-3)/2) + "aa"
	for _, tc := range []struct {
		name, path string
		candidate  bool
	}{
		{"component-255", "/" + strings.Repeat("a", 255), true},
		{"component-256", "/" + strings.Repeat("a", 256), true},
		{"total-at-limit", boundary, true},
		{"total-over-limit", boundary + "a", false},
		{"native-component-256", `C:\` + strings.Repeat("a", 256), true},
		{"unicode-256-bytes", "/" + strings.Repeat("é", 128), true},
		{"unicode-256-utf16", "/" + strings.Repeat("é", 256), true},
		{"unc-component-256", `\\host\share\` + strings.Repeat("a", 256), true},
		{"unclean-component", "/" + strings.Repeat("a", 256) + "/../short", true},
		{"unclean-total", "/short" + strings.Repeat("/.", limit/2), true},
	} {
		t.Run(tc.name, func(t *testing.T) { testArchivePathFieldBoundary(t, tc.path, tc.candidate) })
	}
}

// testArchivePathFieldBoundary checks both row readers and observes candidate stat dispatch.
func testArchivePathFieldBoundary(t *testing.T, path string, candidate bool) {
	t.Helper()
	for _, format := range []string{"- [ ] path: `%s`", "| TODO | `%s` |"} {
		calls := 0
		ops := archiveWorktreeOps{inspect: func(_ context.Context, name string) (archivePath, error) {
			calls++
			return archivePath{path: name}, nil
		}}
		index, err := indexWorktreeRows(t.Context(), []string{fmt.Sprintf(format, path)}, ops)
		if err != nil {
			t.Fatal(err)
		}
		want := 0
		if candidate {
			want = 1
		}
		if calls != want || len(index) != want {
			t.Fatalf("candidate = %v; inspections = %d, indexed paths = %d, want %d each", candidate, calls, len(index), want)
		}
	}
}

// TestArchivePathHostBounds pins native total units without imposing a component ceiling.
func TestArchivePathHostBounds(t *testing.T) {
	limit := requireArchivePathArgumentLimit(t)
	for _, size := range []int{limit - 1, limit, limit + 1} {
		if got := archivePathWithinBounds(strings.Repeat("a", size)); got != (size <= limit) {
			t.Fatalf("total %d: within bounds = %v; limit %d", size, got, limit)
		}
	}
	if !archivePathWithinBounds("/" + strings.Repeat("é", 128)) {
		t.Fatal("component length must reach native stat")
	}
}

// TestArchiveZeroPathLimitDefersToStat verifies zero-limit hosts skip pre-stat ceiling tests and preserve stat verdicts.
func TestArchiveZeroPathLimitDefersToStat(t *testing.T) {
	previous := archivePathArgumentLimit
	archivePathArgumentLimit = func() int { return 0 }
	t.Cleanup(func() { archivePathArgumentLimit = previous })
	for _, tc := range []struct {
		name string
		fn   func(*testing.T)
	}{
		{"cleaned-total-limit", TestArchiveCleanedTotalLimit},
		{"field-length-bounds", TestArchivePathFieldLengthBounds},
		{"host-bounds", TestArchivePathHostBounds},
	} {
		skipped := false
		t.Run(tc.name, func(t *testing.T) {
			t.Cleanup(func() { skipped = t.Skipped() })
			tc.fn(t)
		})
		if !skipped {
			t.Fatalf("%s did not skip when archivePathArgumentLimit is 0", tc.name)
		}
	}
	t.Run("invalid-path-stat", TestArchiveInvalidPathStatPreservesRows)
}

// TestArchiveWindowsPathUnits checks native UTF-16 boundaries for Unicode and unpaired surrogates.
func TestArchiveWindowsPathUnits(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("native UTF16FromString and WTF-8 conversion require Windows")
	}
	limit := archivePathArgumentLimit()
	for _, unit := range []string{"é", "\xed\xa0\x80", "\xed\xbf\xbf"} {
		if !archivePathWithinBounds(strings.Repeat(unit, limit)) || archivePathWithinBounds(strings.Repeat(unit, limit+1)) {
			t.Fatal("native single-unit UTF-16 boundary mismatch")
		}
	}
	if !archivePathWithinBounds(strings.Repeat("𐐷", limit/2)+"a") || archivePathWithinBounds(strings.Repeat("𐐷", limit/2)+"aa") {
		t.Fatal("native surrogate-pair boundary mismatch")
	}
}

// TestArchivePathStatLength archives absent candidates at either side of a common component limit.
func TestArchivePathStatLength(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, size := range []int{255, 256} {
		t.Run(fmt.Sprint(size), func(t *testing.T) {
			testArchivePathStatLength(t, root, size)
		})
	}
}

// testArchivePathStatLength proves native missing and too-long answers both discharge the done row.
func testArchivePathStatLength(t *testing.T, root string, size int) {
	t.Helper()
	path := filepath.Join(root, strings.Repeat("a", size))
	_, statErr := os.Stat(path)
	if statErr == nil || !archivePathAbsent(statErr, runtime.GOOS) {
		t.Fatalf("%d-byte absent candidate stat = %v; want missing or too long", size, statErr)
	}
	logArchiveNativeStat(t, statErr)
	result, err := lookupArchivePath(t.Context(), path)
	if err != nil || result.exists || result.notDirectory || result.path != filepath.Clean(path) {
		t.Fatalf("absent archive path = %+v, %v", result, err)
	}
	testArchiveAbsentPathRow(t, root, path)
}

// testArchiveAbsentPathRow verifies an absent candidate moves from OPEN.md to BACKLOG.md.
func testArchiveAbsentPathRow(t *testing.T, root, path string) {
	t.Helper()
	if err := InitWorkingDir(root); err != nil {
		t.Fatal(err)
	}
	row := fmt.Sprintf("- [x] path: %s | done\n", path)
	open := filepath.Join(root, WorkingDirName, openLedgerName)
	writeIntegrityFile(t, open, row)
	ops := defaultArchiveWorktreeOps()
	ops.list = func(context.Context, string) ([]worktree.WorktreeInfo, error) { return nil, nil }
	if count, err := archiveCompletedTasks(t.Context(), root, "fixture", ops); err != nil || count != 1 {
		t.Fatalf("absent candidate archive = %d, %v; want one discharged row", count, err)
	}
	assertIntegrityFile(t, open, "")
	backlog, err := contextopt.ReadSnapshot(t.Context(), filepath.Join(root, WorkingDirName, "BACKLOG.md"))
	if err != nil || !strings.Contains(string(backlog), row) {
		t.Fatalf("archived candidate missing from backlog: %s, %v", backlog, err)
	}
}

// TestArchiveInvalidPathStatPreservesRows keeps every row on a real invalid-name stat error.
func TestArchiveInvalidPathStatPreservesRows(t *testing.T) {
	repeats := max(2048, archivePathArgumentLimit()/2+1)
	shortNUL := filepath.Join(t.TempDir(), "invalid\x00name")
	longNUL := "/" + strings.Repeat("a/", repeats) + "bad\x00name"
	longPlain := "/" + strings.Repeat("a/", repeats) + "badname"
	proseNUL := "completed prose with\x00nul and no path field"
	for _, format := range []string{"- [x] path: %s | done\n", "| DONE | %s |\n"} {
		t.Run("over-limit-nul/"+format, func(t *testing.T) {
			testArchiveInvalidPathPreservesRows(t, longNUL, format)
		})
		t.Run("over-limit-plain/"+format, func(t *testing.T) {
			testArchiveDischargedRow(t, fmt.Sprintf(format, longPlain))
		})
		t.Run("short-nul/"+format, func(t *testing.T) {
			testArchiveInvalidPathPreservesRows(t, shortNUL, format)
		})
		t.Run("stat-nil-nul/"+format, func(t *testing.T) {
			testArchiveNilStatNULPreservesRows(t, shortNUL, format)
		})
	}
	for _, format := range []string{"- [x] %s\n", "| DONE | %s |\n"} {
		t.Run("prose-nul/"+format, func(t *testing.T) {
			testArchiveProseNULRow(t, fmt.Sprintf(format, proseNUL))
		})
	}
}

// testArchiveNilStatNULPreservesRows verifies NUL candidates fail closed even if stat returns no error.
func testArchiveNilStatNULPreservesRows(t *testing.T, candidate, format string) {
	t.Helper()
	previous := archiveStat
	archiveStat = func(string) (os.FileInfo, error) { return nil, nil }
	t.Cleanup(func() { archiveStat = previous })
	ops := defaultArchiveWorktreeOps()
	ops.list = func(context.Context, string) ([]worktree.WorktreeInfo, error) {
		return []worktree.WorktreeInfo{{Path: fmt.Sprintf("%q", candidate)}}, nil
	}
	row := fmt.Sprintf(format, candidate)
	if _, err := planTaskArchive(t.Context(), row, ops); !errors.Is(err, errInvalidArchivePath) || !strings.Contains(err.Error(), candidate) {
		t.Fatalf("nil-stat NUL plan = %v; want %v naming %q", err, errInvalidArchivePath, candidate)
	}
	root := t.TempDir()
	if err := InitWorkingDir(root); err != nil {
		t.Fatal(err)
	}
	const body = "- [x] unrelated\n"
	open := filepath.Join(root, WorkingDirName, openLedgerName)
	backlog := filepath.Join(root, WorkingDirName, "BACKLOG.md")
	writeIntegrityFile(t, open, body)
	writeIntegrityFile(t, backlog, "unchanged backlog\n")
	if count, err := archiveCompletedTasks(t.Context(), root, "fixture", ops); count != 0 || !errors.Is(err, errInvalidArchivePath) || !strings.Contains(err.Error(), candidate) {
		t.Fatalf("nil-stat NUL archive = %d, %v; want %v naming %q", count, err, errInvalidArchivePath, candidate)
	}
	assertIntegrityFile(t, open, body)
	assertIntegrityFile(t, backlog, "unchanged backlog\n")
}

// testArchiveInvalidPathPreservesRows verifies an invalid path candidate aborts archiving without writes.
func testArchiveInvalidPathPreservesRows(t *testing.T, candidate, format string) {
	t.Helper()
	root := t.TempDir()
	if err := InitWorkingDir(root); err != nil {
		t.Fatal(err)
	}
	_, statErr := os.Stat(candidate)
	var pathErr *os.PathError
	if !errors.As(statErr, &pathErr) || archivePathAbsent(statErr, runtime.GOOS) {
		t.Fatalf("invalid-name fixture stat = %v; want non-absence PathError", statErr)
	}
	body := fmt.Sprintf(format, candidate) + "- [x] unrelated\n"
	open := filepath.Join(root, WorkingDirName, openLedgerName)
	backlog := filepath.Join(root, WorkingDirName, "BACKLOG.md")
	writeIntegrityFile(t, open, body)
	writeIntegrityFile(t, backlog, "unchanged backlog\n")
	ops := defaultArchiveWorktreeOps()
	ops.list = func(context.Context, string) ([]worktree.WorktreeInfo, error) { return nil, nil }
	if count, err := archiveCompletedTasks(t.Context(), root, "fixture", ops); count != 0 || err == nil {
		t.Fatalf("invalid-name archive = %d, %v; want an error and no discharged rows", count, err)
	}
	assertIntegrityFile(t, open, body)
	assertIntegrityFile(t, backlog, "unchanged backlog\n")
	if _, err := planTaskArchive(t.Context(), body, ops); !errors.Is(err, pathErr.Err) || !strings.Contains(err.Error(), candidate) {
		t.Fatalf("invalid-name plan = %v; want %v naming %q", err, pathErr.Err, candidate)
	}
}

// testArchiveDischargedRow verifies a completed row with no live worktree path moves to BACKLOG.md.
func testArchiveDischargedRow(t *testing.T, row string) {
	t.Helper()
	root := t.TempDir()
	if err := InitWorkingDir(root); err != nil {
		t.Fatal(err)
	}
	open := filepath.Join(root, WorkingDirName, openLedgerName)
	writeIntegrityFile(t, open, row+"- [ ] pending\n")
	ops := defaultArchiveWorktreeOps()
	ops.list = func(context.Context, string) ([]worktree.WorktreeInfo, error) { return nil, nil }
	if count, err := archiveCompletedTasks(t.Context(), root, "fixture", ops); err != nil || count != 1 {
		t.Fatalf("archive = %d, %v; want one discharged row", count, err)
	}
	assertIntegrityFile(t, open, "- [ ] pending\n")
	backlog, err := contextopt.ReadSnapshot(t.Context(), filepath.Join(root, WorkingDirName, "BACKLOG.md"))
	if err != nil || !strings.Contains(string(backlog), row) {
		t.Fatalf("discharged row missing from backlog: %s, %v", backlog, err)
	}
}

// testArchiveProseNULRow verifies non-candidate prose with NUL is archived by the row planner while rejected as a disk snapshot.
func testArchiveProseNULRow(t *testing.T, row string) {
	t.Helper()
	root := t.TempDir()
	if err := InitWorkingDir(root); err != nil {
		t.Fatal(err)
	}
	body := row + "- [ ] pending\n"
	open := filepath.Join(root, WorkingDirName, openLedgerName)
	backlog := filepath.Join(root, WorkingDirName, "BACKLOG.md")
	writeIntegrityFile(t, open, body)
	writeIntegrityFile(t, backlog, "unchanged backlog\n")
	ops := defaultArchiveWorktreeOps()
	ops.list = func(context.Context, string) ([]worktree.WorktreeInfo, error) { return nil, nil }
	if count, err := archiveCompletedTasks(t.Context(), root, "fixture", ops); count != 0 || err == nil {
		t.Fatalf("snapshot with NUL archive = %d, %v; want snapshot refusal", count, err)
	}
	assertIntegrityFile(t, open, body)
	assertIntegrityFile(t, backlog, "unchanged backlog\n")
	remaining, completed, err := splitCompletedTasks(t.Context(), root, body)
	if err != nil || len(completed) != 1 || completed[0] != strings.TrimSpace(row) || strings.Join(remaining, "\n") != "- [ ] pending\n" {
		t.Fatalf("prose NUL split = %q, %q, %v; want one archived prose row", remaining, completed, err)
	}
}

// TestArchivePermissionDeniedStatPreservesRows keeps every row when stat is refused for permission.
func TestArchivePermissionDeniedStatPreservesRows(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("file modes do not refuse stat on Windows or for root")
	}
	root := t.TempDir()
	if err := InitWorkingDir(root); err != nil {
		t.Fatal(err)
	}
	locked := filepath.Join(root, "locked")
	// nosemgrep: go.lang.correctness.permissions.file_permission.incorrect-default-permission, go_file-permissions_rule-mkdir -- a directory needs the owner search bit to hold entries; 0o700 is least privilege
	if err := os.Mkdir(locked, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(locked, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { // let TempDir remove the tree
		// nosemgrep: go_file-permissions_rule-fileperm, go.lang.correctness.permissions.file_permission.incorrect-default-permission -- restores the owner search bit so TempDir can remove the tree
		if err := os.Chmod(locked, 0o700); err != nil {
			t.Errorf("restore %s: %v", locked, err)
		}
	})
	path := filepath.Join(locked, "worktree")
	if _, statErr := os.Stat(path); !errors.Is(statErr, os.ErrPermission) {
		t.Fatalf("locked fixture stat = %v; want permission denied", statErr)
	}
	_, longStatErr := os.Stat(path)
	testArchiveLongPathError(t, root, path, longStatErr)
	body := "- [x] path: " + path + " | done\n- [x] unrelated\n"
	open := filepath.Join(root, WorkingDirName, openLedgerName)
	backlog := filepath.Join(root, WorkingDirName, "BACKLOG.md")
	writeIntegrityFile(t, open, body)
	writeIntegrityFile(t, backlog, "unchanged backlog\n")
	ops := defaultArchiveWorktreeOps()
	ops.list = func(context.Context, string) ([]worktree.WorktreeInfo, error) { return nil, nil }
	if count, err := archiveCompletedTasks(t.Context(), root, "fixture", ops); count != 0 || err == nil {
		t.Fatalf("permission-denied archive = %d, %v; want an error and no discharged rows", count, err)
	}
	assertIntegrityFile(t, open, body)
	assertIntegrityFile(t, backlog, "unchanged backlog\n")
}

// TestArchiveCanceledScan preserves the snapshot when the archive context is canceled.
func TestArchiveCanceledScan(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, _, err := splitCompletedTasks(ctx, t.TempDir(), "- [x] done\n"); err == nil {
		t.Fatal("canceled archive accepted")
	}
}

// TestArchiveWorktreeCreatedBeforeWrite protects a real worktree created after the first listing.
func TestArchiveWorktreeCreatedBeforeWrite(t *testing.T) {
	root, _ := archiveWorktreeFixture(t)
	manager := worktree.NewManager(root)
	racing := manager.WorktreePath("racing")
	kept := fmt.Sprintf("- [x] path: %s | racing\n", racing)
	open := filepath.Join(root, WorkingDirName, openLedgerName)
	writeIntegrityFile(t, open, kept+"- [x] unrelated\n")
	ops := defaultArchiveWorktreeOps()
	calls := 0
	ops.list = func(ctx context.Context, root string) ([]worktree.WorktreeInfo, error) {
		calls++
		if calls == 2 {
			if _, err := manager.Create(ctx, "racing", "HEAD"); err != nil {
				return nil, err
			}
		}
		return listArchiveWorktrees(ctx, root)
	}
	if count, err := archiveCompletedTasks(t.Context(), root, "fixture", ops); err != nil || count != 1 {
		t.Fatalf("racing archive = %d, %v", count, err)
	}
	if calls != 2 {
		t.Fatalf("listing calls = %d; want revalidation", calls)
	}
	assertIntegrityFile(t, open, kept)
}

// TestArchivePorcelainLineLimit exercises the real manager parser through a bounded Git stand-in.
func TestArchivePorcelainLineLimit(t *testing.T) {
	root, _ := archiveWorktreeFixture(t)
	open := filepath.Join(root, WorkingDirName, openLedgerName)
	const body = "- [x] preserve on listing limit\n"
	writeIntegrityFile(t, open, body)
	bin := t.TempDir()
	source := fmt.Sprintf(`package main
import ("fmt"; "strings")
func main() { fmt.Print(strings.Repeat("worktree /repo/topic\n", %d)) }
`, worktree.MaxPorcelainLines+1)
	testsupport.BuildExecutable(t, bin, "git", source)
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	count, err := ArchiveCompletedTasks(root, "fixture")
	if count != 0 || !errors.Is(err, worktree.ErrLimitExceeded) || !strings.Contains(err.Error(), "archive worktree listing") {
		t.Fatalf("truncated listing accepted: %d, %v", count, err)
	}
	assertIntegrityFile(t, open, body)
}

// TestArchiveStalledStatPreservesRows aborts at the deadline and refuses another outstanding lookup.
func TestArchiveStalledStatPreservesRows(t *testing.T) {
	root, linked := archiveWorktreeFixture(t)
	open := filepath.Join(root, WorkingDirName, openLedgerName)
	body := fmt.Sprintf("- [x] path: %s | stalled\n", linked)
	writeIntegrityFile(t, open, body)
	release := make(chan struct{})
	defer close(release)
	ops := defaultArchiveWorktreeOps()
	calls := 0
	ops.inspect = func(ctx context.Context, path string) (archivePath, error) {
		calls++
		if calls == 1 {
			return inspectArchivePath(ctx, path)
		}
		return boundedArchivePath(ctx, path, func(string) (archivePath, error) { <-release; return archivePath{}, nil })
	}
	ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
	defer cancel()
	start := time.Now()
	count, err := archiveCompletedTasks(ctx, root, "fixture", ops)
	if count != 0 || !errors.Is(err, context.DeadlineExceeded) || !strings.Contains(err.Error(), "inspect archive worktree") {
		t.Fatalf("stalled stat = %d, %v", count, err)
	}
	if time.Since(start) > 2*time.Second {
		t.Fatal("stalled stat exceeded bounded wait")
	}
	assertIntegrityFile(t, open, body)
	second, stop := context.WithTimeout(t.Context(), 20*time.Millisecond)
	defer stop()
	var secondStarted atomic.Bool
	_, err = boundedArchivePath(second, linked, func(string) (archivePath, error) { secondStarted.Store(true); return archivePath{}, nil })
	if secondStarted.Load() {
		t.Fatal("second stalled lookup started")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("occupied stat slot = %v", err)
	}
}

// TestArchiveManyWorktreesIndexesRows bounds work by unique paths plus listings, not rows times worktrees.
func TestArchiveManyWorktreesIndexesRows(t *testing.T) {
	const count = 4000
	root := t.TempDir()
	if err := InitWorkingDir(root); err != nil {
		t.Fatal(err)
	}
	var body, kept strings.Builder
	entries := make([]worktree.WorktreeInfo, count)
	for i := 0; i < count; i++ {
		path := filepath.Join(root, fmt.Sprintf("topic-%d", i))
		entries[i].Path = path
		for round := 0; round < 4; round++ {
			row := fmt.Sprintf("- [x] path: %s | round: %d\n", path, round)
			body.WriteString(row)
			if round == 3 {
				kept.WriteString(row)
			}
		}
	}
	body.WriteString("- [ ] pending\n")
	kept.WriteString("- [ ] pending\n")
	open := filepath.Join(root, WorkingDirName, openLedgerName)
	writeIntegrityFile(t, open, body.String())
	inspections := 0
	ops := archiveWorktreeOps{
		list: func(context.Context, string) ([]worktree.WorktreeInfo, error) { return entries, nil },
		inspect: func(_ context.Context, path string) (archivePath, error) {
			inspections++
			return archivePath{path: path, exists: true}, nil
		},
	}
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	if archived, err := archiveCompletedTasks(ctx, root, "fixture", ops); err != nil || archived != count*3 {
		t.Fatalf("large archive = %d, %v", archived, err)
	}
	if inspections != count*3 {
		t.Fatalf("inspections = %d; want %d", inspections, count*3)
	}
	assertIntegrityFile(t, open, kept.String())
}

// TestArchiveInspectionErrorsPreserveRows refuses filesystem and second-listing failures before writing.
func TestArchiveInspectionErrorsPreserveRows(t *testing.T) {
	for _, failListing := range []bool{false, true} {
		t.Run(fmt.Sprint(failListing), func(t *testing.T) { testArchiveInspectionError(t, failListing) })
	}
}

// testArchiveInspectionError checks one failure stage without changing the ledger snapshot.
func testArchiveInspectionError(t *testing.T, failListing bool) {
	t.Helper()
	root := t.TempDir()
	if err := InitWorkingDir(root); err != nil {
		t.Fatal(err)
	}
	open := filepath.Join(root, WorkingDirName, openLedgerName)
	path := filepath.Join(root, "missing")
	body := fmt.Sprintf("- [x] path: %s | done\n", path)
	writeIntegrityFile(t, open, body)
	failed := errors.New("fixture inspection refused")
	calls := 0
	ops := defaultArchiveWorktreeOps()
	if failListing {
		ops.list = func(context.Context, string) ([]worktree.WorktreeInfo, error) {
			calls++
			if calls == 2 {
				return nil, failed
			}
			return nil, nil
		}
	} else {
		ops.inspect = func(context.Context, string) (archivePath, error) { return archivePath{}, failed }
	}
	if count, err := archiveCompletedTasks(t.Context(), root, "fixture", ops); count != 0 || !errors.Is(err, failed) {
		t.Fatalf("inspection failure = %d, %v", count, err)
	}
	assertIntegrityFile(t, open, body)
}

// TestArchiveMissingPathCanonicalAncestor resolves a symlinked parent before the worktree exists.
func TestArchiveMissingPathCanonicalAncestor(t *testing.T) {
	parent := t.TempDir()
	alias := filepath.Join(t.TempDir(), "alias")
	if err := os.Symlink(parent, alias); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	actual, err := inspectArchivePath(t.Context(), filepath.Join(parent, "not-yet-created"))
	if err != nil {
		t.Fatal(err)
	}
	recorded, err := inspectArchivePath(t.Context(), filepath.Join(alias, "not-yet-created"))
	if err != nil || recorded.path != actual.path || recorded.exists {
		t.Fatalf("missing alias path = %+v, %v; want %+v", recorded, err, actual)
	}
}

// TestArchiveRegisteredPathNotDirectory retains the prior fail-closed behavior for invalid registrations.
func TestArchiveRegisteredPathNotDirectory(t *testing.T) {
	root := t.TempDir()
	if err := InitWorkingDir(root); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "registered-file")
	writeIntegrityFile(t, path, "fixture\n")
	body := fmt.Sprintf("- [x] path: %s | done\n", path)
	open := filepath.Join(root, WorkingDirName, openLedgerName)
	writeIntegrityFile(t, open, body)
	ops := defaultArchiveWorktreeOps()
	ops.list = func(context.Context, string) ([]worktree.WorktreeInfo, error) {
		return []worktree.WorktreeInfo{{Path: path}}, nil
	}
	if count, err := archiveCompletedTasks(t.Context(), root, "fixture", ops); count != 0 || err == nil || !strings.Contains(err.Error(), "not a directory") {
		t.Fatalf("invalid registration = %d, %v", count, err)
	}
	assertIntegrityFile(t, open, body)
}

// TestNamesNativeWorktreeFields keeps native drive and UNC spellings as complete path fields.
func TestNamesNativeWorktreeFields(t *testing.T) {
	for _, path := range []string{`C:\repo\linked worktree`, `\\server\share\linked worktree`} {
		if !namesWorktreePath("path: `"+path+"` | branch: topic", path) {
			t.Errorf("native path field missed: %q", path)
		}
		if namesWorktreePath("path: "+path+" extra | branch: topic", path) {
			t.Errorf("native prefix matched longer field: %q", path)
		}
	}
}

// logArchiveNativeStat records the actual native code without prescribing a filesystem's answer.
func logArchiveNativeStat(t *testing.T, err error) {
	t.Helper()
	var code syscall.Errno
	if !errors.As(err, &code) {
		t.Fatalf("native stat error lacks a syscall code: %v", err)
	}
	t.Logf("native stat code = %d; error = %v", uint64(code), err)
}

// TestArchiveWindowsErrorClassification exercises the production Windows list without Windows I/O.
func TestArchiveWindowsErrorClassification(t *testing.T) {
	for _, tc := range []struct {
		code   syscall.Errno
		absent bool
	}{
		{2, true}, {3, true}, {53, true}, {123, true}, {206, true},
		{5, false}, {32, false}, {31, false}, {21, false}, {87, false}, {36, false},
	} {
		t.Run(fmt.Sprint(uint64(tc.code)), func(t *testing.T) {
			err := fmt.Errorf("inspect: %w", &os.PathError{Op: "CreateFile", Path: "fixture", Err: tc.code})
			if got := archivePathAbsent(err, "windows"); got != tc.absent {
				t.Fatalf("Windows code %d: absent = %v; want %v", uint64(tc.code), got, tc.absent)
			}
		})
	}
	for _, err := range []error{nil, os.ErrPermission, errors.New("device failure")} {
		if archivePathAbsent(err, "windows") {
			t.Errorf("non-missing error classified absent: %v", err)
		}
	}
}

// TestArchiveUnixErrorClassification preserves Unix absence and fail-closed error decisions.
func TestArchiveUnixErrorClassification(t *testing.T) {
	for _, tc := range []struct {
		err    error
		absent bool
	}{
		{os.ErrNotExist, true}, {syscall.ENAMETOOLONG, true},
		{syscall.EACCES, false}, {syscall.EIO, false}, {syscall.EINVAL, false},
		{syscall.Errno(123), false}, {syscall.Errno(206), false}, {nil, false},
	} {
		if got := archivePathAbsent(tc.err, "linux"); got != tc.absent {
			t.Errorf("Unix error %v: absent = %v; want %v", tc.err, got, tc.absent)
		}
	}
}

// TestArchivePathCandidatePlatforms runs native spellings and prose through both platform rules.
func TestArchivePathCandidatePlatforms(t *testing.T) {
	prose := `Resolve X (BUG-1): the builder returns ErrA \ ErrB per target and the command exits non-zero; ` + strings.Repeat("details ", 60)
	for _, tc := range []struct {
		name, field   string
		windows, unix bool
	}{
		{"bullet-prose", "- [ ] " + prose, false, false},
		{"table-prose", prose, false, false},
		{"several-backslashes", `Resolve ErrA \ ErrB \ ErrC per target`, false, false},
		{"embedded-backslashes", `Resolve ErrA\ErrB and ErrC\ErrD per target`, false, false},
		{"slash-prose", strings.ReplaceAll(prose, `\`, "/"), false, false},
		{"relative", `relative\worktree`, true, false},
		{"relative-leading-space", `relative\ leading worktree`, true, false},
		{"quoted-relative-first-space", `path: "relative worktree\child"`, true, false},
		{"backtick-relative-first-space", "path: `relative worktree\\child`", true, false},
		{"single-quoted-relative-first-space", `path: 'relative worktree\child'`, true, false},
		{"parenthesized-relative-first-space", `path: (relative worktree\child)`, true, false},
		{"bracketed-relative-first-space", `path: [relative worktree\child]`, true, false},
		{"unwrapped-relative-first-space", `relative worktree\child`, false, false},
		{"unpaired-relative-first-space", `path: "relative worktree\child`, false, false},
		{"relative-long-prose", strings.Repeat("long prose with spaces ", 60) + `worktree\child`, false, false},
		{"record-prose-separator", "- [DONE — fixture] details / more " + strings.Repeat("details ", 60), false, false},
		{"dot-relative", `.\worktree`, true, false},
		{"parent-relative", `..\worktree`, true, false},
		{"label-relative", "path: `relative\\worktree`", true, false},
		{"drive", `C:\repo\linked worktree`, true, true},
		{"drive-slash", `C:/repo/linked worktree`, true, true},
		{"unc", `\\server\share\linked worktree`, true, true},
		{"extended", `\\?\C:\repo\linked worktree`, true, true},
		{"extended-unc", `\\?\UNC\server\share\linked worktree`, true, true},
		{"root-relative", `\repo\linked worktree`, true, true},
		{"only-backslashes", `\\\\`, false, false},
		{"single-backslash", `\`, false, false},
		{"unix", "/repo/linked worktree", true, true},
		{"leading-component-space", "/ leading worktree", true, true},
		{"label-leading-space", "path: / leading worktree", true, true},
		{"wrapped-leading-space", "`/ leading worktree`", true, true},
		{"checkbox-leading-space", "- [x] / leading worktree", true, true},
		{"drive-leading-space", `C:\ leading worktree`, true, true},
		{"drive-relative", `C:relative\worktree`, true, false},
		{"drive-relative-first-space", `C:relative worktree\child`, true, false},
		{"unix-backslash-component", `/repo/a\b`, true, true},
		{"url", "url: https://example.test/repo", false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := archivePathStartForPlatform(tc.field, "windows") >= 0; got != tc.windows {
				t.Errorf("Windows candidate %q = %v; want %v", tc.field, got, tc.windows)
			}
			if got := archivePathStartForPlatform(tc.field, "linux") >= 0; got != tc.unix {
				t.Errorf("Unix candidate %q = %v; want %v", tc.field, got, tc.unix)
			}
		})
	}
}

// TestArchiveWindowsRelativeOffsets preserves complete drive-relative fields after labels and wrappers.
func TestArchiveWindowsRelativeOffsets(t *testing.T) {
	for _, tc := range []struct{ field, path string }{
		{`relative\worktree`, `relative\worktree`},
		{`path: "relative worktree\child"`, `relative worktree\child"`},
		{`C:relative\worktree`, `C:relative\worktree`},
		{`C:relative worktree\child`, `C:relative worktree\child`},
		{`path: C:relative worktree\child`, `C:relative worktree\child`},
		{`path: C:relative\worktree`, `C:relative\worktree`},
		{"path: `C:relative\\worktree`", "C:relative\\worktree`"},
	} {
		start := archivePathStartForPlatform(tc.field, "windows")
		if start < 0 || tc.field[start:] != tc.path {
			t.Errorf("candidate start for %q = %d; want complete %q", tc.field, start, tc.path)
		}
	}
}

// TestArchiveWindowsRelativeRetention indexes complete Windows fields and retains their live rows on any host.
func TestArchiveWindowsRelativeRetention(t *testing.T) {
	for _, tc := range []struct{ field, path string }{
		{`path: "relative worktree\child"`, `relative worktree\child`},
		{`C:relative worktree\child`, `C:relative worktree\child`},
	} {
		t.Run(tc.field, func(t *testing.T) { testArchiveWindowsRelativeRetention(t, tc.field, tc.path) })
	}
}

// testArchiveWindowsRelativeRetention checks one platform-parsed field through indexing and retention.
func testArchiveWindowsRelativeRetention(t *testing.T, field, want string) {
	t.Helper()
	root := t.TempDir()
	if err := InitWorkingDir(root); err != nil {
		t.Fatal(err)
	}
	start := archivePathStartForPlatform(field, "windows")
	if start < 0 {
		t.Fatalf("Windows field not indexed: %q", field)
	}
	path, err := archiveCleanPath(archiveUnwrapPath(field[:start], field[start:]))
	if err != nil || path != filepath.Clean(want) {
		t.Fatalf("Windows field = %q, %v; want %q", path, err, want)
	}
	ops := archiveWorktreeOps{
		list: func(context.Context, string) ([]worktree.WorktreeInfo, error) {
			return []worktree.WorktreeInfo{{Path: want}}, nil
		},
		inspect: func(_ context.Context, name string) (archivePath, error) {
			return archivePath{path: name, exists: true}, nil
		},
	}
	index := archiveRowIndex{rows: make(map[string][]int), normalized: make(map[string]string)}
	if err := index.add(t.Context(), 0, []string{path}, ops); err != nil {
		t.Fatal(err)
	}
	row := "- [x] " + field
	content := []byte(row + "\n- [x] unrelated\n")
	open := filepath.Join(root, WorkingDirName, openLedgerName)
	writeIntegrityFile(t, open, string(content))
	plan := taskArchivePlan{lines: []string{row, "- [x] unrelated", ""}, archive: map[int]bool{0: true, 1: true}, index: index.rows}
	if err := retainWorktreeRows(t.Context(), root, plan.index, plan.archive, ops); err != nil {
		t.Fatal(err)
	}
	remaining, completed, err := plan.split(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if count, err := publishTaskArchive(t.Context(), root, open, "fixture", content, remaining, completed); err != nil || count != 1 {
		t.Fatalf("Windows archive = %d, %v; want one removed row", count, err)
	}
	assertIntegrityFile(t, open, row+"\n")
}
