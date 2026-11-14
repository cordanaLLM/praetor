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
