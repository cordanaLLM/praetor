package hiss

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// scanDir scans dir under a bounded context and fails the test on a scanner error.
func scanDir(t *testing.T, dir string, opts ScanOptions) *ScanReport {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	rep, err := Scan(ctx, dir, opts)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	return rep
}

// writeGo places a Go source file below a fresh root and returns that root.
func writeGo(t *testing.T, name, body string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
	return dir
}

// violatingGo has exactly one HISS-02 violation: an unbounded loop.
const violatingGo = "package p\n\nfunc f() {\n\tfor {\n\t}\n}\n"

// TestScanCountsUnparsedFiles is the positive dimension: a file that yields no analyzable
// structure is recorded as unparsed rather than passing silently, and the report declares
// itself incomplete so a zero-infraction count cannot be read as a verdict.
func TestScanCountsUnparsedFiles(t *testing.T) {
	dir := writeGo(t, "broken.go", "this is not Go at all {{{\n")

	rep := scanDir(t, dir, ScanOptions{})
	if rep.Skips.Unparsed != 1 {
		t.Errorf("expected exactly one unparsed file, got %d", rep.Skips.Unparsed)
	}
	if !rep.Incomplete() {
		t.Error("a scan with an unparsed file must report itself incomplete")
	}
	if !strings.Contains(rep.CoverageEvidence(), "no analyzable structure") {
		t.Errorf("coverage evidence hides the unexamined file: %s", rep.CoverageEvidence())
	}
}

// TestScanUnparsedHidesViolations is the regression this counter exists for: breaking a
// source file removed its infractions from the baseline with no signal, so a one-character
// edit could silently lower recorded technical debt. The infractions may still disappear —
// no rule can run on an unparseable file — but the scan must no longer claim completeness.
func TestScanUnparsedHidesViolations(t *testing.T) {
	good := scanDir(t, writeGo(t, "a.go", violatingGo), ScanOptions{})
	if good.TotalInfractions == 0 {
		t.Fatalf("fixture must violate an invariant, got %+v", good.Violations)
	}
	if good.Incomplete() {
		t.Fatal("a fully parsed fixture must not report itself incomplete")
	}

	// The same file with its package clause corrupted: unparseable, so zero infractions.
	broken := scanDir(t, writeGo(t, "a.go", "packag p\n\nfunc f() {\n\tfor {\n\t}\n"), ScanOptions{})
	if broken.TotalInfractions != 0 {
		t.Logf("broken fixture still yielded %d infraction(s); the incompleteness signal is what matters", broken.TotalInfractions)
	}
	if !broken.Incomplete() {
		t.Error("corrupting a file removed its infractions while the scan still claimed completeness")
	}
}

// TestScanCompleteOnCleanTree is the negative dimension: a fully parsed tree must NOT be
// reported as incomplete, otherwise the signal is worthless because it is always on.
func TestScanCompleteOnCleanTree(t *testing.T) {
	dir := writeGo(t, "clean.go", "package p\n\nfunc f() int { return 1 }\n")

	rep := scanDir(t, dir, ScanOptions{})
	if rep.Incomplete() {
		t.Errorf("a clean fully parsed tree must report complete: unparsed=%d truncated=%v",
			rep.Skips.Unparsed, rep.Truncated)
	}
	if rep.Skips.Unparsed != 0 {
		t.Errorf("expected no unparsed files, got %d", rep.Skips.Unparsed)
	}
	if strings.Contains(rep.CoverageEvidence(), "no analyzable structure") {
		t.Errorf("clean scan must not claim unexamined files: %s", rep.CoverageEvidence())
	}
}

// TestScanIncompleteOnTruncation is the boundary dimension: truncation is the other way a
// scope goes unexamined, and it must reach the same predicate as an unparsed file so a
// caller has exactly one thing to check.
func TestScanIncompleteOnTruncation(t *testing.T) {
	dir := t.TempDir()
	body := "package p\n"
	for i := 0; i < 12; i++ {
		body += "func f" + string(rune('a'+i)) + "() {\n\tfor {\n\t}\n}\n"
	}
	if err := os.WriteFile(filepath.Join(dir, "many.go"), []byte(body), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	rep := scanDir(t, dir, ScanOptions{Cap: 2})
	if !rep.Truncated {
		t.Fatalf("Cap=2 over 12 violations must truncate, got %d infractions", rep.TotalInfractions)
	}
	if !rep.Incomplete() {
		t.Error("a truncated scan must report itself incomplete")
	}
}

// TestScanReportIncompleteNilIsIncomplete is the boundary dimension for the absent report:
// a nil report describes no examined scope at all, so it must never answer "complete".
func TestScanReportIncompleteNilIsIncomplete(t *testing.T) {
	var rep *ScanReport
	if !rep.Incomplete() {
		t.Error("a nil report must be treated as incomplete, never as a clean verdict")
	}
}
