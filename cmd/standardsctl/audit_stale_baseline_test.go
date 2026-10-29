package main

import (
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/baseline"
)

// staleEntry is a baseline entry for a file the fixture does not hold: debt a cleanup removed
// without a re-record (#349).
var staleEntry = baseline.Infraction{RuleID: "HISS-07", FilePath: "cleaned.go", LineNumber: 1, Fingerprint: "cleaned.go:1:HISS-07"}

// Positive (#349): a baseline looser than the tree passes the audit and verify, and both name the
// stale count and the re-record; without a stated bound the audit says it only reports.
func TestAudit_Positive_StaleBaselineEntriesReported(t *testing.T) {
	f := newAuditFixture(t)
	inf := f.addViolation(t)
	f.writeBaseline(t, []baseline.Infraction{inf, staleEntry}, "")
	gitCommitAll(t, f.dir, f.gitEnv, "stale baseline fixture")

	out, err := f.audit(t)
	if err != nil {
		t.Fatalf("a stale baseline without a bound must pass: %v\n%s", err, out)
	}
	mustContain(t, out,
		"[WARN] 1 baseline entry matches nothing in the tree",
		"tighten the baseline with 'praetorctl baseline --record'",
		"(reported only; --max-stale-baseline-entries=<n> fails the audit past n)",
	)
	out, err = runBaselineCmd(t, f, "--verify")
	if err != nil {
		t.Fatalf("verify of a stale baseline must pass: %v\n%s", err, out)
	}
	mustContain(t, out, "[WARN] 1 baseline entry matches nothing in the tree")
}

// Negative (#349): past the stated bound the audit fails with the count and the bound, and a
// negative bound is refused before anything is scanned.
func TestAudit_Negative_StaleBaselinePastTheBoundFails(t *testing.T) {
	f := newAuditFixture(t)
	inf := f.addViolation(t)
	f.writeBaseline(t, []baseline.Infraction{inf, staleEntry}, "")
	gitCommitAll(t, f.dir, f.gitEnv, "stale baseline fixture")

	_, err := f.audit(t, "--max-stale-baseline-entries=0")
	mustErrContain(t, err, "[FAIL] 1 baseline entry matches nothing in the tree")
	mustErrContain(t, err, "--max-stale-baseline-entries=0 allows at most 0")

	_, err = f.audit(t, "--max-stale-baseline-entries=-1")
	mustErrContain(t, err, "--max-stale-baseline-entries must be 0 or more, got -1")
}

// Boundary (#349): a count equal to the bound passes and says so, and a baseline that matches the
// tree passes a zero bound without a warning: an unchanged tree keeps passing.
func TestAudit_Boundary_StaleBaselineAtTheBound(t *testing.T) {
	f := newAuditFixture(t)
	inf := f.addViolation(t)
	f.writeBaseline(t, []baseline.Infraction{inf, staleEntry}, "")
	gitCommitAll(t, f.dir, f.gitEnv, "stale baseline fixture")

	out, err := f.audit(t, "--max-stale-baseline-entries=1")
	if err != nil {
		t.Fatalf("a stale count at the bound must pass: %v\n%s", err, out)
	}
	mustContain(t, out, "(within --max-stale-baseline-entries=1)")

	f.writeBaseline(t, []baseline.Infraction{inf}, "")
	gitCommitAll(t, f.dir, f.gitEnv, "tightened baseline")
	out, err = f.audit(t, "--max-stale-baseline-entries=0")
	if err != nil || strings.Contains(out, "matches nothing") {
		t.Fatalf("a baseline matching the tree must pass a zero bound silently: %v\n%s", err, out)
	}
}

// Positive (#348): a touched file whose findings the baseline records reports them as baselined in
// the touched count, and says why they fail anyway.
func TestAudit_Positive_TouchedCountNamesBaselined(t *testing.T) {
	f := newAuditFixture(t)
	inf := f.addViolation(t)
	f.writeBaseline(t, []baseline.Infraction{inf}, "")
	gitCommitAll(t, f.dir, f.gitEnv, "baselined legacy fixture")

	_, err := f.audit(t, "--touched=legacy.go")
	mustErrContain(t, err, "1 in touched files (0 not in the baseline, 1 baselined)")
	mustErrContain(t, err, "(touched file must be clean, baselined)")
	mustErrContain(t, err, "1 of the touched-file violations are baselined: touching a file revokes its baseline exemptions")
}

// Order engine-hiss-unenforced-checks-20261027: HISS-11 entries in the baseline are owned by
// the supply-chain image scanner, not the HISS AST sweep. Neither praetorctl baseline --verify
// nor praetorctl audit's AST sweep counts them as stale or fails a zero-stale bound.
func TestAudit_Positive_HISS11BaselineEntriesNotStale(t *testing.T) {
	f := newAuditFixture(t)
	writeFixtureFile(t, f.dir, "Dockerfile", "FROM golang:1.27-alpine\n")
	f.writeBaseline(t, []baseline.Infraction{
		{
			RuleID:      baseline.ImageRefRuleID,
			FilePath:    "Dockerfile",
			LineNumber:  1,
			Message:     "golang:1.27-alpine",
			Fingerprint: "Dockerfile:HISS-11:image:golang:1.27-alpine",
		},
	}, "")
	gitCommitAll(t, f.dir, f.gitEnv, "hiss11 baseline fixture")

	out, err := f.audit(t, "--max-stale-baseline-entries=0")
	if err != nil {
		t.Fatalf("audit failed on HISS-11 baseline entry: %v\n%s", err, out)
	}
	if strings.Contains(out, "matches nothing") {
		t.Fatalf("audit counted HISS-11 baseline entry as stale:\n%s", out)
	}

	out, err = runBaselineCmd(t, f, "--verify")
	if err != nil {
		t.Fatalf("baseline --verify failed: %v\n%s", err, out)
	}
	if strings.Contains(out, "matches nothing") {
		t.Fatalf("baseline --verify counted HISS-11 entry as stale:\n%s", out)
	}
	mustContain(t, out, "0 active infractions within the 1 recorded")
}
