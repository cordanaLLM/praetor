package state

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// syntheticLog renders a STATE.md body: the default preamble, then count compact entries.
func syntheticLog(count int) string {
	var b strings.Builder
	b.WriteString(defaultStateMD())
	for i := 0; i < count; i++ {
		b.WriteString("\n")
		b.WriteString(stateEntry{Time: fmt.Sprintf("2026-09-01 00:%02d:%02d UTC", i/60%60, i%60), Head: "h",
			Branch: "b", Activity: fmt.Sprintf("entry %d", i)}.render())
	}
	return b.String()
}

// rotationLedger initializes a ledger whose STATE.md holds body and no sync marker.
func rotationLedger(t *testing.T, body string) (root, path string) {
	t.Helper()
	root = t.TempDir()
	if err := InitWorkingDir(root); err != nil {
		t.Fatal(err)
	}
	path = filepath.Join(root, WorkingDirName, "STATE.md")
	writeIntegrityFile(t, path, body)
	return root, path
}

// historyArchives returns the rotation archives in the ledger directory.
func historyArchives(t *testing.T, root string) []string {
	t.Helper()
	matches, err := filepath.Glob(filepath.Join(root, WorkingDirName, stateHistoryPrefix+"*.md"))
	if err != nil {
		t.Fatal(err)
	}
	return matches
}

// Positive: a sync past the entry trigger moves the oldest entries, byte for byte, into a
// new archive file, keeps the newest stateKeepEntries plus the new entry, names the
// archive in that entry, and the sync marker still verifies (BUG-289, BUG-462).
func TestSyncStateRotatesHistoryPastTheTrigger(t *testing.T) {
	body := syntheticLog(stateRotateEntries + 1)
	root, path := rotationLedger(t, body)
	snap, err := SyncState(t.Context(), root, "")
	if err != nil {
		t.Fatal(err)
	}
	archived := stateRotateEntries + 1 - stateKeepEntries
	if snap.ArchivedEntries != archived || !strings.HasPrefix(snap.HistoryArchive, stateHistoryPrefix) {
		t.Fatalf("rotation not reported on the snapshot: %+v", snap)
	}
	archives := historyArchives(t, root)
	if len(archives) != 1 || filepath.Base(archives[0]) != snap.HistoryArchive {
		t.Fatalf("expected one archive named %q, got %v", snap.HistoryArchive, archives)
	}
	moved, err := os.ReadFile(archives[0])
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	preamble := defaultStateMD() + "\n"
	kept := strings.TrimPrefix(supersededBase(data), preamble)
	lastNew := strings.LastIndex(kept, "\n### [")
	if preamble+string(moved)+kept[:lastNew] != body {
		t.Fatal("preamble + archive + kept entries do not reassemble the original log byte for byte")
	}
	if got := strings.Count(text, "\n### ["); got != stateKeepEntries+1 {
		t.Fatalf("kept %d entries, want %d", got, stateKeepEntries+1)
	}
	if !strings.Contains(text, fmt.Sprintf("- sync; archived %d entries to %s | ", archived, snap.HistoryArchive)) {
		t.Fatalf("new entry does not name the archive:\n%s", kept[lastNew:])
	}
	if err := VerifyStateSync(t.Context(), root); err != nil {
		t.Fatalf("marker does not verify after rotation: %v", err)
	}
}

// Boundary: a log exactly at the entry trigger is appended to, not rewritten, and leaves no
// archive; a log over the byte trigger with few entries rotates by size, archiving an
// entry larger than the keep bound whole rather than splitting or keeping it.
func TestSyncStateRotationBoundaries(t *testing.T) {
	body := syntheticLog(stateRotateEntries)
	root, path := rotationLedger(t, body)
	if _, err := SyncState(t.Context(), root, "at trigger"); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(data), body) || len(historyArchives(t, root)) != 0 {
		t.Fatal("a log at the trigger was rewritten or archived")
	}

	huge := "\n### [note] hand written\n" + strings.Repeat("x", stateRotateBytes) + "\n"
	body = syntheticLog(2) + huge
	root, path = rotationLedger(t, body)
	snap, err := SyncState(t.Context(), root, "")
	if err != nil {
		t.Fatal(err)
	}
	if snap.ArchivedEntries != 3 {
		t.Fatalf("an oversized newest entry must be archived whole with the rest: %+v", snap)
	}
	moved, err := os.ReadFile(historyArchives(t, root)[0])
	if err != nil || !strings.HasSuffix(string(moved), strings.TrimPrefix(huge, "\n")) {
		t.Fatalf("oversized entry not archived verbatim: %v", err)
	}
	if data, err = os.ReadFile(path); err != nil || len(data) > stateKeepBytes || strings.Count(string(data), "\n### [") != 1 {
		t.Fatalf("rotation by size did not converge: %d bytes, %v", len(data), err)
	}
}

// Negative: an entry whose --log text alone exceeds the one-entry bound is refused before
// anything is written, and an archive name that already exists is never replaced.
func TestSyncStateRefusesOversizedEntryAndArchiveCollision(t *testing.T) {
	body := syntheticLog(3)
	root, path := rotationLedger(t, body)
	if _, err := SyncState(t.Context(), root, strings.Repeat("y ", maxStateEntryBytes)); err == nil || !strings.Contains(err.Error(), "bound for one entry") {
		t.Fatalf("an oversized entry was accepted: %v", err)
	}
	assertIntegrityFile(t, path, body)

	stamp := time.Date(2026, 9, 26, 10, 0, 0, 1, time.UTC)
	name, err := archiveStateHistory(t.Context(), root, "first\n", stamp)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := archiveStateHistory(t.Context(), root, "second\n", stamp); err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("an existing archive was not refused: %v", err)
	}
	assertIntegrityFile(t, filepath.Join(root, WorkingDirName, name), "first\n")
	if strings.ContainsAny(name, `:\/`) {
		t.Fatalf("archive name %q is not a portable flat filename", name)
	}
}

// Boundary: the rotation plan keeps the preamble, never splits an entry and leaves an
// empty or preamble-only log alone.
func TestPlanStateRotation(t *testing.T) {
	for _, body := range []string{"", defaultStateMD(), syntheticLog(stateRotateEntries)} {
		plan, err := planStateRotation(body)
		if err != nil || plan.entries != 0 || plan.body != body {
			t.Fatalf("log within the triggers was changed: %+v, %v", plan, err)
		}
	}
	body := syntheticLog(stateRotateEntries + 50)
	plan, err := planStateRotation(body)
	if err != nil || plan.entries != stateRotateEntries+50-stateKeepEntries {
		t.Fatalf("unexpected plan: %d entries, %v", plan.entries, err)
	}
	if !strings.HasPrefix(plan.archived, "### [") || !strings.HasPrefix(plan.body, defaultStateMD()) {
		t.Fatal("plan split an entry or dropped the preamble")
	}
	if got := strings.Count(plan.body, "\n### ["); got != stateKeepEntries {
		t.Fatalf("plan keeps %d entries", got)
	}
}
