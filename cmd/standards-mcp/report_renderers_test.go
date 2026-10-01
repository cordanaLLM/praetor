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

// A standards_adopt dry run shows each ruleset preview, the text the CLI prints for it
// (adopt.FilePreview.Text); a report without previews, a real run's, shows none.
func TestFormatAdoptMCPResultPrintsPreviews(t *testing.T) {
	create := adopt.FilePreview{Path: ".github/rulesets/main.json", Action: adopt.PreviewCreate,
		Content: "{\"name\": \"praetor-main-protection\"}\n", Note: "2 required status checks"}
	keep := adopt.FilePreview{Path: ".github/rulesets/main.json", Action: adopt.PreviewKeep, Diff: "--- a\n+++ b\n"}
	got := string(formatAdoptMCPResult(&adopt.AdoptReport{Previews: []adopt.FilePreview{create, keep}}, true))
	for _, want := range []string{create.Text(), keep.Text()} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing preview %q in:\n%s", want, got)
		}
	}
	if got = string(formatAdoptMCPResult(&adopt.AdoptReport{}, false)); strings.Contains(got, "--- Preview:") {
		t.Fatalf("a report without previews printed one:\n%s", got)
	}
}

// TestFormatAdoptMCPResultPrintsFacetNotes (#596): standards_adopt prints the default-facet notes
// after the facets line, one per line, as the CLI prints them under Facets (positive); declared
// facets print none (negative); notes on an empty facet list still print (boundary).
func TestFormatAdoptMCPResultPrintsFacetNotes(t *testing.T) {
	notes := []string{"default facets: --facets was omitted", "security:high raises over template-seed alone: x"}
	got := string(formatAdoptMCPResult(&adopt.AdoptReport{Facets: []string{"security:high"}, FacetOrigin: adopt.FacetsDefaulted, FacetNotes: notes}, true))
	if !strings.Contains(got, "facets: security:high.\nfacet note: "+notes[0]+"\nfacet note: "+notes[1]+"\n") {
		t.Fatalf("default facet notes missing after the facets line:\n%s", got)
	}
	if got = string(formatAdoptMCPResult(&adopt.AdoptReport{Facets: []string{"security:high"}, FacetOrigin: adopt.FacetsDeclared}, true)); strings.Contains(got, "facet note:") {
		t.Fatalf("declared facets printed a default note:\n%s", got)
	}
	if got = string(formatAdoptMCPResult(&adopt.AdoptReport{FacetNotes: notes[:1]}, true)); !strings.Contains(got, "facets: .\nfacet note: "+notes[0]+"\n") {
		t.Fatalf("a note on an empty facet list was dropped:\n%s", got)
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

// #613: standards_version_audit states the verdict `bump audit` exits on, and a failed
// report is an error result. Drift alone lowers the count but does not fail the report.
func TestVersionAuditResultFollowsPassed(t *testing.T) {
	passed := versionAuditResult("repo", &bump.VersionAuditReport{TotalScanned: 1, UpToDate: 1, ModernizationScore: 100, Passed: true})
	if passed.IsError || !strings.Contains(passed.Content[0].Text, "current: 1; actions: 0; deprecations: 0; passed: true.") {
		t.Fatalf("passing audit: error=%v\n%s", passed.IsError, passed.Content[0].Text)
	}
	drift := versionAuditResult("repo", &bump.VersionAuditReport{TotalScanned: 2, UpToDate: 1, ModernizationScore: 50, Passed: true})
	if drift.IsError || !strings.Contains(drift.Content[0].Text, "current: 1;") {
		t.Fatalf("drift-only audit: error=%v\n%s", drift.IsError, drift.Content[0].Text)
	}
	failed := versionAuditResult("repo", &bump.VersionAuditReport{TotalScanned: 1, ModernizationScore: 0,
		Deprecations: []bump.DeprecationWarning{{Component: "actions/upload-artifact@v4", Kind: "runner-runtime-deprecated"}}})
	if !failed.IsError || !strings.Contains(failed.Content[0].Text, "deprecations: 1; passed: false.") {
		t.Fatalf("failed audit: error=%v\n%s", failed.IsError, failed.Content[0].Text)
	}
}
