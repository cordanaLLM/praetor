// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package state

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/contextopt"
	"github.com/cordanaLLM/praetor/internal/testsupport"
	"github.com/cordanaLLM/praetor/internal/util"
)

// Positive, negative and byte boundaries use short lines so the file-size guard,
// rather than the independent task-line bound, decides each outcome.
func TestOpenLedgerSizeSync(t *testing.T) {
	for _, size := range []int{800*1024 - 1, 800 * 1024, 800*1024 + 1, 900 * 1024, 1 << 20, 1<<20 + 1} {
		t.Run(fmt.Sprint(size), func(t *testing.T) {
			testOpenLedgerSizeSync(t, size)
		})
	}
}

// testOpenLedgerSizeSync writes an OPEN.md of size bytes, syncs state and checks the warning or the typed error.
func testOpenLedgerSizeSync(t *testing.T, size int) {
	t.Helper()
	root := t.TempDir()
	stateFixtureGit(t, root, "init")
	if err := InitWorkingDir(root); err != nil {
		t.Fatal(err)
	}
	body := strings.Repeat("padding\n", size/8) + strings.Repeat(" ", size%8)
	writeIntegrityFile(t, filepath.Join(root, WorkingDirName, "OPEN.md"), body)
	var warnings bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&warnings, nil)))
	defer slog.SetDefault(previous)
	snap, err := SyncState(testsupport.DeadlineContext(t), root, "size fixture")
	if size > 1<<20 {
		if !errors.Is(err, util.ErrFileTooLarge) || !strings.Contains(err.Error(), "praetorctl state task archive") {
			t.Fatalf("oversize error lacks archive remedy: %v", err)
		}
		return
	}
	if err != nil || snap == nil {
		t.Fatalf("sync below read limit: snapshot=%v error=%v", snap, err)
	}
	assertOpenSizeWarning(t, warnings.String(), size)
}

// assertOpenSizeWarning requires no warning at or below 800 KiB and a warning naming size and archive command above it.
func assertOpenSizeWarning(t *testing.T, warning string, size int) {
	t.Helper()
	if size <= 800*1024 {
		if warning != "" {
			t.Fatalf("threshold itself warned: %s", warning)
		}
		return
	}
	for _, want := range []string{"WARN", fmt.Sprint(size), "1048576", "praetorctl state task archive"} {
		if !strings.Contains(warning, want) {
			t.Errorf("warning lacks %q: %s", want, warning)
		}
	}
}

// TestOpenLedgerSizeWarningUnusableContext requires an error, never a silent pass, for a nil or canceled context.
func TestOpenLedgerSizeWarningUnusableContext(t *testing.T) {
	root := t.TempDir()
	stateFixtureGit(t, root, "init")
	if err := InitWorkingDir(root); err != nil {
		t.Fatal(err)
	}
	canceled, cancel := context.WithCancel(testsupport.DeadlineContext(t))
	cancel()
	for _, ctx := range []context.Context{nil, canceled} {
		if warning, err := OpenLedgerSizeWarning(ctx, root); err == nil || warning != "" {
			t.Fatalf("unusable context: warning=%q error=%v", warning, err)
		}
	}
}

// TestOversizedOpenRecovery exercises the advertised command's implementation beyond the read limit.
func TestOversizedOpenRecovery(t *testing.T) {
	root := t.TempDir()
	if err := InitWorkingDir(root); err != nil {
		t.Fatal(err)
	}
	pending := "- [ ] keep pending first\n- [ ] literal [DONE and | DONE\n"
	done := strings.Repeat("- [x] discharged record with padding\n", maxLedgerBytes/35+100)
	open := filepath.Join(root, WorkingDirName, openLedgerName)
	writeIntegrityFile(t, open, pending+done+"- [ ] keep pending last\n")
	if _, err := OpenLedgerSizeWarning(t.Context(), root); !errors.Is(err, util.ErrFileTooLarge) {
		t.Fatalf("oversize warning = %v", err)
	}
	if count, err := ArchiveCompletedTasks(root, "fixture"); err != nil || count == 0 {
		t.Fatalf("advertised recovery = %d, %v", count, err)
	}
	assertIntegrityFile(t, open, pending+"- [ ] keep pending last\n")
	if warning, err := OpenLedgerSizeWarning(t.Context(), root); err != nil || warning != "" {
		t.Fatalf("recovery still over limit: %s, %v", warning, err)
	}
	assertRecoveryArchives(t, root, strings.Count(done, "- [x]"))
}

// TestArchiveRecoveryFullBacklog rolls a full ledger into indexed history and repeats unchanged.
func TestArchiveRecoveryFullBacklog(t *testing.T) {
	root := t.TempDir()
	if err := InitWorkingDir(root); err != nil {
		t.Fatal(err)
	}
	backlogFile := filepath.Join(root, WorkingDirName, backlogLedgerName)
	const pending = "- [ ] Deferred migration\n"
	backlog := pending + "```\n- [ ] fenced example\n```\n"
	backlog += strings.Repeat("padding\n", (maxLedgerBytes-len(backlog))/8)
	writeIntegrityFile(t, backlogFile, backlog)
	done := strings.Repeat("- [x] discharged record with padding\n", maxLedgerBytes/35+100)
	open := filepath.Join(root, WorkingDirName, openLedgerName)
	writeIntegrityFile(t, open, done+"- [ ] pending\n")
	want := strings.Count(done, "- [x]")
	if count, err := ArchiveCompletedTasks(root, "fixture"); err != nil || count != want {
		t.Fatalf("full BACKLOG recovery = %d, %v; want %d", count, err, want)
	}
	assertIntegrityFile(t, open, "- [ ] pending\n")
	assertRecoveryArchives(t, root, want)
	assertArchivedBacklog(t, root, backlog)
	index, err := contextopt.ReadSnapshot(t.Context(), backlogFile)
	if err != nil {
		t.Fatal(err)
	}
	assertPendingBacklogRecovery(t, root, string(index), pending)
	before := recoveryHistoryNames(t, root)
	if count, err := ArchiveCompletedTasks(root, "fixture"); err != nil || count != 0 {
		t.Fatalf("repeat full BACKLOG recovery = %d, %v", count, err)
	}
	assertIntegrityFile(t, backlogFile, string(index))
	if after := recoveryHistoryNames(t, root); strings.Join(before, "\n") != strings.Join(after, "\n") {
		t.Fatalf("repeat recovery created history: %v -> %v", before, after)
	}
}

// assertPendingBacklogRecovery checks listing and row placement while excluding archived examples.
func assertPendingBacklogRecovery(t *testing.T, root, index, pending string) {
	t.Helper()
	listed, err := ListBacklogTasksContext(t.Context(), root)
	if err != nil || len(listed) != 1 {
		t.Fatalf("retained backlog tasks = %+v, %v", listed, err)
	}
	if listed[0].Completed || listed[0].Description != "Deferred migration" {
		t.Errorf("retained backlog task = %+v", listed[0])
	}
	if listed[0].Section != "Future Workstreams" {
		t.Errorf("pending backlog task under wrong heading: %q", listed[0].Section)
	}
	if !strings.HasSuffix(index, pending) {
		t.Fatal("pending backlog row missing after history index")
	}
}

// TestArchiveRecoveryBacklogCapacity checks the retained-row index at and just above its byte limit.
func TestArchiveRecoveryBacklogCapacity(t *testing.T) {
	for _, extra := range []int{0, 1} {
		t.Run(fmt.Sprint(extra), func(t *testing.T) { testArchiveRecoveryBacklogCapacity(t, extra) })
	}
}

// testArchiveRecoveryBacklogCapacity requires overflow refusal before any ledger or history write.
func testArchiveRecoveryBacklogCapacity(t *testing.T, extra int) {
	t.Helper()
	root := t.TempDir()
	if err := InitWorkingDir(root); err != nil {
		t.Fatal(err)
	}
	const header = "\n### Discharged Tasks [2006-01-02 15:04:05 UTC, commit `fixture`]\n"
	_, index, err := archiveChunkIndex(t.Context(), header, 2)
	if err != nil {
		t.Fatal(err)
	}
	size := maxLedgerBytes - len(index) - len(archivePendingHeading) + extra
	const row = "- [ ] Deferred migration\n"
	backlog := strings.Repeat(row, (size-7)/len(row))
	backlog += "- [ ] " + strings.Repeat("p", size-len(backlog)-7) + "\n"
	backlogFile := filepath.Join(root, WorkingDirName, backlogLedgerName)
	writeIntegrityFile(t, backlogFile, backlog)
	open := filepath.Join(root, WorkingDirName, openLedgerName)
	done := strings.Repeat("- [x] done\n", 100)
	writeIntegrityFile(t, open, done)
	count, err := ArchiveCompletedTasks(root, "fixture")
	if extra == 0 {
		if err != nil || count != 100 {
			t.Fatalf("exact-fit recovery = %d, %v", count, err)
		}
		assertExactBacklogRecovery(t, backlogFile, backlog)
		assertIntegrityFile(t, open, "")
		return
	}
	if count != 0 || err == nil || !strings.Contains(err.Error(), "archive index") {
		t.Errorf("retained backlog overflow = %d, %v", count, err)
	}
	assertIntegrityFile(t, backlogFile, backlog)
	assertIntegrityFile(t, open, done)
	if names := recoveryHistoryNames(t, root); len(names) != 0 {
		t.Fatalf("history published before retained backlog fit: %v", names)
	}
}

// assertExactBacklogRecovery requires a readable ledger exactly at the byte cap with all pending rows.
func assertExactBacklogRecovery(t *testing.T, path, pending string) {
	t.Helper()
	data, err := contextopt.ReadSnapshot(t.Context(), path)
	if err != nil || len(data) != maxLedgerBytes || !strings.HasSuffix(string(data), pending) {
		t.Fatalf("exact-fit backlog = %d bytes, %v", len(data), err)
	}
}

// TestArchiveRecoveryIndexOverflow refuses an unindexable archive before publishing history.
func TestArchiveRecoveryIndexOverflow(t *testing.T) {
	root := t.TempDir()
	if err := InitWorkingDir(root); err != nil {
		t.Fatal(err)
	}
	open := filepath.Join(root, WorkingDirName, openLedgerName)
	const body = "- [x] done\n"
	writeIntegrityFile(t, open, body)
	sha := strings.Repeat("s", maxLedgerBytes-256)
	if count, err := ArchiveCompletedTasks(root, sha); count != 0 || err == nil || !strings.Contains(err.Error(), "archive index") {
		t.Errorf("unindexable archive = %d, %v", count, err)
	}
	assertIntegrityFile(t, open, body)
	if names := recoveryHistoryNames(t, root); len(names) != 0 {
		t.Fatalf("unindexed history published: %v", names)
	}
}

// recoveryHistoryNames enumerates only history files in the fixture ledger directory.
func recoveryHistoryNames(t *testing.T, root string) []string {
	t.Helper()
	names, err := filepath.Glob(filepath.Join(root, WorkingDirName, "BACKLOG.history-*.md"))
	if err != nil {
		t.Fatal(err)
	}
	return names
}

// assertArchivedBacklog requires an exact copy of the rolled ledger and private history modes.
func assertArchivedBacklog(t *testing.T, root, backlog string) {
	t.Helper()
	found := false
	names := recoveryHistoryNames(t, root)
	for i := 0; i < len(names) && i < maxScannedLines; i++ {
		name := names[i]
		data, err := contextopt.ReadSnapshot(t.Context(), name)
		if err != nil {
			t.Fatal(err)
		}
		found = found || string(data) == backlog
		t.Run(filepath.Base(name), func(t *testing.T) { testsupport.RequireCreatedMode(t, name, 0o600) })
	}
	if !found {
		t.Fatal("full BACKLOG bytes missing from history")
	}
}

// TestArchiveRecoverySnapshotBounds preserves the default limit and rejects stale recovery expectations.
func TestArchiveRecoverySnapshotBounds(t *testing.T) {
	path := filepath.Join(t.TempDir(), "OPEN.md")
	large := []byte(strings.Repeat("line\n", maxLedgerBytes/5+1))
	writeIntegrityFile(t, path, string(large))
	if _, err := contextopt.ReadSnapshot(t.Context(), path); !errors.Is(err, util.ErrFileTooLarge) {
		t.Fatalf("ordinary read limit = %v", err)
	}
	observed, err := contextopt.ReadSnapshotLimit(t.Context(), path, maxArchiveRecoveryBytes)
	if err != nil || !bytes.Equal(observed, large) {
		t.Fatalf("bounded recovery snapshot = %d, %v", len(observed), err)
	}
	options := contextopt.ReplaceOptions{Expected: observed, Exists: true, Mode: 0o600}
	if err := contextopt.ReplaceSnapshot(t.Context(), path, []byte("small\n"), options); err == nil {
		t.Fatal("default writer accepted oversized expectation")
	}
	options.ExpectedLimit = maxArchiveRecoveryBytes
	writeIntegrityFile(t, path, string(large)+"changed\n")
	if err := contextopt.ReplaceSnapshot(t.Context(), path, []byte("small\n"), options); err == nil {
		t.Fatal("recovery writer accepted stale bytes")
	}
	assertIntegrityFile(t, path, string(large)+"changed\n")
	for _, limit := range []int64{-1, 0, maxArchiveRecoveryBytes + 1} {
		if _, err := contextopt.ReadSnapshotLimit(t.Context(), path, limit); err == nil {
			t.Errorf("invalid recovery bound %d accepted", limit)
		}
	}
	options.ExpectedLimit = maxArchiveRecoveryBytes + 1
	if err := contextopt.ReplaceSnapshot(t.Context(), path, nil, options); err == nil {
		t.Fatal("unbounded recovery expectation accepted")
	}
}

// TestArchiveRecoveryRefusesPendingOverflow leaves every byte intact when live work cannot fit.
func TestArchiveRecoveryRefusesPendingOverflow(t *testing.T) {
	root := t.TempDir()
	if err := InitWorkingDir(root); err != nil {
		t.Fatal(err)
	}
	body := strings.Repeat("- [ ] pending\n", maxLedgerBytes/14+10) + "- [x] done\n"
	open := filepath.Join(root, WorkingDirName, openLedgerName)
	writeIntegrityFile(t, open, body)
	if count, err := ArchiveCompletedTasks(root, "fixture"); count != 0 || err == nil || !strings.Contains(err.Error(), "pending and live rows") {
		t.Fatalf("oversized pending archive = %d, %v", count, err)
	}
	assertIntegrityFile(t, open, body)
}

// assertRecoveryArchives reads every discharge file through the shared bound and counts preserved rows.
func assertRecoveryArchives(t *testing.T, root string, want int) {
	t.Helper()
	names, err := filepath.Glob(filepath.Join(root, WorkingDirName, "BACKLOG.history-*.md"))
	if err != nil || len(names) == 0 {
		t.Fatalf("recovery archives = %v, %v", names, err)
	}
	backlog, err := contextopt.ReadSnapshot(t.Context(), filepath.Join(root, WorkingDirName, backlogLedgerName))
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, name := range names {
		data, err := contextopt.ReadSnapshot(t.Context(), name)
		if err != nil {
			t.Fatalf("archive unreadable through ordinary bound: %v", err)
		}
		if !strings.Contains(string(backlog), filepath.Base(name)) {
			t.Fatalf("archive %s not indexed in BACKLOG.md", name)
		}
		count += strings.Count(string(data), "- [x]")
	}
	if count != want {
		t.Fatalf("archived rows = %d; want %d", count, want)
	}
}

// TestArchiveRecoverySnapshotSymlink refuses a symlink even with the larger recovery read bound.
func TestArchiveRecoverySnapshotSymlink(t *testing.T) {
	path := filepath.Join(t.TempDir(), openLedgerName)
	writeIntegrityFile(t, path, "- [x] fixture\n")
	link := filepath.Join(t.TempDir(), "link.md")
	if err := os.Symlink(path, link); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	if _, err := contextopt.ReadSnapshotLimit(t.Context(), link, maxArchiveRecoveryBytes); err == nil {
		t.Fatal("recovery followed a symlink")
	}
}
