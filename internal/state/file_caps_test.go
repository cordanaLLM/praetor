package state

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// capLedger seeds only temporary state and supplies the first ratchet observation.
func capLedger(t *testing.T, name, content string) string {
	t.Helper()
	root := t.TempDir()
	if err := InitWorkingDir(root); err != nil {
		t.Fatal(err)
	}
	writeIntegrityFile(t, filepath.Join(root, WorkingDirName, name), content)
	if _, err := SyncState(t.Context(), root, "baseline"); err != nil {
		t.Fatal(err)
	}
	return root
}

// TestStateFileCapOpenBoundary plants growth immediately beyond both inclusive limits.
func TestStateFileCapOpenBoundary(t *testing.T) {
	for _, tc := range []struct {
		name, before, after string
	}{
		{"entries", strings.Repeat("- [ ] a\n", 150), strings.Repeat("- [ ] a\n", 151)},
		{"bytes", strings.Repeat("a\n", 32768), strings.Repeat("a\n", 32768) + "b"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := capLedger(t, "OPEN.md", tc.before)
			writeIntegrityFile(t, filepath.Join(root, WorkingDirName, "OPEN.md"), tc.after)
			if _, err := SyncState(t.Context(), root, "over cap"); err == nil || !strings.Contains(err.Error(), "OPEN.md") {
				t.Fatalf("OPEN.md above boundary accepted: %v", err)
			}
		})
	}
}

// TestStateFileCapRatchet accepts legacy debt shrinking, then refuses its return.
func TestStateFileCapRatchet(t *testing.T) {
	root := capLedger(t, "OPEN.md", strings.Repeat("- [ ] debt\n", 170))
	path := filepath.Join(root, WorkingDirName, "OPEN.md")
	writeIntegrityFile(t, path, strings.Repeat("- [ ] debt\n", 160))
	if _, err := SyncState(t.Context(), root, "shrink"); err != nil {
		t.Fatalf("shrinking ledger refused: %v", err)
	}
	writeIntegrityFile(t, path, strings.Repeat("- [ ] debt\n", 161))
	if _, err := SyncState(t.Context(), root, "grow"); err == nil || !strings.Contains(err.Error(), "ratchet") {
		t.Fatalf("growing ledger accepted: %v", err)
	}
}

// TestStateFileCapCompletedRows refuses done work even when counts and bytes shrink.
func TestStateFileCapCompletedRows(t *testing.T) {
	for _, tc := range []struct{ name, before, after, diagnostic string }{
		{"OPEN.md", "- [ ] a\n", "- [x] a\n", "completed"},
		{"BUGS.md", defaultBugsMD() + "| `BUG-001` | padding title | p2 | open | file |  |\n",
			defaultBugsMD() + "| `BUG-001` | a | p2 | resolved | file |  |\n", "resolved"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := capLedger(t, tc.name, tc.before)
			writeIntegrityFile(t, filepath.Join(root, WorkingDirName, tc.name), tc.after)
			if _, err := SyncState(t.Context(), root, "done left live"); err == nil || !strings.Contains(err.Error(), tc.diagnostic) {
				t.Fatalf("done row accepted: %v", err)
			}
		})
	}
}

// TestStateFileCapBacklogBoundary refuses the first entry beyond the backlog limit.
func TestStateFileCapBacklogBoundary(t *testing.T) {
	root := capLedger(t, "BACKLOG.md", strings.Repeat("- [ ] later\n", 50))
	writeIntegrityFile(t, filepath.Join(root, WorkingDirName, "BACKLOG.md"), strings.Repeat("- [ ] later\n", 51))
	if _, err := SyncState(t.Context(), root, "backlog overflow"); err == nil || !strings.Contains(err.Error(), "BACKLOG.md") {
		t.Fatalf("backlog overflow accepted: %v", err)
	}
}

// TestStateFileCapByteRatchet refuses byte growth while legacy size debt is above the cap.
func TestStateFileCapByteRatchet(t *testing.T) {
	root := capLedger(t, "OPEN.md", strings.Repeat("a\n", 35000))
	path := filepath.Join(root, WorkingDirName, "OPEN.md")
	writeIntegrityFile(t, path, strings.Repeat("a\n", 34000))
	if _, err := SyncState(t.Context(), root, "shrink bytes"); err != nil {
		t.Fatal(err)
	}
	writeIntegrityFile(t, path, strings.Repeat("a\n", 34001))
	if _, err := SyncState(t.Context(), root, "grow bytes"); err == nil || !strings.Contains(err.Error(), "bytes 68002 exceed ratchet baseline 68000") {
		t.Fatalf("byte regrowth accepted: %v", err)
	}
}

// TestStateFileCapMetadataFailsClosed refuses corruption without resetting the baseline.
func TestStateFileCapMetadataFailsClosed(t *testing.T) {
	for _, bad := range []string{
		`{"version":1,"version":1,"files":{}}`, `{"files":{}}`,
		`{"version":2,"files":{}}`, `{"version":1,"files":{},"extra":true}`,
		`{"version":1,"files":{}}`, `null`,
	} {
		t.Run(bad, func(t *testing.T) {
			root := capLedger(t, "OPEN.md", "- [ ] a\n")
			path := filepath.Join(root, WorkingDirName, stateFileCapsName)
			writeIntegrityFile(t, path, bad)
			if _, err := CheckStateFileCaps(t.Context(), root, true); err == nil {
				t.Fatal("corrupt baseline accepted")
			}
			assertIntegrityFile(t, path, bad)
		})
	}
}

// TestStateFileCapReadOnlyAndCancellation refuses invalid contexts and leaves minima untouched.
func TestStateFileCapReadOnlyAndCancellation(t *testing.T) {
	root := capLedger(t, "OPEN.md", "- [ ] a\n")
	path := filepath.Join(root, WorkingDirName, stateFileCapsName)
	before := readLedgerFile(t, path)
	writeIntegrityFile(t, filepath.Join(root, WorkingDirName, "OPEN.md"), "")
	usage, err := CheckStateFileCaps(t.Context(), root, false)
	if err != nil || len(usage) != 3 || usage[0].Bytes != 0 || usage[0].Open != 0 {
		t.Fatalf("read-only measurement = %+v, %v", usage, err)
	}
	assertIntegrityFile(t, path, before)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := CheckStateFileCaps(ctx, root, true); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled check = %v", err)
	}
	var absent context.Context
	if _, err := CheckStateFileCaps(absent, root, true); err == nil {
		t.Fatal("nil context accepted")
	}
	assertIntegrityFile(t, path, before)
}

// TestStateFileCapPlantedMissingFileAppears proves planted case (a): baseline taken with
// OPEN.md missing, then OPEN.md appears with 2 open entries -> passes and records 2.
func TestStateFileCapPlantedMissingFileAppears(t *testing.T) {
	root := t.TempDir()
	if err := InitWorkingDir(root); err != nil {
		t.Fatal(err)
	}
	openPath := filepath.Join(root, WorkingDirName, "OPEN.md")
	if err := os.Remove(openPath); err != nil {
		t.Fatal(err)
	}
	if _, err := CheckStateFileCaps(t.Context(), root, true); err != nil {
		t.Fatal(err)
	}
	writeIntegrityFile(t, openPath, "- [ ] one\n- [ ] two\n")
	usage, err := CheckStateFileCaps(t.Context(), root, true)
	if err != nil {
		t.Fatalf("OPEN.md appearing after missing baseline rejected: %v", err)
	}
	var openUsage LiveStateFileUsage
	for _, u := range usage {
		if u.Name == "OPEN.md" {
			openUsage = u
			break
		}
	}
	if openUsage.Open != 2 {
		t.Fatalf("expected 2 open entries recorded, got %d", openUsage.Open)
	}
}

// TestStateFileCapUnderCapGrowthPasses proves that growth under hard caps passes without ratchet failures.
func TestStateFileCapUnderCapGrowthPasses(t *testing.T) {
	root := t.TempDir()
	if err := InitWorkingDir(root); err != nil {
		t.Fatal(err)
	}
	if _, err := CheckStateFileCaps(t.Context(), root, true); err != nil {
		t.Fatal(err)
	}
	openPath := filepath.Join(root, WorkingDirName, "OPEN.md")
	writeIntegrityFile(t, openPath, "- [ ] one\n- [ ] two\n")
	if _, err := CheckStateFileCaps(t.Context(), root, true); err != nil {
		t.Fatalf("under-cap growth rejected: %v", err)
	}
}

// TestStateFileCapPlantedShrinkThenGrow proves ratchet enforcement on a legacy file above entry cap.
func TestStateFileCapPlantedShrinkThenGrow(t *testing.T) {
	root := t.TempDir()
	if err := InitWorkingDir(root); err != nil {
		t.Fatal(err)
	}
	openPath := filepath.Join(root, WorkingDirName, "OPEN.md")
	writeIntegrityFile(t, openPath, strings.Repeat("- [ ] debt\n", OpenStateEntryCap+3))
	if _, err := CheckStateFileCaps(t.Context(), root, true); err != nil {
		t.Fatal(err)
	}
	writeIntegrityFile(t, openPath, strings.Repeat("- [ ] debt\n", OpenStateEntryCap+1))
	if _, err := CheckStateFileCaps(t.Context(), root, true); err != nil {
		t.Fatalf("shrinking legacy ledger rejected: %v", err)
	}
	writeIntegrityFile(t, openPath, strings.Repeat("- [ ] debt\n", OpenStateEntryCap+2))
	if _, err := CheckStateFileCaps(t.Context(), root, true); err == nil || !strings.Contains(err.Error(), "exceed ratchet baseline") {
		t.Fatalf("growing legacy after shrink accepted: %v", err)
	}
}

// TestStateFileCapLegacyCompletedRowRatchet proves ratchet enforcement on a legacy file with a completed row.
func TestStateFileCapLegacyCompletedRowRatchet(t *testing.T) {
	root := t.TempDir()
	if err := InitWorkingDir(root); err != nil {
		t.Fatal(err)
	}
	capsPath := filepath.Join(root, WorkingDirName, stateFileCapsName)
	writeIntegrityFile(t, capsPath, `{"version":1,"files":{"OPEN.md":{"name":"OPEN.md","open":2,"completed":1,"bytes":100}}}`)
	openPath := filepath.Join(root, WorkingDirName, "OPEN.md")
	writeIntegrityFile(t, openPath, "- [ ] one\n- [ ] two\n- [ ] three\n- [x] done\n")
	if _, err := CheckStateFileCaps(t.Context(), root, true); err == nil || !strings.Contains(err.Error(), "exceed ratchet baseline") {
		t.Fatalf("growing open on legacy completed row accepted: %v", err)
	}
}

// TestStateFileCapBugsUnderCapGrowthPasses proves that adding an open bug under cap passes.
func TestStateFileCapBugsUnderCapGrowthPasses(t *testing.T) {
	root := t.TempDir()
	if err := InitWorkingDir(root); err != nil {
		t.Fatal(err)
	}
	if _, err := CheckStateFileCaps(t.Context(), root, true); err != nil {
		t.Fatal(err)
	}
	if _, err := AddBug(root, BugEntry{Title: "new bug", Location: "internal/state"}); err != nil {
		t.Fatal(err)
	}
	if _, err := CheckStateFileCaps(t.Context(), root, true); err != nil {
		t.Fatalf("adding open bug under cap rejected: %v", err)
	}
}

// TestStateFileCapBugsLegacyResolvedRowRatchet proves that a baseline with a live resolved row ratchets open bugs.
func TestStateFileCapBugsLegacyResolvedRowRatchet(t *testing.T) {
	root := t.TempDir()
	if err := InitWorkingDir(root); err != nil {
		t.Fatal(err)
	}
	bugsPath := filepath.Join(root, WorkingDirName, "BUGS.md")
	writeIntegrityFile(t, bugsPath, defaultBugsMD()+"| `BUG-001` | resolved | p2 | resolved | internal/state |  |\n")
	if _, err := CheckStateFileCaps(t.Context(), root, true); err != nil {
		t.Fatal(err)
	}
	writeIntegrityFile(t, bugsPath, defaultBugsMD()+"| `BUG-001` | resolved | p2 | resolved | internal/state |  |\n| `BUG-002` | open | p2 | open | internal/state |  |\n")
	if _, err := CheckStateFileCaps(t.Context(), root, true); err == nil || !strings.Contains(err.Error(), "exceed ratchet baseline") {
		t.Fatalf("adding open bug on legacy resolved baseline accepted: %v", err)
	}
}

// TestStateFileCapBacklogUnderCapAndBoundary proves backlog growth passes under cap and fails at cap+1.
func TestStateFileCapBacklogUnderCapAndBoundary(t *testing.T) {
	root := t.TempDir()
	if err := InitWorkingDir(root); err != nil {
		t.Fatal(err)
	}
	if _, err := CheckStateFileCaps(t.Context(), root, true); err != nil {
		t.Fatal(err)
	}
	backlogPath := filepath.Join(root, WorkingDirName, "BACKLOG.md")
	writeIntegrityFile(t, backlogPath, strings.Repeat("- [ ] later\n", BacklogStateEntryCap))
	if _, err := CheckStateFileCaps(t.Context(), root, true); err != nil {
		t.Fatalf("backlog growth up to cap rejected: %v", err)
	}
	writeIntegrityFile(t, backlogPath, strings.Repeat("- [ ] later\n", BacklogStateEntryCap+1))
	if _, err := CheckStateFileCaps(t.Context(), root, true); err == nil || !strings.Contains(err.Error(), "exceed cap") {
		t.Fatalf("backlog overflow accepted: %v", err)
	}
}

// TestStateFileCapLegacyCompletedRowGrowthRefused proves that growing completed rows above a legacy baseline is refused, while the same count passes.
func TestStateFileCapLegacyCompletedRowGrowthRefused(t *testing.T) {
	root := t.TempDir()
	if err := InitWorkingDir(root); err != nil {
		t.Fatal(err)
	}
	capsPath := filepath.Join(root, WorkingDirName, stateFileCapsName)
	writeIntegrityFile(t, capsPath, `{"version":1,"files":{"OPEN.md":{"name":"OPEN.md","open":2,"completed":1,"bytes":100}}}`)
	openPath := filepath.Join(root, WorkingDirName, "OPEN.md")
	// Same count of completed rows (1 completed, 2 open) passes.
	writeIntegrityFile(t, openPath, "- [ ] one\n- [ ] two\n- [x] done\n")
	if _, err := CheckStateFileCaps(t.Context(), root, true); err != nil {
		t.Fatalf("same completed count on legacy baseline rejected: %v", err)
	}
	// One more completed row (2 completed, 2 open) is refused by the ratchet.
	writeIntegrityFile(t, openPath, "- [ ] one\n- [ ] two\n- [x] done\n- [x] extra done\n")
	if _, err := CheckStateFileCaps(t.Context(), root, true); err == nil || !strings.Contains(err.Error(), "completed entries 2 exceed ratchet baseline 1") {
		t.Fatalf("growing completed rows on legacy baseline accepted: %v", err)
	}
}

// TestCheckStateFileCapsExpiredDeadlineReturnsWithoutMeasuring proves that an expired context returns a deadline error without measuring.
func TestCheckStateFileCapsExpiredDeadlineReturnsWithoutMeasuring(t *testing.T) {
	root := t.TempDir()
	expired, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Hour))
	defer cancel()
	usage, err := CheckStateFileCaps(expired, root, false)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected deadline exceeded error, got: %v", err)
	}
	if strings.Contains(fmt.Sprint(err), "measure") {
		t.Fatalf("expected error without measuring, got: %v", err)
	}
	if len(usage) != 0 {
		t.Fatalf("expected no usage measurements on expired context, got: %v", usage)
	}
}
