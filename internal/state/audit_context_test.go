package state

import (
	"context"
	"testing"
)

// Positive: a live context audits a fresh ledger. Negative: an unresolved P0 row is reported
// as a violation. Boundary: a cancelled context ends the audit with an error instead of a
// report.
func TestAuditWorkingDirContext(t *testing.T) {
	root := t.TempDir()
	if _, err := SyncState(context.Background(), root, "audit context fixture"); err != nil {
		t.Fatalf("sync: %v", err)
	}
	report, err := AuditWorkingDirContext(context.Background(), root)
	if err != nil || !report.Valid {
		t.Fatalf("fresh ledger: %+v, %v", report, err)
	}
	if _, err := AddBug(root, BugEntry{Title: "p0 fixture", Severity: "p0"}); err != nil {
		t.Fatalf("add bug: %v", err)
	}
	report, err = AuditWorkingDirContext(context.Background(), root)
	if err != nil || report.Valid || report.P0Bugs != 1 {
		t.Fatalf("unresolved P0: %+v, %v", report, err)
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := AuditWorkingDirContext(cancelled, root); err == nil {
		t.Fatal("a cancelled context must end the audit with an error")
	}
}
