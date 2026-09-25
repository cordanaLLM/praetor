package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/baseline"
)

// runBaselineCmd dispatches the baseline command for the fixture with extra flags.
func runBaselineCmd(t *testing.T, f *auditFixture, extra ...string) (string, error) {
	t.Helper()
	args := append([]string{"--file=" + f.baselinePath}, extra...)
	return captureStdout(t, func() error { return dispatchCommand("baseline", args) })
}

func TestBaselineRecord_Positive(t *testing.T) {
	f := newAuditFixture(t)
	inf := f.addViolation(t)

	// Growth from 0 to 1 needs an explicit, justified increase.
	out, err := runBaselineCmd(t, f, "--record", "--allow-increase", "--reason=legacy debt inventory")
	if err != nil {
		t.Fatalf("record with rationale: %v\n%s", err, out)
	}
	mustContain(t, out, "Total: 1 infractions, previously 0", "[WARN] Debt increased deliberately")
	b, err := baseline.LoadBaseline(f.baselinePath)
	if err != nil {
		t.Fatal(err)
	}
	if b.TotalInfractions != 1 || b.Infractions[0].Fingerprint != inf.Fingerprint || b.IncreaseRationale != "legacy debt inventory" {
		t.Fatalf("unexpected recorded baseline: %+v", b)
	}

	// Fixing the debt re-records without the rationale.
	if err := os.Remove(filepath.Join(f.dir, "legacy.go")); err != nil {
		t.Fatal(err)
	}
	out, err = runBaselineCmd(t, f, "--record")
	if err != nil {
		t.Fatalf("record decrease: %v\n%s", err, out)
	}
	mustContain(t, out, "Total: 0 infractions, previously 1")
	b, err = baseline.LoadBaseline(f.baselinePath)
	if err != nil {
		t.Fatal(err)
	}
	if b.TotalInfractions != 0 || b.IncreaseRationale != "" {
		t.Fatalf("expected a clean baseline without rationale, got %+v", b)
	}

	// Inspect prints the recorded state.
	out, err = runBaselineCmd(t, f)
	if err != nil {
		t.Fatalf("inspect: %v", err)
	}
	mustContain(t, out, "=== acme/widgets Technical Debt Baseline ===", "Total Infractions: 0",
		"Zero technical debt recorded in this baseline.", "not a live scan")
	// Inspect never scans, so it never claims compliance (BUG-802).
	if strings.Contains(out, "compliant") {
		t.Fatalf("inspect claimed compliance from a stored count:\n%s", out)
	}
}

// TestBaselineInspect_MissingFileIsNotReportedAsCompliant pins BUG-802: a missing
// baseline and a recorded zero used to print the same "100% compliant" line.
func TestBaselineInspect_MissingFileIsNotReportedAsCompliant(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "absent.json")
	out, err := captureStdout(t, func() error {
		return dispatchCommand("baseline", []string{"--file=" + missing})
	})
	if err != nil {
		t.Fatalf("inspecting a missing baseline: %v", err)
	}
	mustContain(t, out, "No baseline file at "+missing, "nothing was scanned", "baseline --record")
	for _, claim := range []string{"compliant", "Zero technical debt", "Total Infractions"} {
		if strings.Contains(out, claim) {
			t.Errorf("missing baseline output contains %q:\n%s", claim, out)
		}
	}
	if _, statErr := os.Stat(missing); !os.IsNotExist(statErr) {
		t.Fatalf("inspect created the baseline file (stat err %v)", statErr)
	}
}

func TestBaselineRecord_Negative(t *testing.T) {
	f := newAuditFixture(t)
	f.addViolation(t)

	_, err := runBaselineCmd(t, f, "--record")
	mustErrContain(t, err, "--allow-increase")
	_, err = runBaselineCmd(t, f, "--record", "--allow-increase")
	mustErrContain(t, err, "rationale")

	// The refused records left the file untouched.
	b, err := baseline.LoadBaseline(f.baselinePath)
	if err != nil {
		t.Fatal(err)
	}
	if b.TotalInfractions != 0 {
		t.Fatalf("refused record must not write, got %d infractions", b.TotalInfractions)
	}

	_, err = runBaselineCmd(t, f, "extra")
	mustErrContain(t, err, "no positional arguments")

	// A directory is not a baseline file.
	_, err = captureStdout(t, func() error { return dispatchCommand("baseline", []string{"--file=" + f.dir}) })
	mustErrContain(t, err, "failed to load baseline")
}

func TestBaselineRecord_Boundary(t *testing.T) {
	f := newAuditFixture(t)

	// Zero to zero is a no-op record.
	out, err := runBaselineCmd(t, f, "--record")
	if err != nil {
		t.Fatalf("zero record: %v", err)
	}
	mustContain(t, out, "Total: 0 infractions, previously 0")

	// Inspect shows a recorded rationale and every infraction.
	f.writeBaseline(t, []baseline.Infraction{{RuleID: "HISS-07", FilePath: "x.go", LineNumber: 1, Symbol: "f", Message: "m", Fingerprint: "x.go:1:HISS-07"}}, "why")
	out, err = runBaselineCmd(t, f)
	if err != nil {
		t.Fatalf("inspect: %v", err)
	}
	mustContain(t, out, "Recorded increase rationale: why", "#1 [HISS-07] x.go:1 (f) - m")

	// A missing baseline file inspects as empty.
	_, err = captureStdout(t, func() error {
		return dispatchCommand("baseline", []string{"--file=" + filepath.Join(t.TempDir(), "absent.json")})
	})
	if err != nil {
		t.Fatalf("missing baseline must inspect as empty: %v", err)
	}
}
