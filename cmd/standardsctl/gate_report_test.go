package main

import (
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/gating"
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
