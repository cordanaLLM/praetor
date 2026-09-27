package main

import (
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/adopt"
	"github.com/cordanaLLM/praetor/internal/bump"
)

// BUG-871: a dry run that recorded errors is INCOMPLETE, not SIMULATED, and the MCP result
// carries the same step-derived pillar lines as the CLI.
func TestFormatAdoptMCPResultOutcomeAndPillars(t *testing.T) {
	failed := &adopt.AdoptReport{
		Errors: []string{"makefile failed"},
		Steps: []adopt.StepOutcome{
			{Name: "dev-container", Status: adopt.StepCompleted, Warnings: []string{"DevContainer bootstrap unavailable"}},
			{Name: "makefile", Status: adopt.StepFailed},
		},
	}
	got := string(formatAdoptMCPResult(failed, true))
	for _, want := range []string{"mode: INCOMPLETE", "Governance Pillars:", "⚠ DevContainer", "✗ Verification Gate", "[failed]", "[not-run]"} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q in:\n%s", want, got)
		}
	}
	if strings.Contains(got, "SIMULATED") || strings.Contains(got, "✓ DevContainer") {
		t.Fatalf("failed dry run rendered as a clean plan:\n%s", got)
	}

	// Positive: a clean dry run is SIMULATED and its completed steps are planned.
	clean := &adopt.AdoptReport{Steps: []adopt.StepOutcome{{Name: "makefile", Status: adopt.StepCompleted}}}
	got = string(formatAdoptMCPResult(clean, true))
	if !strings.Contains(got, "mode: SIMULATED (DRY RUN)") || !strings.Contains(got, "○ Verification Gate") {
		t.Fatalf("clean dry run:\n%s", got)
	}
	// Boundary: the same report applied marks the step ready.
	if got = string(formatAdoptMCPResult(clean, false)); !strings.Contains(got, "mode: APPLIED") || !strings.Contains(got, "✓ Verification Gate") {
		t.Fatalf("clean apply:\n%s", got)
	}
}

// BUG-872: standards_version_audit promises workflow-action auditing, so it lists
// report.Actions exactly as `bump audit` does.
func TestFormatVersionAuditListsActions(t *testing.T) {
	report := &bump.VersionAuditReport{Actions: []bump.ActionCandidate{
		{WorkflowFile: ".github/workflows/ci.yml", Action: "actions/checkout", CurrentVersion: "v4", LatestVersion: "v5"},
		{WorkflowFile: ".github/workflows/ci.yml", Action: "actions/cache", CurrentVersion: "v3", LatestVersion: "v3", Deprecated: true},
	}}
	got := string(formatVersionAudit("repo", report))
	for _, want := range []string{"actions: 2", bump.FormatActionsInventory(report.Actions), "[DRIFT]", "[DEPRECATED]"} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q in:\n%s", want, got)
		}
	}
	// Boundary: zero actions prints the count and no empty inventory heading.
	got = string(formatVersionAudit("repo", &bump.VersionAuditReport{}))
	if !strings.Contains(got, "actions: 0") || strings.Contains(got, "GitHub Actions Inventory") {
		t.Fatalf("empty audit:\n%s", got)
	}
}
