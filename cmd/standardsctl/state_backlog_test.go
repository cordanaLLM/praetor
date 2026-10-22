// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/config"
	"github.com/cordanaLLM/praetor/internal/state"
)

// capsRepository writes an unadopted repository whose manifest carries caps and whose bug
// ledger holds defects open defect rows.
func capsRepository(t *testing.T, caps string, defects int) string {
	t.Helper()
	dir := t.TempDir()
	manifest := "version: 1\nrepository:\n  owner: acme\n  name: widgets\n"
	if caps != "" {
		manifest += "backlog:\n  caps:\n" + caps
	}
	writeFixtureFile(t, dir, ".standards.yaml", manifest)
	if err := state.InitWorkingDir(dir); err != nil {
		t.Fatal(err)
	}
	addDefects(t, dir, defects)
	return dir
}

func addDefects(t *testing.T, dir string, count int) {
	t.Helper()
	for i := 0; i < count; i++ {
		if _, err := state.AddBug(dir, state.BugEntry{Title: "defect", Location: "core", Kind: state.BugKindDefect}); err != nil {
			t.Fatal(err)
		}
	}
}

// Positive: state status prints each capped category with its count and cap, marks one over
// the cap and says how to write its batch; boundary: a category at its cap is marked at it.
func TestStateStatus_Positive_PrintsBacklogCaps(t *testing.T) {
	dir := capsRepository(t, "    defects: {max: 1, action: batch}\n", 2)
	out, err := captureStdout(t, func() error { return dispatchCommand("state", []string{"status", dir}) })
	if err != nil {
		t.Fatalf("state status: %v\n%s", err, out)
	}
	mustContain(t, out,
		"  Backlog Cap:       defects: 2 of 1, OVER the cap (max 1 set by repository, action batch set by repository)",
		"defects: run 'praetorctl state batch' to write its batch")

	atCap := capsRepository(t, "    defects: {max: 2, action: batch}\n", 2)
	out, err = captureStdout(t, func() error { return dispatchCommand("state", []string{"status", atCap}) })
	if err != nil || !strings.Contains(out, "defects: 2 of 2, at the cap") || strings.Contains(out, "state batch") {
		t.Fatalf("at the cap: %v\n%s", err, out)
	}
}

// Negative: without a declared cap status prints no cap line; a manifest whose caps do not
// resolve prints them as unresolved instead of omitting them, and status still reports.
func TestStateStatus_Negative_NoCapOrUnresolvedCaps(t *testing.T) {
	out, err := captureStdout(t, func() error { return dispatchCommand("state", []string{"status", capsRepository(t, "", 3)}) })
	if err != nil || strings.Contains(out, "Backlog Cap") {
		t.Fatalf("uncapped status: %v\n%s", err, out)
	}
	dir := capsRepository(t, "", 0)
	writeFixtureFile(t, dir, ".standards.yaml", "version: 1\nbacklog:\n  caps:\n    bugs: {max: 1}\n")
	out, err = captureStdout(t, func() error { return dispatchCommand("state", []string{"status", dir}) })
	if err != nil || !strings.Contains(out, `Backlog Cap:       unresolved: resolve backlog caps:`) || !strings.Contains(out, `unknown category "bugs"`) {
		t.Fatalf("unresolved caps: %v\n%s", err, out)
	}
}

// Positive: state batch writes the batch file the date names; negative: a repository without
// caps writes nothing, and a malformed date fails.
func TestStateBatch_WritesTheBatchOfAnOverCapCategory(t *testing.T) {
	dir := capsRepository(t, "    defects: {max: 1, action: gate}\n", 2)
	out, err := captureStdout(t, func() error {
		return dispatchCommand("state", []string{"batch", dir, "--date=2026-10-07"})
	})
	if err != nil {
		t.Fatalf("state batch: %v\n%s", err, out)
	}
	mustContain(t, out, "[INFO] "+config.NoLockNotice, "Wrote .workingdir/batches/defects-2026-10-07.md")
	data, err := os.ReadFile(filepath.Join(dir, ".workingdir", "batches", "defects-2026-10-07.md"))
	if err != nil || !strings.Contains(string(data), "`BUG-001`") || !strings.Contains(string(data), "`BUG-002`") {
		t.Fatalf("batch file: %v\n%s", err, data)
	}

	out, err = captureStdout(t, func() error { return dispatchCommand("state", []string{"batch", capsRepository(t, "", 4)}) })
	if err != nil || !strings.Contains(out, "no backlog cap is declared; nothing written") {
		t.Fatalf("uncapped batch: %v\n%s", err, out)
	}
	if _, err := captureStdout(t, func() error {
		return dispatchCommand("state", []string{"batch", dir, "--date=tomorrow"})
	}); err == nil || !strings.Contains(err.Error(), `batch date "tomorrow"`) {
		t.Fatalf("malformed date: %v", err)
	}
}

// Positive: state bug kind labels a row and state bug add records the kind; negative: an
// unknown kind and a missing argument fail.
func TestStateBugKind_LabelsARow(t *testing.T) {
	dir := capsRepository(t, "", 0)
	out, err := captureStdout(t, func() error {
		return dispatchCommand("state", []string{"bug", "add", "--dir", dir, "--title", "scoped", "--kind", "scope"})
	})
	if err != nil || !strings.Contains(out, "(p2, core, scope)") {
		t.Fatalf("bug add --kind: %v\n%s", err, out)
	}
	if _, err := captureStdout(t, func() error {
		return dispatchCommand("state", []string{"bug", "kind", "BUG-001", "defect", "--dir", dir})
	}); err != nil {
		t.Fatal(err)
	}
	bugs, err := state.ListBugs(dir, "all")
	if err != nil || len(bugs) != 1 || bugs[0].Kind != state.BugKindDefect {
		t.Fatalf("labelled row: %+v %v", bugs, err)
	}
	for _, args := range [][]string{{"bug", "kind", "BUG-001", "feature", "--dir", dir}, {"bug", "kind", "BUG-001"}} {
		if _, err := captureStdout(t, func() error { return dispatchCommand("state", args) }); err == nil {
			t.Errorf("state %v succeeded", args)
		}
	}
}

// The audit's backlog gate: at the cap it passes and prints the category; one over it fails
// naming the category, the count and the cap; without a cap it prints nothing.
func TestAuditBacklogCaps_GateAtAndOverTheCap(t *testing.T) {
	dir := capsRepository(t, "    defects: {max: 2, action: gate}\n", 2)
	out, err := captureStdout(t, func() error { return auditBacklogCaps(t.Context(), dir, resolveCaps(t, dir)) })
	if err != nil {
		t.Fatalf("at the cap: %v\n%s", err, out)
	}
	mustContain(t, out, "[INFO] Backlog cap defects: 2 of 2, at the cap",
		"[PASS] Backlog caps: every category with action gate was counted and is within its cap.")

	addDefects(t, dir, 1)
	_, err = captureStdout(t, func() error { return auditBacklogCaps(t.Context(), dir, resolveCaps(t, dir)) })
	mustErrContain(t, err, "[FAIL] backlog cap defects: 3 items, over the cap of 2 (action gate, max set by repository)")

	uncapped := capsRepository(t, "", 5)
	out, err = captureStdout(t, func() error { return auditBacklogCaps(t.Context(), uncapped, resolveCaps(t, uncapped)) })
	if err != nil || out != "" {
		t.Fatalf("uncapped audit: %v %q", err, out)
	}
}

// The gate runs inside praetorctl audit: a passing fixture fails once a gated category in its
// ledger is over the cap.
func TestAudit_Negative_BacklogCapGateFailsTheAudit(t *testing.T) {
	f := newAuditFixture(t)
	manifest, err := os.ReadFile(f.manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	writeFixtureFile(t, f.dir, ".standards.yaml", string(manifest)+"backlog:\n  caps:\n    defects: {max: 1, action: gate}\n")
	addDefects(t, f.dir, 2)
	out, err := f.audit(t)
	mustErrContain(t, err, "backlog cap defects: 2 items, over the cap of 1")
	mustContain(t, out, "backlog.caps.defects: max=1 (repository) action=gate (repository)")
}

// resolveCaps resolves the unadopted policy of dir as the audit hands it to the gate.
func resolveCaps(t *testing.T, dir string) *config.EffectivePolicy {
	t.Helper()
	policy, _, err := config.LoadUnadoptedEffectivePolicyContext(t.Context(), config.EffectiveOptions{Root: dir})
	if err != nil {
		t.Fatal(err)
	}
	return policy
}

// manifestOnly writes a repository with a manifest and no .workingdir, as a CI clone is.
func manifestOnly(t *testing.T, caps string) string {
	t.Helper()
	dir := t.TempDir()
	writeFixtureFile(t, dir, ".standards.yaml", "version: 1\nrepository:\n  owner: acme\n  name: widgets\nbacklog:\n  caps:\n"+caps)
	return dir
}

// HISS-21: in a checkout without the ledgers a gated category is not counted, prints SKIP with
// the reason and the summary is SKIP, never PASS; the audit does not fail on it. Once the
// ledger exists the same cap is counted and passes.
func TestAuditBacklogCaps_Boundary_AbsentLedgerSkipsTheGate(t *testing.T) {
	dir := manifestOnly(t, "    defects: {max: 2, action: gate}\n    questions: {max: 2}\n")
	out, err := captureStdout(t, func() error { return auditBacklogCaps(t.Context(), dir, resolveCaps(t, dir)) })
	if err != nil {
		t.Fatalf("absent ledger failed the audit: %v\n%s", err, out)
	}
	mustContain(t, out,
		"[SKIP] Backlog cap defects: not counted: .workingdir/BUGS.md is not present in this checkout",
		"[INFO] Backlog cap questions: not counted: .workingdir/QUESTIONS.md is not present in this checkout",
		"[SKIP] Backlog caps: the gate on defects did not run: the ledger is not present in this checkout.")
	if strings.Contains(out, "[PASS]") || strings.Contains(out, "0 of 2") {
		t.Fatalf("an absent ledger passed or counted zero:\n%s", out)
	}
	if err := state.InitWorkingDir(dir); err != nil {
		t.Fatal(err)
	}
	out, err = captureStdout(t, func() error { return auditBacklogCaps(t.Context(), dir, resolveCaps(t, dir)) })
	if err != nil || !strings.Contains(out, "[PASS] Backlog caps") || strings.Contains(out, "[SKIP]") {
		t.Fatalf("present ledger: %v\n%s", err, out)
	}
}

// A ledger that cannot be read fails the audit only for a gated category; with action batch
// it is reported as a warning for that category, and without any gate the summary says so.
func TestAuditBacklogCaps_Negative_ReadErrorFailsOnlyAGate(t *testing.T) {
	dir := capsRepository(t, "    tasks: {max: 5, action: batch}\n", 0)
	writeFixtureFile(t, dir, ".workingdir/BACKLOG.md", "```\n- [ ] swallowed\n")
	out, err := captureStdout(t, func() error { return auditBacklogCaps(t.Context(), dir, resolveCaps(t, dir)) })
	if err != nil {
		t.Fatalf("an unreadable batch-only category failed the audit: %v\n%s", err, out)
	}
	mustContain(t, out, "[WARN] Backlog cap tasks: not counted: BACKLOG.md has an unterminated code fence",
		"[INFO] Backlog caps: no category declares action gate; nothing is gated.")
	writeFixtureFile(t, dir, ".standards.yaml", "version: 1\nbacklog:\n  caps:\n    tasks: {max: 5, action: gate}\n")
	_, err = captureStdout(t, func() error { return auditBacklogCaps(t.Context(), dir, resolveCaps(t, dir)) })
	mustErrContain(t, err, "[FAIL] backlog cap tasks has action gate but cannot be counted: BACKLOG.md has an unterminated code fence")
}

// state status prints a category whose ledger is absent as not counted, and prints no
// unresolved line for a policy that fails to resolve while no layer declares a backlog
// section; with one declared, the unresolved line stays.
func TestStateStatus_Boundary_AbsentLedgerAndUnresolvedWithoutCaps(t *testing.T) {
	dir := capsRepository(t, "    defects: {max: 1}\n", 0)
	if err := os.Remove(filepath.Join(dir, ".workingdir", "BUGS.md")); err != nil {
		t.Fatal(err)
	}
	out, err := captureStdout(t, func() error { return dispatchCommand("state", []string{"status", dir}) })
	if err != nil || !strings.Contains(out, "Backlog Cap:       defects: not counted: .workingdir/BUGS.md is not present in this checkout") {
		t.Fatalf("absent ledger status: %v\n%s", err, out)
	}
	plain := capsRepository(t, "", 0)
	writeFixtureFile(t, plain, ".standards.lock", "version: 1\n")
	out, err = captureStdout(t, func() error { return dispatchCommand("state", []string{"status", plain}) })
	if err != nil || strings.Contains(out, "Backlog Cap") {
		t.Fatalf("an invalid lock without caps printed a cap line: %v\n%s", err, out)
	}
	capped := capsRepository(t, "    defects: {max: 1}\n", 0)
	writeFixtureFile(t, capped, ".standards.lock", "version: 1\n")
	out, err = captureStdout(t, func() error { return dispatchCommand("state", []string{"status", capped}) })
	if err != nil || !strings.Contains(out, "Backlog Cap:       unresolved: resolve backlog caps:") {
		t.Fatalf("an invalid lock with caps hid them: %v\n%s", err, out)
	}
}

// The no-lock notice of state batch names the external layer that set the cap instead of
// claiming built-in defaults and repository overrides only.
func TestStateBatch_NoLockNoticeNamesTheFleet(t *testing.T) {
	dir := capsRepository(t, "", 2)
	fleet := writeFixtureFile(t, t.TempDir(), "fleet.yaml", "backlog:\n  caps:\n    defects: {max: 1, action: batch}\n")
	out, err := captureStdout(t, func() error {
		return dispatchCommand("state", []string{"batch", dir, "--date=2026-10-07", "--fleet-config=" + fleet})
	})
	if err != nil {
		t.Fatalf("state batch: %v\n%s", err, out)
	}
	mustContain(t, out, "[INFO] no .standards.lock: built-in defaults, repository overrides and the fleet policy only",
		"max 1 set by fleet", "Wrote .workingdir/batches/defects-2026-10-07.md")
	if strings.Contains(out, config.NoLockNotice) {
		t.Fatalf("the notice contradicts the fleet layer:\n%s", out)
	}
}
