package state

import (
	"context"
	"encoding/json/v2"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

var migrationBugs = []BugEntry{
	{ID: "BUG-001", Title: "  pipes | edge  ", Severity: "p0", Status: "open", Location: "a.go:1",
		Context: "context\nwith | evidence & λ", CreatedAt: time.Date(2026, 9, 12, 12, 1, 2, 3, time.UTC)},
	{ID: "BUG-002", Title: "offset time", Severity: "p2", Status: "resolved", Location: "core", Resolution: "fixed\nproof",
		CreatedAt:  time.Date(2026, 9, 12, 12, 0, 0, 0, time.FixedZone("", 7200)),
		ResolvedAt: time.Date(2026, 9, 13, 1, 2, 3, 999999999, time.UTC)},
	{ID: "BUG-004", Title: "empty context", Severity: "p3", Status: "deferred"},
}

const legacyBugRow = "| `BUG-003` | literal \\n & &#124; | p1 | open | file:1 |  |"

// migrationLedger writes inline v1 rows, one legacy row and prose, then syncs.
func migrationLedger(t *testing.T, bugs []BugEntry) (root, ledger string) {
	t.Helper()
	md, err := RenderBugsMarkdownStrict(bugs)
	if err != nil {
		t.Fatal(err)
	}
	if len(bugs) > 1 {
		md = strings.Replace(md, "\n| `BUG-002`", "\n"+legacyBugRow+"\n| `BUG-002`", 1)
	}
	root = writeBugFixture(t, "# Notes\n\n"+md+"\n## Human notes\nKeep this.\n")
	if _, err := SyncState(t.Context(), root, "fixture"); err != nil {
		t.Fatal(err)
	}
	return root, filepath.Join(root, WorkingDirName, bugLedgerName)
}

func canonicalBugs(t *testing.T, root string) string {
	t.Helper()
	bugs, err := ListBugs(root, "all")
	if err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(bugs, json.Deterministic(true))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func readLedgerFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestMigrateBugMetadataRoundTripAndIdempotence(t *testing.T) {
	root, ledger := migrationLedger(t, migrationBugs)
	before := canonicalBugs(t, root)
	report, err := MigrateBugMetadata(t.Context(), root)
	if err != nil {
		t.Fatal(err)
	}
	if !report.Changed || report.Rows != 4 || report.Migrated != 3 || report.BytesAfter >= report.BytesBefore || report.SidecarBytes == 0 {
		t.Fatalf("unexpected report: %+v", report)
	}
	text := readLedgerFile(t, ledger)
	if strings.Contains(text, "praetor-bug:v1") || strings.Count(text, bugMetadataRef) != 3 ||
		!strings.Contains(text, legacyBugRow+"\n") || !strings.HasPrefix(text, "# Notes\n") || !strings.HasSuffix(text, "## Human notes\nKeep this.\n") {
		t.Fatalf("migrated ledger wrong:\n%s", text)
	}
	if after := canonicalBugs(t, root); after != before {
		t.Fatalf("round trip changed records:\n%s\n%s", before, after)
	}
	if err := VerifyStateSync(t.Context(), root); err != nil {
		t.Fatalf("migration did not resync: %v", err)
	}
	sidecar := readLedgerFile(t, filepath.Join(root, WorkingDirName, bugMetaName))
	again, err := MigrateBugMetadata(t.Context(), root)
	if err != nil || again.Changed || again.Migrated != 0 {
		t.Fatalf("second migration not a no-op: %+v, %v", again, err)
	}
	assertIntegrityFile(t, ledger, text)
	assertIntegrityFile(t, filepath.Join(root, WorkingDirName, bugMetaName), sidecar)
}

func TestMigrateBugMetadataRefusesMismatchAndStaleLedger(t *testing.T) {
	root, ledger := migrationLedger(t, migrationBugs)
	original := readLedgerFile(t, ledger)
	faulty := func(bug BugEntry) (string, error) {
		bug.Title += " (changed)"
		return encodeBugRow(bug)
	}
	if _, err := migrateBugMetadata(t.Context(), root, faulty); err == nil || !strings.Contains(err.Error(), "round trip") {
		t.Fatalf("mismatching migration accepted: %v", err)
	}
	assertIntegrityFile(t, ledger, original)
	if _, err := os.Stat(filepath.Join(root, WorkingDirName, bugMetaName)); !os.IsNotExist(err) {
		t.Fatalf("refused migration wrote a sidecar: %v", err)
	}
	if err := AddTask(root, "unsynchronized"); err != nil {
		t.Fatal(err)
	}
	if _, err := MigrateBugMetadata(t.Context(), root); err == nil || !strings.Contains(err.Error(), "unverified") {
		t.Fatalf("stale ledger migrated: %v", err)
	}
	assertIntegrityFile(t, ledger, original)
	var missing context.Context
	if _, err := MigrateBugMetadata(missing, root); err == nil {
		t.Fatal("nil context accepted")
	}
}

func TestMigrateBugMetadataBoundaries(t *testing.T) {
	t.Run("empty ledger", func(t *testing.T) {
		root, ledger := migrationLedger(t, nil)
		original := readLedgerFile(t, ledger)
		report, err := MigrateBugMetadata(t.Context(), root)
		if err != nil || report.Changed || report.Rows != 0 {
			t.Fatalf("empty ledger: %+v, %v", report, err)
		}
		assertIntegrityFile(t, ledger, original)
		if _, err := os.Stat(filepath.Join(root, WorkingDirName, bugMetaName)); !os.IsNotExist(err) {
			t.Fatalf("empty migration created a sidecar: %v", err)
		}
	})
	t.Run("one row", func(t *testing.T) {
		root, _ := migrationLedger(t, migrationBugs[:1])
		before := canonicalBugs(t, root)
		report, err := MigrateBugMetadata(t.Context(), root)
		if err != nil || report.Migrated != 1 || canonicalBugs(t, root) != before {
			t.Fatalf("one row: %+v, %v", report, err)
		}
	})
	t.Run("large ledger", func(t *testing.T) {
		bugs := make([]BugEntry, 2000)
		for i := range bugs {
			bugs[i] = BugEntry{ID: fmt.Sprintf("BUG-%03d", i+1001), Title: "row", Severity: "p2", Status: "open",
				Context: strings.Repeat("c", 200), CreatedAt: time.Date(2026, 9, 1, 0, 0, i, 0, time.UTC)}
		}
		root, ledger := migrationLedger(t, bugs)
		before := canonicalBugs(t, root)
		report, err := MigrateBugMetadata(t.Context(), root)
		if err != nil || report.Migrated != len(bugs) || canonicalBugs(t, root) != before {
			t.Fatalf("large ledger: %+v, %v", report, err)
		}
		if size := len(readLedgerFile(t, ledger)); size*2 > report.BytesBefore {
			t.Fatalf("ledger did not shrink by half: %d of %d bytes", size, report.BytesBefore)
		}
	})
}

func TestMigrateBugMetadataRefusesOversizedSidecar(t *testing.T) {
	big := migrationBugs[0]
	big.Context = strings.Repeat("x", maxLedgerFieldBytes)
	root, ledger := migrationLedger(t, []BugEntry{big})
	// 63 records of 16 KiB stay under the 1 MiB sidecar bound; one more crosses it.
	orphans := ledgerMetaIndex{}
	for i := 0; i < 63; i++ {
		orphans[fmt.Sprintf("BUG-%03d", 900+i)] = ledgerMetadata{Context: strings.Repeat("o", maxLedgerFieldBytes)}
	}
	sidecar, err := encodeBugMeta(orphans)
	if err != nil {
		t.Fatal(err)
	}
	sidecarPath := filepath.Join(root, WorkingDirName, bugMetaName)
	writeIntegrityFile(t, sidecarPath, string(sidecar))
	if _, err := SyncState(t.Context(), root, "orphans"); err != nil {
		t.Fatal(err)
	}
	original := readLedgerFile(t, ledger)
	if _, err := MigrateBugMetadata(t.Context(), root); err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("migration past the sidecar bound accepted: %v", err)
	}
	if _, err := AddBug(root, big); err == nil {
		t.Fatal("write past the sidecar bound accepted")
	}
	assertIntegrityFile(t, ledger, original)
	assertIntegrityFile(t, sidecarPath, string(sidecar))
}
