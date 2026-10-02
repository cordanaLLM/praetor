package adopt

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/baseline"
)

// debtFile is Go source carrying one HISS infraction: an error result that is discarded.
const debtFile = "package main\nfunc run() {\n\t_ = doSomething()\n}\n"

// adoptedWithDebt adopts a repository holding one infraction and returns it with the bytes of
// the baseline that first adoption recorded.
func adoptedWithDebt(t *testing.T, name string) (repoPath, recorded string) {
	t.Helper()
	repoPath = newTestRepo(t, name)
	mustWrite(t, filepath.Join(repoPath, "main.go"), debtFile)
	rep, err := Adopt(context.Background(), readoptOptions(t, repoPath))
	if err != nil {
		t.Fatalf("first adoption: %v", err)
	}
	assertNoIssues(t, rep)
	if rep.BaselineStatus != "scanned" || rep.LegacyDebtCount != 1 || !contains(rep.CreatedFiles, baselineFile) {
		t.Fatalf("first adoption must record the one infraction: status %q, count %d, created %v",
			rep.BaselineStatus, rep.LegacyDebtCount, rep.CreatedFiles)
	}
	return repoPath, mustRead(t, filepath.Join(repoPath, baselineFile))
}

// readoptOptions are the options `praetorctl adopt` passes by default: --record-baseline is on.
func readoptOptions(t *testing.T, repoPath string) AdoptOptions {
	t.Helper()
	return AdoptOptions{LockSourceRoot: newAdoptLockSource(t), Path: repoPath, RecordBaseline: true}
}

// warned reports whether one of the report's warnings contains every fragment.
func warned(rep *AdoptReport, fragments ...string) bool {
	for _, warning := range rep.Warnings {
		all := true
		for _, fragment := range fragments {
			all = all && strings.Contains(warning, fragment)
		}
		if all {
			return true
		}
	}
	return false
}

// TestAdopt_Positive_ReAdoptionKeepsBaseline: a first adoption records the baseline, and a
// re-adoption of the unchanged repository keeps it byte for byte and reports the ratchet as
// passing, with the recorded count.
func TestAdopt_Positive_ReAdoptionKeepsBaseline(t *testing.T) {
	repoPath, recorded := adoptedWithDebt(t, "keeps-baseline")

	rep, err := Adopt(context.Background(), readoptOptions(t, repoPath))
	if err != nil {
		t.Fatalf("re-adoption: %v", err)
	}
	assertNoIssues(t, rep)
	if got := mustRead(t, filepath.Join(repoPath, baselineFile)); got != recorded {
		t.Fatalf("re-adoption rewrote the baseline:\n%s", got)
	}
	if rep.BaselineStatus != "existing" || rep.LegacyDebtCount != 1 {
		t.Fatalf("status %q, count %d; want the existing baseline's one infraction", rep.BaselineStatus, rep.LegacyDebtCount)
	}
	verdict := rep.BaselineRatchet
	if verdict == nil || !verdict.Passed || verdict.Recorded != 1 || verdict.Active != 1 || verdict.Unbaselined != 0 {
		t.Fatalf("ratchet verdict = %+v; want a pass at 1 active within 1 recorded", verdict)
	}
	if warned(rep, baselineFile) {
		t.Fatalf("a passing ratchet warned about the baseline: %v", rep.Warnings)
	}
}

// TestAdopt_Negative_ReAdoptionDoesNotAbsorbNewDebt pins #358: a default re-adoption, forced
// included, of a repository that gained an infraction keeps the baseline byte for byte, so the
// finding stays unbaselined, and reports it where it used to record it as accepted debt.
func TestAdopt_Negative_ReAdoptionDoesNotAbsorbNewDebt(t *testing.T) {
	for name, force := range map[string]bool{"plain": false, "forced": true} {
		t.Run(name, func(t *testing.T) {
			repoPath, recorded := adoptedWithDebt(t, "ratchet-"+name)
			mustWrite(t, filepath.Join(repoPath, "added.go"), strings.Replace(debtFile, "run", "added", 1))

			opts := readoptOptions(t, repoPath)
			opts.Force = force
			rep, err := Adopt(context.Background(), opts)
			if err != nil {
				t.Fatalf("re-adoption: %v", err)
			}
			assertNoIssues(t, rep)
			full := filepath.Join(repoPath, baselineFile)
			if got := mustRead(t, full); got != recorded {
				t.Fatalf("re-adoption absorbed the new infraction into the baseline:\n%s", got)
			}
			kept, err := baseline.LoadBaseline(full)
			if err != nil || kept.TotalInfractions != 1 || strings.Contains(recorded, "added.go") {
				t.Fatalf("kept baseline = %+v, %v; want the one recorded infraction and no added.go", kept, err)
			}
			verdict := rep.BaselineRatchet
			if verdict == nil || verdict.Passed || verdict.Recorded != 1 || verdict.Active != 2 || verdict.Unbaselined != 1 {
				t.Fatalf("ratchet verdict = %+v; want a rejection at 2 active, 1 unbaselined, 1 recorded", verdict)
			}
			if rep.BaselineStatus != "existing" || rep.LegacyDebtCount != 1 {
				t.Fatalf("status %q, count %d; the report must state the recorded debt, not the rescan", rep.BaselineStatus, rep.LegacyDebtCount)
			}
			if !warned(rep, baselineFile, "added.go", "--rerecord-baseline") {
				t.Fatalf("no warning names the unbaselined finding and the deliberate re-record: %v", rep.Warnings)
			}
		})
	}
}

// rerecordOptions are readoptOptions with the explicit re-record.
func rerecordOptions(t *testing.T, repoPath string) AdoptOptions {
	t.Helper()
	opts := readoptOptions(t, repoPath)
	opts.RerecordBaseline = true
	return opts
}

// TestAdopt_RerecordBaseline_3D: an explicit re-record obeys baseline.Record, the rule of
// `praetorctl baseline --record`. Negative: a higher count is refused, with or without a
// rationale-less --allow-increase, the file is kept and the refusal names the finding.
// Positive: the allowed increase is recorded with its rationale. Boundary: a lower count needs
// no allowance, and unchanged debt keeps the file byte for byte.
func TestAdopt_RerecordBaseline_3D(t *testing.T) {
	repoPath, recorded := adoptedWithDebt(t, "rerecord")
	full := filepath.Join(repoPath, baselineFile)
	added := filepath.Join(repoPath, "added.go")

	same, err := Adopt(context.Background(), rerecordOptions(t, repoPath))
	if err != nil || mustRead(t, full) != recorded || !strings.Contains(actionDetail(same, baselineFile, actionReconcile), "baseline unchanged") {
		t.Fatalf("unchanged debt = %v; want the file kept and reported as unchanged: %+v", err, same.ActionDetails)
	}

	mustWrite(t, added, strings.Replace(debtFile, "run", "added", 1))
	rep, err := Adopt(context.Background(), rerecordOptions(t, repoPath))
	if !errors.Is(err, baseline.ErrDebtIncrease) || !strings.Contains(err.Error(), "added.go") || !strings.Contains(err.Error(), "--allow-increase") {
		t.Fatalf("re-record of a higher count = %v; want ErrDebtIncrease naming added.go and --allow-increase", err)
	}
	if rep == nil || rep.BaselineStatus != "failed" || mustRead(t, full) != recorded {
		t.Fatalf("refused re-record must fail the step and keep the baseline: %+v", rep)
	}

	silent := rerecordOptions(t, repoPath)
	silent.AllowBaselineIncrease = true
	if _, err := Adopt(context.Background(), silent); !errors.Is(err, baseline.ErrIncreaseRationaleRequired) || mustRead(t, full) != recorded {
		t.Fatalf("increase without a reason = %v; want ErrIncreaseRationaleRequired and the baseline kept", err)
	}

	allowed := rerecordOptions(t, repoPath)
	allowed.AllowBaselineIncrease, allowed.BaselineIncreaseReason = true, "vendored generator output"
	rep, err = Adopt(context.Background(), allowed)
	if err != nil {
		t.Fatalf("allowed increase: %v", err)
	}
	raised, err := baseline.LoadBaseline(full)
	if err != nil || raised.TotalInfractions != 2 || raised.IncreaseRationale != "vendored generator output" || raised.Repository != "acme/rerecord" {
		t.Fatalf("raised baseline = %+v, %v; want 2 infractions with the rationale and repository", raised, err)
	}
	if rep.BaselineStatus != "scanned" || rep.LegacyDebtCount != 2 || rep.BaselineRatchet != nil || !warned(rep, "increased deliberately", "vendored generator output") {
		t.Fatalf("allowed increase report: status %q, count %d, verdict %+v, warnings %v",
			rep.BaselineStatus, rep.LegacyDebtCount, rep.BaselineRatchet, rep.Warnings)
	}

	if err := os.Remove(added); err != nil {
		t.Fatal(err)
	}
	if rep, err = Adopt(context.Background(), rerecordOptions(t, repoPath)); err != nil || rep.LegacyDebtCount != 1 {
		t.Fatalf("re-record of a lower count = %v, %+v; want 1 infraction without an allowance", err, rep)
	}
	if lowered, err := baseline.LoadBaseline(full); err != nil || lowered.TotalInfractions != 1 || lowered.IncreaseRationale != "" {
		t.Fatalf("lowered baseline = %+v, %v; want 1 infraction and no rationale", lowered, err)
	}
}

// TestAdopt_Boundary_BaselineDryRunWritesNothing: a dry run plans every baseline path and
// writes none of them: a first adoption creates no baseline file, a re-adoption and an allowed
// re-record of changed debt leave the recorded file byte for byte, and each still reports.
func TestAdopt_Boundary_BaselineDryRunWritesNothing(t *testing.T) {
	fresh := newTestRepo(t, "dry-first")
	mustWrite(t, filepath.Join(fresh, "main.go"), debtFile)
	first := readoptOptions(t, fresh)
	first.DryRun = true
	rep, err := Adopt(context.Background(), first)
	if err != nil || rep.BaselineStatus != "scanned" || rep.LegacyDebtCount != 1 {
		t.Fatalf("dry first adoption = %v, %+v; want the scanned infraction planned", err, rep)
	}
	if _, err := os.Stat(filepath.Join(fresh, baselineFile)); !os.IsNotExist(err) {
		t.Fatalf("dry first adoption wrote a baseline: %v", err)
	}

	repoPath, recorded := adoptedWithDebt(t, "dry-readopt")
	full := filepath.Join(repoPath, baselineFile)
	mustWrite(t, filepath.Join(repoPath, "added.go"), strings.Replace(debtFile, "run", "added", 1))
	kept := readoptOptions(t, repoPath)
	kept.DryRun = true
	rep, err = Adopt(context.Background(), kept)
	if err != nil || mustRead(t, full) != recorded || rep.BaselineRatchet == nil || rep.BaselineRatchet.Passed {
		t.Fatalf("dry re-adoption = %v, verdict %+v; want the baseline kept and the rejection reported", err, rep.BaselineRatchet)
	}

	planned := rerecordOptions(t, repoPath)
	planned.DryRun, planned.AllowBaselineIncrease, planned.BaselineIncreaseReason = true, true, "planned"
	rep, err = Adopt(context.Background(), planned)
	if err != nil || mustRead(t, full) != recorded || rep.LegacyDebtCount != 2 {
		t.Fatalf("dry re-record = %v, %+v; want 2 infractions planned and the baseline kept", err, rep)
	}
	refused := rerecordOptions(t, repoPath)
	refused.DryRun = true
	if _, err := Adopt(context.Background(), refused); !errors.Is(err, baseline.ErrDebtIncrease) || mustRead(t, full) != recorded {
		t.Fatalf("dry re-record of a higher count = %v; a dry run must refuse it as the real run does", err)
	}
}

// TestAdopt_Negative_BaselineOptionsAndUnreadableBaseline: contradictory baseline options are
// refused before the run writes a file, and an unreadable baseline fails the step on the default
// path and on the re-record path, where it used to be replaced by the rescan.
func TestAdopt_Negative_BaselineOptionsAndUnreadableBaseline(t *testing.T) {
	fresh := newTestRepo(t, "options")
	before := snapshotTree(t, fresh)
	for name, opts := range map[string]AdoptOptions{
		"re-record declined":      {Path: fresh, RerecordBaseline: true},
		"increase without record": {Path: fresh, RecordBaseline: true, AllowBaselineIncrease: true},
		"reason without record":   {Path: fresh, RecordBaseline: true, BaselineIncreaseReason: "why"},
	} {
		if rep, err := Adopt(context.Background(), opts); !errors.Is(err, ErrBaselineOptions) || rep != nil {
			t.Errorf("%s = %v, %+v; want ErrBaselineOptions and no report", name, err, rep)
		}
	}
	if after := snapshotTree(t, fresh); !deepEqual(before, after) {
		t.Fatalf("refused options wrote to the repository: %v", after)
	}

	repoPath, _ := adoptedWithDebt(t, "unreadable")
	full := filepath.Join(repoPath, baselineFile)
	mustWrite(t, full, "{")
	for name, opts := range map[string]AdoptOptions{"default": readoptOptions(t, repoPath), "re-record": rerecordOptions(t, repoPath)} {
		rep, err := Adopt(context.Background(), opts)
		if err == nil || rep.BaselineStatus != "failed" || mustRead(t, full) != "{" {
			t.Errorf("%s over an unreadable baseline = %v, status %q; want a failed step and the file untouched", name, err, rep.BaselineStatus)
		}
	}
}
