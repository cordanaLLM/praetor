package adopt

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/baseline"
)

// TestAdopt_RejectedKeptBaselineIsPending_3D pins the shape #358 was reported in, on the verdict
// that closes the run: a repository adopted without debt records a baseline with zero entries and
// then gains one finding. Positive: the first adoption and a re-adoption of the clean tree leave
// the baseline out of the pending pillars. Negative: a default re-adoption, forced included,
// keeps the baseline byte for byte, applies without an error, and counts the rejected baseline as
// pending, with the verdict and each way to resolve it in the closing lines. Boundary: once the
// finding is fixed the baseline is no longer pending.
func TestAdopt_RejectedKeptBaselineIsPending_3D(t *testing.T) {
	repoPath := newTestRepo(t, "pending-baseline")
	full := filepath.Join(repoPath, baselineFile)
	for _, run := range []string{"first adoption", "clean re-adoption"} {
		rep, err := Adopt(context.Background(), readoptOptions(t, repoPath))
		if err != nil || slices.Contains(rep.PendingPillars(), debtBaselinePillar) || rep.PendingBaseline() != nil {
			t.Fatalf("%s = %v; pending %q, closing %q; want no pending baseline", run, err, rep.PendingPillars(), rep.PendingBaseline())
		}
	}
	recorded := mustRead(t, full)
	if zero, err := baseline.LoadBaseline(full); err != nil || zero.Count() != 0 || len(zero.Infractions) != 0 {
		t.Fatalf("first baseline = %+v, %v; want zero entries", zero, err)
	}

	added := filepath.Join(repoPath, "added.go")
	mustWrite(t, added, debtFile)
	for name, force := range map[string]bool{"plain": false, "forced": true} {
		opts := readoptOptions(t, repoPath)
		opts.Force = force
		rep, err := Adopt(context.Background(), opts)
		if err != nil || rep.Outcome() != OutcomeApplied || mustRead(t, full) != recorded {
			t.Fatalf("%s re-adoption = %v, outcome %s; want an applied run that keeps the baseline byte for byte", name, err, rep.Outcome())
		}
		if verdict := rep.BaselineRatchet; verdict == nil || verdict.Passed || verdict.Recorded != 0 || verdict.Active != 1 || verdict.Unbaselined != 1 {
			t.Fatalf("%s verdict = %+v; want a rejection at 1 active, 1 unbaselined, 0 recorded", name, verdict)
		}
		if !slices.Contains(rep.PendingPillars(), debtBaselinePillar) {
			t.Fatalf("%s pending = %q; want the rejected baseline to hold back the success line", name, rep.PendingPillars())
		}
		closing := strings.Join(rep.PendingBaseline(), "\n")
		for _, want := range []string{
			debtBaselinePillar + ": " + rep.BaselineRatchet.Line(), "ratchet rejects: 1 active infractions against 0 recorded, 1 not in the baseline",
			"fix the findings", "'praetorctl adopt --rerecord-baseline --allow-increase --reason=<why>'",
			"'praetorctl baseline --record --allow-increase --reason=<why>'", "praetorctl audit rejects the repository",
		} {
			if !strings.Contains(closing, want) {
				t.Errorf("%s closing lines lack %q:\n%s", name, want, closing)
			}
		}
	}

	if err := os.Remove(added); err != nil {
		t.Fatal(err)
	}
	rep, err := Adopt(context.Background(), readoptOptions(t, repoPath))
	if err != nil || slices.Contains(rep.PendingPillars(), debtBaselinePillar) || rep.PendingBaseline() != nil || mustRead(t, full) != recorded {
		t.Fatalf("fixed re-adoption = %v; pending %q; want the baseline kept and no longer pending", err, rep.PendingPillars())
	}
}

// TestPendingPillars_Baseline_3D: the baseline is pending only on a rejecting verdict, and it is
// named after the Verification Gate when both hold the success line back. A run that recorded,
// re-recorded or did not scan carries no verdict, and a verdict that passes, a stale entry's
// warning included, is not pending.
func TestPendingPillars_Baseline_3D(t *testing.T) {
	ready := []StepOutcome{{Name: verificationStep, Status: StepCompleted}}
	rejected := &BaselineRatchet{Recorded: 1, Active: 2, Unbaselined: 1}
	for name, tc := range map[string]struct {
		report *AdoptReport
		want   []string
	}{
		"no verdict":      {&AdoptReport{Steps: ready}, nil},
		"passing verdict": {&AdoptReport{Steps: ready, BaselineRatchet: &BaselineRatchet{Passed: true, Recorded: 2, Active: 1}, Warnings: []string{"stale"}}, nil},
		"rejected":        {&AdoptReport{Steps: ready, BaselineRatchet: rejected}, []string{debtBaselinePillar}},
		"rejected beside an unavailable plan": {
			&AdoptReport{Steps: ready, BaselineRatchet: rejected, Verification: &VerificationPlan{Status: verificationUnavailable}},
			[]string{"Verification Gate", debtBaselinePillar},
		},
	} {
		if got := tc.report.PendingPillars(); !slices.Equal(got, tc.want) {
			t.Errorf("%s: PendingPillars() = %q, want %q", name, got, tc.want)
		}
		if closing := tc.report.PendingBaseline(); (len(closing) == 2) != slices.Contains(tc.want, debtBaselinePillar) {
			t.Errorf("%s: PendingBaseline() = %q", name, closing)
		}
	}
}
