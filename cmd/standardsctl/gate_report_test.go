package main

import (
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/gating"
	"github.com/cordanaLLM/praetor/internal/hiss"
)

// The report prints each stage's verdict. A stage that did not run used to print [PASS].
func TestPrintGatingReport_Positive_SkippedStagesNeverPrintPass(t *testing.T) {
	rep := &gating.PipelineReport{
		Status: gating.StatusAdmitted,
		DryRun: true,
		Stages: []gating.StageResult{
			{Name: "HISS Invariant Scan", Status: gating.StagePassed},
			{Name: "Security & SCA Scan", Status: gating.StageSkipped, Message: "dry run: govulncheck and gosec not run"},
			{Name: "Race-Detector Tests", Status: gating.StageNotApplicable, Message: "no go.mod"},
		},
	}
	out, err := captureStdout(t, func() error { printGatingReport(rep); return nil })
	if err != nil {
		t.Fatalf("printGatingReport: %v", err)
	}
	for _, want := range []string{
		"1. [PASS] HISS Invariant Scan",
		"2. [SKIP] Security & SCA Scan",
		"3. [N/A]  Race-Detector Tests",
		"Reason: dry run: govulncheck and gosec not run",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("report missing %q:\n%s", want, out)
		}
	}
	if strings.Count(out, "[PASS]") != 1 {
		t.Errorf("only the stage that ran may print [PASS]:\n%s", out)
	}
}

// A dry run is not refused over an unclean tree, so the report says what a real run would
// refuse; a refused real run already names the reason on its precondition stage, and a clean
// tree adds nothing.
func TestPrintGatingReport_Boundary_UncleanTreeNote(t *testing.T) {
	problem := "1 changed path(s) differ from HEAD: ?? scratch.txt"
	cases := map[string]struct {
		rep  *gating.PipelineReport
		note bool
	}{
		"dry run over an unclean tree": {rep: &gating.PipelineReport{DryRun: true, WorktreeProblem: problem}, note: true},
		"refused real run": {rep: &gating.PipelineReport{Status: gating.StatusRejected, WorktreeProblem: problem,
			Stages: []gating.StageResult{{Name: gating.TreePreconditionStage, Status: gating.StageFailed, Message: problem}}}},
		"clean dry run": {rep: &gating.PipelineReport{DryRun: true, WorktreeClean: true}},
	}
	for name, tc := range cases {
		out, err := captureStdout(t, func() error { printGatingReport(tc.rep); return nil })
		if err != nil {
			t.Fatalf("%s: printGatingReport: %v", name, err)
		}
		hasNote := strings.Contains(out, "Worktree: "+problem) && strings.Contains(out, "without --dry-run refuses")
		if hasNote != tc.note {
			t.Errorf("%s: worktree note printed = %v, want %v:\n%s", name, hasNote, tc.note, out)
		}
	}
}

func TestStageLabel_Negative_FailedAndUnknownVerdicts(t *testing.T) {
	if got := stageLabel(gating.StageFailed); got != "[FAIL]" {
		t.Errorf("failed label = %q", got)
	}
	// An unknown verdict renders as itself, never as a pass.
	if got := stageLabel(gating.StageStatus("bogus")); got != "[BOGUS]" {
		t.Errorf("unknown label = %q", got)
	}
}

// Boundary: the empty verdict of a zero StageResult is not a pass either.
func TestStageLabel_Boundary_ZeroVerdictIsNotAPass(t *testing.T) {
	if got := stageLabel(""); got == "[PASS]" {
		t.Errorf("a zero verdict rendered as %q", got)
	}
}

// Positive and negative: the HISS stage's complexity report prints as the same report-only
// lines the audit prints, after the stages; a run whose HISS stage never scanned prints none.
func TestPrintGatingReport_ComplexityLines(t *testing.T) {
	measured := &hiss.ComplexityReport{Measurements: []hiss.Measurement{{
		RuleID: "HISS-04", FilePath: "a.go", LineNumber: 3, Symbol: "F",
		Kind: hiss.KindCyclomatic, Value: 11, Limit: 10, Severity: hiss.SeverityReport,
	}}}
	out, err := captureStdout(t, func() error {
		printGatingReport(&gating.PipelineReport{Status: gating.StatusAdmitted, Complexity: measured})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	mustContain(t, out, measured.Summary(), measured.Measurements[0].String())
	none, err := captureStdout(t, func() error { printGatingReport(&gating.PipelineReport{Status: gating.StatusAdmitted}); return nil })
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(none, "[REPORT]") {
		t.Errorf("a run without a HISS scan printed complexity lines:\n%s", none)
	}
}
