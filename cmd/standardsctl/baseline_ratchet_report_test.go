package main

import (
	"fmt"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/baseline"
)

// discardsSource is a Go file carrying n HISS-07 infractions, one discarded call result per line.
func discardsSource(n int) string {
	var b strings.Builder
	b.WriteString("package legacy\n\nfunc discards() {\n")
	for i := 0; i < n; i++ {
		b.WriteString("\t_ = failing()\n")
	}
	b.WriteString("}\n\nfunc failing() error { return nil }\n")
	return b.String()
}

// Positive (#598): a cut rejection says how many lines it hid and names the read-only command,
// and that command lists every violation without touching the baseline.
func TestBaselineVerify_Positive_AllViolationsListsEveryOne(t *testing.T) {
	f := newAuditFixture(t)
	writeFixtureFile(t, f.dir, "discards.go", discardsSource(5))
	data, mod := fileSnapshot(t, f.baselinePath)

	_, err := runBaselineCmd(t, f, "--verify")
	mustErrContain(t, err, "... and 2 more unbaselined violations not shown; 'praetorctl baseline --verify --all-violations' lists every one")
	if n := strings.Count(err.Error(), "discards.go:"); n != 3 {
		t.Errorf("bounded rejection listed %d lines, want 3:\n%v", n, err)
	}

	_, err = runBaselineCmd(t, f, "--verify", "--all-violations")
	if err == nil || strings.Contains(err.Error(), "not shown") {
		t.Fatalf("--all-violations must fail listing every line, got %v", err)
	}
	for line := 4; line <= 8; line++ {
		mustErrContain(t, err, fmt.Sprintf("discards.go:%d", line))
	}
	assertUnchanged(t, f.baselinePath, data, mod)
}

// Negative: --all-violations belongs to --verify and is refused without it; a passing verify
// prints no marker. The audit accepts the same flag.
func TestBaselineVerify_Negative_AllViolationsNeedsVerify(t *testing.T) {
	f := newAuditFixture(t)
	for _, args := range [][]string{{"--all-violations"}, {"--record", "--all-violations"}} {
		_, err := runBaselineCmd(t, f, args...)
		mustErrContain(t, err, "pass it together with --verify")
	}
	out, err := runBaselineCmd(t, f, "--verify", "--all-violations")
	if err != nil || strings.Contains(out, "not shown") {
		t.Fatalf("a clean verify with --all-violations: %v\n%s", err, out)
	}

	writeFixtureFile(t, f.dir, "discards.go", discardsSource(5))
	_, err = f.audit(t, "--all-violations")
	if err == nil || strings.Contains(err.Error(), "not shown") || strings.Count(err.Error(), "discards.go:") != 5 {
		t.Fatalf("audit --all-violations must list all five, got %v", err)
	}
}

// Boundary (#598): one violation over the bound hides exactly one, in the singular.
func TestBaselineVerify_Boundary_OneOverTheBound(t *testing.T) {
	f := newAuditFixture(t)
	writeFixtureFile(t, f.dir, "discards.go", discardsSource(4))
	_, err := runBaselineCmd(t, f, "--verify")
	mustErrContain(t, err, "... and 1 more unbaselined violation not shown")
}

// writeBaselineAt records infractions as the fixture baseline recorded at commit.
func (f *auditFixture) writeBaselineAt(t *testing.T, commit string, infractions []baseline.Infraction) {
	t.Helper()
	b := &baseline.Baseline{Version: 1, Repository: "acme/widgets", CommitSHA: commit, Infractions: append([]baseline.Infraction{}, infractions...)}
	if err := baseline.SaveBaseline(f.baselinePath, b); err != nil {
		t.Fatalf("write baseline: %v", err)
	}
}

// fixtureHead returns the fixture's HEAD commit.
func (f *auditFixture) fixtureHead(t *testing.T) string {
	t.Helper()
	out, err := runFixtureGit(t, f.dir, f.gitEnv, "rev-parse", "HEAD")
	if err != nil {
		t.Fatalf("rev-parse: %v (%s)", err, out)
	}
	return strings.TrimSpace(out)
}

// Positive (#599): a finding in code the baseline's commit already held, which the baseline does
// not record, is attributed to a changed check with the remediation, in verify and in audit.
func TestBaselineVerify_Positive_UnchangedCodeBlamesTheCheck(t *testing.T) {
	f := newAuditFixture(t)
	f.addViolation(t)
	gitCommitAll(t, f.dir, f.gitEnv, "legacy code")
	head := f.fixtureHead(t)
	// An engine without the check recorded nothing at this commit.
	f.writeBaselineAt(t, head, nil)
	gitCommitAll(t, f.dir, f.gitEnv, "baseline")

	_, err := runBaselineCmd(t, f, "--verify")
	mustErrContain(t, err, "[HISS-07] legacy.go:4")
	mustErrContain(t, err, "(check added or changed since the baseline)")
	mustErrContain(t, err, "sit in code the current checks flag at "+head[:12])
	mustErrContain(t, err, "'praetorctl baseline --record --allow-increase --reason=<why>'")
	if strings.Contains(err.Error(), "(new)") {
		t.Errorf("unchanged code reported as new:\n%v", err)
	}
	_, err = f.audit(t)
	mustErrContain(t, err, "(check added or changed since the baseline)")
}

// Negative (#599): a finding the baseline's commit does not hold is introduced, beside one the
// commit held; the ratchet still fails.
func TestBaselineVerify_Negative_ChangedCodeIsIntroduced(t *testing.T) {
	f := newAuditFixture(t)
	f.addViolation(t)
	gitCommitAll(t, f.dir, f.gitEnv, "legacy code")
	f.writeBaselineAt(t, f.fixtureHead(t), nil)
	gitCommitAll(t, f.dir, f.gitEnv, "baseline")
	writeFixtureFile(t, f.dir, "discards.go", discardsSource(1))

	_, err := runBaselineCmd(t, f, "--verify")
	mustErrContain(t, err, "[HISS-07] discards.go:4 - ")
	mustErrContain(t, err, "(new)")
	mustErrContain(t, err, "2 unbaselined (1 introduced, 1 from checks added or changed since the baseline)")
}

// Boundary (#599): a baseline that records no commit, as one written before the field was
// filled, gets the neutral wording, the reason and the remediation, never "introduced".
func TestBaselineVerify_Boundary_NoRecordedCommitIsNeutral(t *testing.T) {
	f := newAuditFixture(t)
	f.addViolation(t)
	gitCommitAll(t, f.dir, f.gitEnv, "legacy code")

	_, err := runBaselineCmd(t, f, "--verify")
	mustErrContain(t, err, "HISS invariant violations the baseline does not record")
	mustErrContain(t, err, "(not in the baseline)")
	mustErrContain(t, err, "(the baseline records no commit to compare against)")
	mustErrContain(t, err, "'praetorctl baseline --record --allow-increase --reason=<why>'")
	if strings.Contains(err.Error(), "introduced") || strings.Contains(err.Error(), "(new)") {
		t.Errorf("a baseline without a commit blamed the change:\n%v", err)
	}
}

// Positive (#29, #599): lines added above a recorded finding move its fingerprint, not its debt.
// The rejection names the line the baseline records, never an untraced reason or
// --allow-increase, and the plain re-record it names does clear it.
func TestBaselineVerify_Positive_MovedFindingNeedsOnlyARerecord(t *testing.T) {
	f := newAuditFixture(t)
	f.addViolation(t)
	if out, err := runBaselineCmd(t, f, "--record", "--allow-increase", "--reason=legacy debt inventory"); err != nil {
		t.Fatalf("record the legacy finding: %v\n%s", err, out)
	}
	writeFixtureFile(t, f.dir, "legacy.go", strings.Replace(legacyGoSource, "func legacy", "// moved\n// down\nfunc legacy", 1))

	_, err := runBaselineCmd(t, f, "--verify")
	mustErrContain(t, err, "HISS invariant violations the baseline records at other lines (1 total infractions, 1 moved, 0 in touched files):")
	mustErrContain(t, err, "[HISS-07] legacy.go:6 - Legacy unchecked error assignment (recorded in the baseline at line 4)")
	mustErrContain(t, err, "re-record the baseline with 'praetorctl baseline --record', which needs no --allow-increase for them")
	for _, wrong := range []string{"not traced", "(not in the baseline)", "(new)", "--reason=<why>"} {
		if strings.Contains(err.Error(), wrong) {
			t.Errorf("a moved finding's rejection contains %q:\n%v", wrong, err)
		}
	}

	if out, err := runBaselineCmd(t, f, "--record"); err != nil {
		t.Fatalf("the plain re-record the rejection names failed: %v\n%s", err, out)
	}
	if out, err := runBaselineCmd(t, f, "--verify"); err != nil {
		t.Fatalf("verify after the re-record: %v\n%s", err, out)
	}
}
