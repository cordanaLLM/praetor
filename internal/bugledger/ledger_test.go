package bugledger

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const ledgerFixture = "# Bug Ledger\n\n" +
	"| ID | Title | Severity | Status | Location | Resolution |\n" +
	"| :--- | :--- | :--- | :--- | :--- | :--- |\n" +
	"| `BUG-001` | Points at a live line | p1 | open | internal/x/live.go:2 |  |\n" +
	"| `BUG-002` | Points past the end | p1 | open | internal/x/live.go:900 |  |\n" +
	"| `BUG-003` | File is gone | p1 | open | internal/x/vanished.go:1 |  |\n" +
	"| `BUG-004` | Already resolved, stale location | p1 | resolved | internal/x/vanished.go:1 | fixed |\n" +
	"| `BUG-005` | Not a file location | p2 | open | core |  |\n"

func ledgerRepo(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".workingdir"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".workingdir", "BUGS.md"), []byte(ledgerFixture), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "internal", "x"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "internal", "x", "live.go"),
		[]byte("package x\n\nvar A = 1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return root
}

// Positive: the two shapes that prove a row has not been re-read are reported, and named.
func TestAudit_Positive_ReportsUnresolvableLocations(t *testing.T) {
	report, err := Audit(t.Context(), ledgerRepo(t))
	if err != nil {
		t.Fatalf("audit: %v", err)
	}
	if len(report.Findings) != 2 {
		t.Fatalf("expected 2 findings, got %d: %v", len(report.Findings), report.Findings)
	}
	joined := report.Findings[0].String() + report.Findings[1].String()
	if !strings.Contains(joined, "BUG-002") || !strings.Contains(joined, "past the end") {
		t.Errorf("the past-the-end row is not reported: %v", report.Findings)
	}
	if !strings.Contains(joined, "BUG-003") || !strings.Contains(joined, "no longer exists") {
		t.Errorf("the missing-file row is not reported: %v", report.Findings)
	}
}

// Negative: a resolved row is not audited, and a row whose location is not a file:line is
// counted as open but not treated as unresolvable. Many rows legitimately record "core".
func TestAudit_Negative_IgnoresResolvedRowsAndNonFileLocations(t *testing.T) {
	report, err := Audit(t.Context(), ledgerRepo(t))
	if err != nil {
		t.Fatalf("audit: %v", err)
	}
	if report.Open != 4 {
		t.Errorf("expected 4 open rows, got %d", report.Open)
	}
	if report.Locatable != 3 {
		t.Errorf("expected 3 locatable rows, got %d", report.Locatable)
	}
	for _, f := range report.Findings {
		if f.Row.ID == "BUG-004" {
			t.Error("a resolved row was audited")
		}
		if f.Row.ID == "BUG-005" {
			t.Error("a non-file location was reported as unresolvable")
		}
	}
}

// The counts are the point: a report that lists only failures cannot be told apart from one
// that read no rows at all, which is how a gate goes green having measured nothing.
func TestAudit_Positive_ReportsWhatItRead(t *testing.T) {
	report, err := Audit(t.Context(), ledgerRepo(t))
	if err != nil {
		t.Fatalf("audit: %v", err)
	}
	if report.Rows != 5 {
		t.Errorf("expected 5 rows read, got %d", report.Rows)
	}
}

// Boundary: a repository with no ledger is not a failure; a ledger whose format changed so
// that nothing parses is, because that is a silent loss of coverage rather than a clean run.
func TestAudit_Boundary_AbsentLedgerAndUnparseableLedger(t *testing.T) {
	report, err := Audit(t.Context(), t.TempDir())
	if err != nil || report.Rows != 0 {
		t.Errorf("a repository without a ledger should audit to nothing: %v %v", report, err)
	}
	root := ledgerRepo(t)
	if err := os.WriteFile(filepath.Join(root, ".workingdir", "BUGS.md"),
		[]byte("# Bug Ledger\n\nno table here\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Audit(t.Context(), root); err == nil {
		t.Fatal("a ledger that parsed to zero rows was reported as clean")
	}
}

func TestRow_Boundary_LocationParsing(t *testing.T) {
	for _, tc := range []struct {
		location string
		ok       bool
	}{
		{"internal/x/a.go:12", true},
		{"core", false},
		{"internal/x/a.go", false},
		{"internal/x/a.go:0", false},
		{"internal/x/a.go:-3", false},
		{"", false},
	} {
		if _, _, ok := (Row{Location: tc.location}).FileLine(); ok != tc.ok {
			t.Errorf("%q: parsed=%v want=%v", tc.location, ok, tc.ok)
		}
	}
}
