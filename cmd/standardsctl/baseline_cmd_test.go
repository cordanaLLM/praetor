package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cordanaLLM/praetor/internal/baseline"
	"github.com/cordanaLLM/praetor/internal/gating"
	"github.com/cordanaLLM/praetor/internal/readmepresentation"
)

// runBaselineCmd dispatches the baseline command for the fixture with extra flags.
func runBaselineCmd(t *testing.T, f *auditFixture, extra ...string) (string, error) {
	t.Helper()
	args := append([]string{"--file=" + f.baselinePath}, extra...)
	return captureStdout(t, func() error { return dispatchCommand("baseline", args) })
}

func TestBaselineRecord_Positive(t *testing.T) {
	f := newAuditFixture(t)
	f.addViolation(t)

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
	// The entry is recorded in the anchored form: keyed by the function holding the finding,
	// with the line kept beside it for a reader (#29).
	if b.TotalInfractions != 1 || b.Infractions[0].Fingerprint != "legacy.go:HISS-07:fn:legacy#1" ||
		b.Infractions[0].Anchor != "fn:legacy" || b.Infractions[0].LineNumber != 4 || b.IncreaseRationale != "legacy debt inventory" {
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

// TestBaselineRecord_RecordsRepositoryIdentity_3D pins BUG-801 for `baseline --record`: the
// recorded baseline names the origin remote's repository and the HEAD commit it was taken at.
func TestBaselineRecord_RecordsRepositoryIdentity_3D(t *testing.T) {
	// Positive: the fixture is a committed repository; give it an origin remote.
	f := newAuditFixture(t)
	if out, err := runFixtureGit(t, f.dir, f.gitEnv, "remote", "add", "origin", "https://github.com/acme/widgets.git"); err != nil {
		t.Fatalf("add origin: %v (%s)", err, out)
	}
	head, err := runFixtureGit(t, f.dir, f.gitEnv, "rev-parse", "HEAD")
	if err != nil {
		t.Fatalf("rev-parse: %v (%s)", err, head)
	}
	if out, err := runBaselineCmd(t, f, "--record"); err != nil {
		t.Fatalf("record: %v\n%s", err, out)
	}
	b, err := baseline.LoadBaseline(f.baselinePath)
	if err != nil {
		t.Fatal(err)
	}
	if b.Repository != "acme/widgets" || b.CommitSHA != strings.TrimSpace(head) {
		t.Fatalf("recorded identity = %q @ %q; want acme/widgets @ %q", b.Repository, b.CommitSHA, strings.TrimSpace(head))
	}

	// Boundary: outside a Git worktree there is no repository or commit to record.
	plain := filepath.Join(t.TempDir(), ".standards-baseline.json")
	if out, err := captureStdout(t, func() error {
		return dispatchCommand("baseline", []string{"--file=" + plain, "--record"})
	}); err != nil {
		t.Fatalf("record outside Git: %v\n%s", err, out)
	}
	if b, err = baseline.LoadBaseline(plain); err != nil || b.Repository != "" || b.CommitSHA != "" {
		t.Fatalf("identity outside Git = %+v, %v; want both empty", b, err)
	}

	// Negative: Git metadata that does not answer refuses the record and writes nothing.
	brokenDir := t.TempDir()
	writeFixtureFile(t, brokenDir, ".git", "gitdir: nonexistent\n")
	broken := filepath.Join(brokenDir, ".standards-baseline.json")
	if _, err := captureStdout(t, func() error {
		return dispatchCommand("baseline", []string{"--file=" + broken, "--record"})
	}); err == nil {
		t.Fatal("record with broken Git metadata succeeded; want a refusal")
	}
	if _, err := os.Stat(broken); !os.IsNotExist(err) {
		t.Fatalf("refused record still wrote a baseline: %v", err)
	}
}

// fileSnapshot returns the bytes and modification time of path, so a test can prove a
// read-only command left the file alone.
func fileSnapshot(t *testing.T, path string) (string, time.Time) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data), info.ModTime()
}

func assertUnchanged(t *testing.T, path, wantData string, wantMod time.Time) {
	t.Helper()
	data, mod := fileSnapshot(t, path)
	if data != wantData || !mod.Equal(wantMod) {
		t.Fatalf("--verify rewrote %s:\n%s", path, data)
	}
}

// Positive (BUG-662): a tree whose debt is exactly what the baseline records passes --verify,
// and the file is neither rewritten nor touched. standards-sync step 4 runs this command.
func TestBaselineVerify_Positive_UnchangedTreePassesReadOnly(t *testing.T) {
	f := newAuditFixture(t)
	f.writeBaseline(t, []baseline.Infraction{f.addViolation(t)}, "legacy debt inventory")
	data, mod := fileSnapshot(t, f.baselinePath)

	out, err := runBaselineCmd(t, f, "--verify")
	if err != nil {
		t.Fatalf("verify of an unchanged tree: %v\n%s", err, out)
	}
	mustContain(t, out, "[PASS] HISS-13 debt ratchet: 1 active infractions within the 1 recorded", "not rewritten")
	assertUnchanged(t, f.baselinePath, data, mod)
}

// Negative: an infraction the baseline does not record fails --verify and names it, without
// recording it; a missing baseline fails with its own error; --verify refuses the write flags.
func TestBaselineVerify_Negative_NewDebtAndMissingBaselineFail(t *testing.T) {
	f := newAuditFixture(t)
	f.addViolation(t)
	data, mod := fileSnapshot(t, f.baselinePath)
	_, err := runBaselineCmd(t, f, "--verify")
	mustErrContain(t, err, "1 unbaselined")
	mustErrContain(t, err, "legacy.go:4")
	assertUnchanged(t, f.baselinePath, data, mod)

	missing := filepath.Join(t.TempDir(), "absent.json")
	_, err = captureStdout(t, func() error { return dispatchCommand("baseline", []string{"--file=" + missing, "--verify"}) })
	if !errors.Is(err, errBaselineMissing) {
		t.Fatalf("missing baseline must fail distinctly, got %v", err)
	}
	if _, statErr := os.Stat(missing); !os.IsNotExist(statErr) {
		t.Fatalf("--verify created the baseline file (stat err %v)", statErr)
	}

	for _, extra := range [][]string{{"--record"}, {"--allow-increase"}, {"--reason=x"}} {
		_, err := runBaselineCmd(t, f, append([]string{"--verify"}, extra...)...)
		mustErrContain(t, err, "read-only")
	}
	assertUnchanged(t, f.baselinePath, data, mod)
}

// Boundary: a count equal to the baseline passes only when it is the recorded debt. The same
// count at an unrecorded fingerprint is new debt, and the scan uses the repository's own
// function-length limit, the one the audit enforces, not the scanner default.
func TestBaselineVerify_Boundary_EqualCountAndPolicyLimit(t *testing.T) {
	f := newAuditFixture(t)
	f.addViolation(t)
	f.writeBaseline(t, []baseline.Infraction{{RuleID: "HISS-07", FilePath: "other.go", LineNumber: 9, Fingerprint: "other.go:9:HISS-07"}}, "")
	_, err := runBaselineCmd(t, f, "--verify")
	mustErrContain(t, err, "1 unbaselined")

	tight := newAuditFixture(t)
	writeFixtureFile(t, tight.dir, ".standards.yaml",
		fixtureManifest("acme", "widgets", false)+"overrides:\n  complexity:\n    max_func_loc: 40\n")
	writeFixtureFile(t, tight.dir, "long.go", "package long\n\nfunc long() int {\n\tx := 0\n"+
		strings.Repeat("\tx++\n", 45)+"\treturn x\n}\n")
	_, err = runBaselineCmd(t, tight, "--verify")
	mustErrContain(t, err, "long.go")
	// --record counts the same debt, so it refuses to raise the zero baseline silently.
	_, err = runBaselineCmd(t, tight, "--record")
	mustErrContain(t, err, "--allow-increase")
}

// Positive (BUG-895): baseline routes argv through parseInterspersed like every command, so
// --verify binds wherever it is written and a trailing "--" terminator adds no positional.
func TestParseBaselineMode_Positive_InterspersedFlagsAndTrailingTerminator(t *testing.T) {
	mode, err := parseBaselineMode([]string{"--verify", "--file", "custom.json", "--"})
	if err != nil || !mode.verify || mode.path != "custom.json" {
		t.Fatalf("flags before a trailing terminator: mode %+v, err %v", mode, err)
	}
}

// Negative: a stray positional is refused by name alone while a flag written after it still
// binds, and an unknown flag after it is reported as a flag instead of as a positional.
func TestParseBaselineMode_Negative_StrayPositionalRefusedByName(t *testing.T) {
	mode, err := parseBaselineMode([]string{"stray", "--verify"})
	if err == nil || err.Error() != `baseline accepts no positional arguments, got ["stray"]` {
		t.Fatalf("a stray positional must be refused by name alone, got %v", err)
	}
	if !mode.verify {
		t.Error("--verify written after the positional must still bind")
	}
	_, err = parseBaselineMode([]string{"stray", "--bogus"})
	mustErrContain(t, err, "flag provided but not defined: -bogus")
}

// Boundary: a token after "--" stays positional, so "--verify -- --record" is refused as a
// positional instead of switching on --record or tripping the read-only combination check.
func TestParseBaselineMode_Boundary_TerminatorKeepsFlagShapedTokenPositional(t *testing.T) {
	mode, err := parseBaselineMode([]string{"--verify", "--", "--record"})
	if err == nil || err.Error() != `baseline accepts no positional arguments, got ["--record"]` {
		t.Fatalf("a token after -- must stay positional, got %v", err)
	}
	if mode.record {
		t.Error("a --record token after the terminator must not bind")
	}
}

const fixtureGosecVuln = `package widgets

import (
	"context"
	"os/exec"
)

func runVuln(ctx context.Context, arg string) error {
	return exec.CommandContext(ctx, "sh", "-c", arg).Run()
}
`

func TestBaselineRecord_Gosec_Positive(t *testing.T) {
	f := gosecRecordFixture(t)

	seedHistoricalGosec(t, f)
	out, err := runBaselineCmd(t, f, "--record")
	if err != nil {
		t.Fatalf("record with gosec: %v\n%s", err, out)
	}
	mustContain(t, out, "Gosec baseline successfully updated", "Total: 1 infractions, previously 1")

	gosecPath := filepath.Join(f.dir, gating.GosecBaselineFile)
	gosecBase, err := baseline.LoadBaseline(gosecPath)
	if err != nil {
		t.Fatalf("load gosec baseline: %v", err)
	}
	if gosecBase.TotalInfractions != 1 || gosecBase.Infractions[0].RuleID != "G204" {
		t.Fatalf("unexpected gosec baseline: %+v", gosecBase)
	}

	// Remove finding from code, commit, and re-record while HISS debt is unchanged (shrinks gosec to 0).
	if err := os.Remove(filepath.Join(f.dir, "vuln.go")); err != nil {
		t.Fatal(err)
	}
	gitCommitAll(t, f.dir, f.gitEnv, "remove vuln")

	out, err = runBaselineCmd(t, f, "--record")
	if err != nil {
		t.Fatalf("record gosec decrease: %v\n%s", err, out)
	}
	mustContain(t, out, "Baseline unchanged", "kept the recorded file",
		"Gosec baseline successfully updated", "Total: 0 infractions, previously 1")

	gosecBase, err = baseline.LoadBaseline(gosecPath)
	if err != nil {
		t.Fatalf("load shrunk gosec baseline: %v", err)
	}
	if gosecBase.TotalInfractions != 0 {
		t.Fatalf("expected gosec baseline to shrink to 0, got %d", gosecBase.TotalInfractions)
	}
}

func TestBaselineRecord_Gosec_Negative(t *testing.T) {
	f := gosecRecordFixture(t)

	gosecPath := filepath.Join(f.dir, gating.GosecBaselineFile)

	// HISS increase flags cannot authorize a first gosec baseline.
	_, err := runBaselineCmd(t, f, "--record", "--allow-increase", "--reason=hiss debt only")
	if err == nil {
		t.Fatal("expected error when gosec increases without --allow-gosec-increase")
	}
	mustErrContain(t, err, "no first gosec baseline")
	if _, statErr := os.Stat(gosecPath); !os.IsNotExist(statErr) {
		t.Fatalf("refused record must not write gosec baseline: %v", statErr)
	}

	// A gosec rationale cannot authorize a first gosec baseline.
	_, err = runBaselineCmd(t, f, "--record", "--gosec-reason=rationale without flag")
	if err == nil {
		t.Fatal("expected error with --gosec-reason but no --allow-gosec-increase")
	}
	mustErrContain(t, err, "no first gosec baseline")
	if _, statErr := os.Stat(gosecPath); !os.IsNotExist(statErr) {
		t.Fatalf("refused record must not write gosec baseline: %v", statErr)
	}

	// --verify is read-only and refuses --gosec-reason.
	_, err = runBaselineCmd(t, f, "--verify", "--gosec-reason=x")
	mustErrContain(t, err, "read-only")

	// --verify is read-only and refuses --allow-gosec-increase.
	_, err = runBaselineCmd(t, f, "--verify", "--allow-gosec-increase")
	mustErrContain(t, err, "read-only")
}

func TestBaselineRecord_Gosec_Boundary_UnchangedDebtKeepsBothFiles(t *testing.T) {
	f := gosecRecordFixture(t)

	seedHistoricalGosec(t, f)
	out, err := runBaselineCmd(t, f, "--record")
	if err != nil {
		t.Fatalf("record initial: %v\n%s", err, out)
	}
	mustContain(t, out, "Gosec baseline successfully updated")

	gosecPath := filepath.Join(f.dir, gating.GosecBaselineFile)
	hissData, err := os.ReadFile(f.baselinePath)
	if err != nil {
		t.Fatal(err)
	}
	gosecData, err := os.ReadFile(gosecPath)
	if err != nil {
		t.Fatal(err)
	}

	// Re-record with unchanged HISS and unchanged gosec debt.
	out, err = runBaselineCmd(t, f, "--record")
	if err != nil {
		t.Fatalf("record unchanged: %v\n%s", err, out)
	}
	mustContain(t, out, "Baseline unchanged", "kept the recorded file", "Gosec baseline unchanged")

	hissAfter, err := os.ReadFile(f.baselinePath)
	if err != nil {
		t.Fatal(err)
	}
	if string(hissAfter) != string(hissData) {
		t.Error("HISS baseline was rewritten despite unchanged debt")
	}
	gosecAfter, err := os.ReadFile(gosecPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(gosecAfter) != string(gosecData) {
		t.Error("gosec baseline was rewritten despite unchanged debt")
	}
}

func TestBaselineRecord_ReadmePresentationSurvivesWithZeroHISS(t *testing.T) {
	f := newAuditFixture(t)
	writeFixtureFile(t, f.dir, "README.md", "# Widgets\n")

	// Zero-debt baseline + report-only README finding must record fine without --allow-increase.
	out, err := runBaselineCmd(t, f, "--record")
	if err != nil {
		t.Fatalf("record report-only readme finding with 0 HISS: %v\n%s", err, out)
	}
	mustContain(t, out, "Total: 0 infractions")

	// With gate.readme_presentation: true, recording without --allow-increase refuses and names README findings apart from HISS.
	writeFixtureFile(t, f.dir, ".standards.yaml", "version: 1\nrepository:\n  owner: acme\n  name: widgets\ngate:\n  readme_presentation: true\n")
	if _, err := runBaselineCmd(t, f, "--record"); err == nil || !strings.Contains(err.Error(), "README presentation finding(s)") {
		t.Fatalf("expected refusal naming README presentation findings apart from HISS, got: %v", err)
	}

	out, err = runBaselineCmd(t, f, "--record", "--allow-increase", "--reason=readme presentation debt")
	if err != nil {
		t.Fatalf("record enforced readme finding with 0 HISS: %v\n%s", err, out)
	}
	mustContain(t, out, "Total: 1 infractions, previously 0")

	// Subsequent baseline --record with 0 HISS findings must preserve the README entry even without the enforce key.
	writeFixtureFile(t, f.dir, ".standards.yaml", "version: 1\nrepository:\n  owner: acme\n  name: widgets\n")
	out, err = runBaselineCmd(t, f, "--record")
	if err != nil {
		t.Fatalf("second record: %v\n%s", err, out)
	}
	mustContain(t, out, "Baseline unchanged", "Total: 1 infractions, previously 1")

	b, err := baseline.LoadBaseline(f.baselinePath)
	if err != nil {
		t.Fatal(err)
	}
	if _, hasReadme := readmepresentation.ReadmeBaseline(b); !hasReadme || b.TotalInfractions != 1 {
		t.Fatalf("expected README entry preserved in baseline, got: %+v", b)
	}

	out, err = runBaselineCmd(t, f, "--verify")
	if err != nil {
		t.Fatalf("verify baseline with README entry: %v\n%s", err, out)
	}
	mustContain(t, out, "1 active infractions within the 1 recorded")
}
