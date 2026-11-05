package state

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
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

// TestStateFileCapPlantedExistingEmptyThenGrows proves planted case (b): baseline
// recorded from an existing OPEN.md with 0 open, then 2 open -> still fails.
func TestStateFileCapPlantedExistingEmptyThenGrows(t *testing.T) {
	root := t.TempDir()
	if err := InitWorkingDir(root); err != nil {
		t.Fatal(err)
	}
	if _, err := CheckStateFileCaps(t.Context(), root, true); err != nil {
		t.Fatal(err)
	}
	openPath := filepath.Join(root, WorkingDirName, "OPEN.md")
	writeIntegrityFile(t, openPath, "- [ ] one\n- [ ] two\n")
	if _, err := CheckStateFileCaps(t.Context(), root, true); err == nil || !strings.Contains(err.Error(), "open entries 2 exceed ratchet baseline 0") {
		t.Fatalf("expected ratchet baseline 0 failure, got: %v", err)
	}
}

// TestStateFileCapPlantedShrinkThenGrow proves planted case (c): shrink then grow -> fails.
func TestStateFileCapPlantedShrinkThenGrow(t *testing.T) {
	root := t.TempDir()
	if err := InitWorkingDir(root); err != nil {
		t.Fatal(err)
	}
	openPath := filepath.Join(root, WorkingDirName, "OPEN.md")
	writeIntegrityFile(t, openPath, "- [ ] one\n- [ ] two\n- [ ] three\n")
	if _, err := CheckStateFileCaps(t.Context(), root, true); err != nil {
		t.Fatal(err)
	}
	writeIntegrityFile(t, openPath, "- [ ] one\n")
	if _, err := CheckStateFileCaps(t.Context(), root, true); err != nil {
		t.Fatalf("shrinking ledger rejected: %v", err)
	}
	writeIntegrityFile(t, openPath, "- [ ] one\n- [ ] two\n")
	if _, err := CheckStateFileCaps(t.Context(), root, true); err == nil || !strings.Contains(err.Error(), "exceed ratchet baseline") {
		t.Fatalf("growing after shrink accepted: %v", err)
	}
}
