package adopt

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
)

func pillarNamed(t *testing.T, r *AdoptReport, name string) Pillar {
	t.Helper()
	for _, pillar := range r.Pillars() {
		if pillar.Name == name {
			return pillar
		}
	}
	t.Fatalf("no pillar %q", name)
	return Pillar{}
}

// BUG-871: errors win over a dry run, so a failed plan is never labelled simulated.
func TestAdoptReportOutcome(t *testing.T) {
	cases := []struct {
		report AdoptReport
		want   AdoptOutcome
	}{
		{AdoptReport{}, OutcomeApplied},
		{AdoptReport{DryRun: true}, OutcomeSimulated},
		{AdoptReport{Errors: []string{"boom"}}, OutcomeIncomplete},
		{AdoptReport{DryRun: true, Errors: []string{"boom"}}, OutcomeIncomplete},
		{AdoptReport{DryRun: true, Warnings: []string{"soft"}}, OutcomeSimulated},
	}
	for _, tc := range cases {
		if got := tc.report.Outcome(); got != tc.want {
			t.Errorf("Outcome(%+v) = %s, want %s", tc.report, got, tc.want)
		}
	}
}

// BUG-871: each pillar line follows the step that owns it; a warning on one step marks only
// that step's pillars.
func TestPillarsFollowTheirStep(t *testing.T) {
	r := &AdoptReport{}
	r.recordStep("agent-harness", StepCompleted, stepMark{})
	r.addWarning("DevContainer bootstrap unavailable: no runtime")
	r.recordStep("dev-container", StepCompleted, stepMark{})
	r.recordStep("editors", StepDeclined, r.mark())
	r.recordStep("makefile", StepFailed, r.mark())

	want := map[string]PillarStatus{
		"Universal Harness": PillarReady, "AI Context Sync": PillarReady,
		"DevContainer": PillarWarned, "IDE Ecosystem": PillarDeclined, "Verification Gate": PillarFailed,
	}
	for name, status := range want {
		if got := pillarNamed(t, r, name); got.Status != status {
			t.Errorf("%s = %s, want %s", name, got.Status, status)
		}
	}
	devcontainer := pillarNamed(t, r, "DevContainer")
	if line := devcontainer.Line(); strings.HasPrefix(line, "✓") || !strings.Contains(line, "[warned: 1 warning(s)]") {
		t.Errorf("warned DevContainer line = %q", line)
	}
	if line := pillarNamed(t, r, "Verification Gate").Line(); !strings.HasPrefix(line, "✗") || !strings.HasSuffix(line, "[failed]") {
		t.Errorf("failed line = %q", line)
	}
	if line := pillarNamed(t, r, "Universal Harness").Line(); !strings.HasPrefix(line, "✓ Universal Harness") || strings.Contains(line, "[") {
		t.Errorf("ready line = %q", line)
	}
}

// Boundary: a report without step outcomes claims no pillar, and a dry run's completed step
// is planned rather than ready.
func TestPillarsWithoutStepsOrInDryRun(t *testing.T) {
	for _, pillar := range (&AdoptReport{}).Pillars() {
		if pillar.Status != PillarNotRun || !strings.HasSuffix(pillar.Line(), "[not-run]") {
			t.Errorf("unreached pillar %s = %s (%q)", pillar.Name, pillar.Status, pillar.Line())
		}
	}
	dry := &AdoptReport{DryRun: true}
	dry.recordStep("makefile", StepCompleted, stepMark{})
	if got := pillarNamed(t, dry, "Verification Gate"); got.Status != PillarPlanned {
		t.Errorf("dry-run completed step = %s, want planned", got.Status)
	}
	// A negative or past-the-end offset attributes nothing.
	dry.addWarning("unrelated")
	dry.addError("unrelated")
	dry.recordStep("editors", StepCompleted, stepMark{warnings: -1, errors: -1})
	dry.recordStep("dev-container", StepCompleted, stepMark{warnings: 5, errors: 5})
	if got := pillarNamed(t, dry, "IDE Ecosystem"); len(got.Warnings) != 0 || len(got.Errors) != 0 || got.Status != PillarPlanned {
		t.Errorf("an invalid offset attributed warnings or errors: %+v", got)
	}
}

// The chain records every step it reaches, in order, and a declined step as declined.
func TestAdoptRecordsStepOutcomes(t *testing.T) {
	repoPath := newTestRepo(t, "step-outcomes")
	mustWrite(t, filepath.Join(repoPath, manifestFile), "version: 1\nadoption:\n  decline:\n    - editors\n")
	rep, err := Adopt(context.Background(), AdoptOptions{LockSourceRoot: newAdoptLockSource(t), Path: repoPath, DryRun: true})
	if err != nil {
		t.Fatalf("adopt: %v", err)
	}
	steps := adoptSteps()
	if len(rep.Steps) != len(steps) {
		t.Fatalf("recorded %d steps, chain has %d: %+v", len(rep.Steps), len(steps), rep.Steps)
	}
	for i, step := range steps {
		want := StepCompleted
		if step.name == "editors" {
			want = StepDeclined
		}
		if rep.Steps[i].Name != step.name || rep.Steps[i].Status != want {
			t.Errorf("step %d = %+v, want %s %s", i, rep.Steps[i], step.name, want)
		}
	}
	if got := pillarNamed(t, rep, "IDE Ecosystem"); got.Status != PillarDeclined {
		t.Errorf("declined editors pillar = %s", got.Status)
	}
}

// A step that records an error and returns nil (harness.go under --force without a harness
// boundary) failed: its pillars are failed, never planned or ready, and only its pillars.
func TestPillarsFailOnARecordedStepError(t *testing.T) {
	for _, dryRun := range []bool{true, false} {
		r := &AdoptReport{DryRun: dryRun}
		r.recordStep("makefile", StepCompleted, r.mark())
		from := r.mark()
		r.addError("AGENTS.md: cannot locate the end of the existing harness")
		r.recordStep("agent-harness", StepCompleted, from)
		if step := r.Steps[1]; step.Status != StepFailed || len(step.Errors) != 1 {
			t.Fatalf("dryRun=%v: soft error step = %+v", dryRun, step)
		}
		for _, name := range []string{"Universal Harness", "AI Context Sync"} {
			pillar := pillarNamed(t, r, name)
			if pillar.Status != PillarFailed || !strings.HasPrefix(pillar.Line(), "✗") || !strings.HasSuffix(pillar.Line(), "[failed: 1 error(s)]") {
				t.Errorf("dryRun=%v: %s = %s (%q)", dryRun, name, pillar.Status, pillar.Line())
			}
		}
		if got := pillarNamed(t, r, "Verification Gate"); got.Status == PillarFailed || len(got.Errors) != 0 {
			t.Errorf("dryRun=%v: an earlier step inherited the error: %+v", dryRun, got)
		}
	}
	// A report built outside the chain with errors on a completed step still reads failed.
	external := &AdoptReport{Steps: []StepOutcome{{Name: "editors", Status: StepCompleted, Errors: []string{"boom"}}}}
	if got := pillarNamed(t, external, "IDE Ecosystem"); got.Status != PillarFailed {
		t.Errorf("completed step carrying errors = %s, want failed", got.Status)
	}
}

// End to end: adopt --force on an AGENTS.md whose harness has no boundary records the error on
// the agent-harness step and fails both of its pillars.
func TestAdoptForceWithoutHarnessBoundaryFailsItsPillars(t *testing.T) {
	repoPath := newTestRepo(t, "soft-error")
	mustWrite(t, filepath.Join(repoPath, agentsFile), "# Agent Operating Harness\n")
	rep, err := Adopt(context.Background(), AdoptOptions{LockSourceRoot: newAdoptLockSource(t), Path: repoPath, DryRun: true, Force: true})
	if err != nil {
		t.Fatalf("adopt: %v", err)
	}
	if rep.Outcome() != OutcomeIncomplete {
		t.Fatalf("outcome = %s, errors %v", rep.Outcome(), rep.Errors)
	}
	for _, name := range []string{"Universal Harness", "AI Context Sync"} {
		if got := pillarNamed(t, rep, name); got.Status != PillarFailed || len(got.Errors) == 0 {
			t.Errorf("%s = %+v, want failed with the boundary error", name, got)
		}
	}
}
