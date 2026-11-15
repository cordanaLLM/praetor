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
	"strings"
	"sync/atomic"
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
