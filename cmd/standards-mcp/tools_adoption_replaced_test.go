package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/adopt"
)

// replacedAdoptReport is a report whose lefthook.yml was replaced and whose README was verified.
func replacedAdoptReport() *adopt.AdoptReport {
	return &adopt.AdoptReport{
		BaselineStatus:  "skipped",
		ReconciledFiles: []string{"README.md", "lefthook.yml"},
		ActionDetails: []adopt.ActionDetail{
			{Path: "README.md", Action: "reconcile", Details: "Governance block verified"},
			{Path: "lefthook.yml", Action: "replace", Details: "Scaffolded; replaced existing content (-1/+9 lines); backup: .workingdir/adopt-backups/x/lefthook.yml"},
		},
	}
}

// Positive: a replaced file gets its own section carrying the line delta and backup, and is not
// listed among the reconciled files; a dry run labels the section as planned.
func TestFormatAdoptMCPResult_Positive_ReplacedSection(t *testing.T) {
	got := string(formatAdoptMCPResult(replacedAdoptReport(), false))
	for _, want := range []string{"Reconciled Files: 1\n  ~ README.md\n", "Replaced Files: 1\n",
		"  ! lefthook.yml: Scaffolded; replaced existing content (-1/+9 lines); backup: .workingdir/adopt-backups/x/lefthook.yml\n"} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q in:\n%s", want, got)
		}
	}
	if strings.Contains(got, "~ lefthook.yml") {
		t.Fatalf("replaced file listed as reconciled:\n%s", got)
	}
	plannedReport := replacedAdoptReport()
	preview := adopt.FilePreview{Path: ".github/rulesets/main.json", Action: adopt.PreviewUpdate, Diff: "--- a\n+++ b\n"}
	plannedReport.Previews = []adopt.FilePreview{preview}
	planned := string(formatAdoptMCPResult(plannedReport, true))
	if !strings.Contains(planned, "Planned Reconciliations: 1\n") || !strings.Contains(planned, "Planned Replacements: 1\n  ! lefthook.yml: ") ||
		strings.Contains(planned, "Replaced Files") {
		t.Fatalf("dry run replaced section:\n%s", planned)
	}
	// A dry run under --force carries both: the file sections first, then the previews, as the
	// CLI prints them (printAdoptedFiles, then printAdoptPreviews).
	replacedAt, previewAt := strings.Index(planned, "Planned Replacements"), strings.Index(planned, preview.Text())
	if previewAt < 0 || replacedAt > previewAt {
		t.Fatalf("the preview must follow the replaced section (replaced at %d, preview at %d):\n%s", replacedAt, previewAt, planned)
	}
}

// Negative: a report without a replace entry prints no replaced section and keeps every
// reconciled file.
func TestFormatAdoptMCPResult_Negative_NoReplacedSection(t *testing.T) {
	rep := replacedAdoptReport()
	rep.ActionDetails[1].Action = "merge"
	got := string(formatAdoptMCPResult(rep, false))
	if strings.Contains(got, "Replaced Files") || strings.Contains(got, "  ! ") {
		t.Fatalf("replaced section without a replace entry:\n%s", got)
	}
	if !strings.Contains(got, "Reconciled Files: 2\n  ~ README.md\n  ~ lefthook.yml\n") {
		t.Fatalf("reconciled files lost:\n%s", got)
	}
}

// Boundary: when every reconciled file was replaced, the reconciled count reads 0 and the
// replaced section still lists the file once.
func TestFormatAdoptMCPResult_Boundary_OnlyReplacedFiles(t *testing.T) {
	rep := replacedAdoptReport()
	rep.ReconciledFiles, rep.ActionDetails = []string{"lefthook.yml"}, rep.ActionDetails[1:]
	got := string(formatAdoptMCPResult(rep, false))
	if !strings.Contains(got, "Reconciled Files: 0\n") || !strings.Contains(got, "Replaced Files: 1\n") ||
		strings.Count(got, "lefthook.yml:") != 1 {
		t.Fatalf("only-replaced report:\n%s", got)
	}
}

// Positive, through the real tool: standards_adopt with force over a foreign lefthook.yml
// plans, then reports, a replace with its line delta, and never lists the file as reconciled;
// the plan without force keeps the file and plans no replace.
func TestServer_Positive_AdoptForceReportsReplacedFile(t *testing.T) {
	root := t.TempDir()
	initGitRepo(t, root)
	source, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	srv, err := NewServerWithOptions(ServerOptions{RootDir: root, Version: "test", AllowOutsideRoot: true})
	if err != nil {
		t.Fatal(err)
	}
	foreign := "pre-commit:\n  commands: {}\n"
	writeFixtureFile(t, root, "lefthook.yml", foreign)
	args := map[string]any{"profile": "planning-artifacts", "facets": "agent:sandboxed", "source_root": source, "record_baseline": false, "dry_run": true}
	kept := callTool(t, srv, "standards_adopt", args)
	expectText(t, "adopt without force", kept, "mode: SIMULATED (DRY RUN)")
	if strings.Contains(kept.Content[0].Text, "Planned Replacements") {
		t.Fatalf("replace planned without force:\n%s", kept.Content[0].Text)
	}
	args["force"] = true
	planned := callTool(t, srv, "standards_adopt", args)
	expectText(t, "adopt force plan", planned, "Planned Replacements: ")
	expectText(t, "adopt force plan", planned, "  ! lefthook.yml: ")
	if after := mustReadFile(t, filepath.Join(root, "lefthook.yml")); after != foreign {
		t.Fatalf("dry run wrote lefthook.yml: %q", after)
	}
	args["dry_run"] = false
	applied := callTool(t, srv, "standards_adopt", args)
	expectText(t, "adopt force apply", applied, "mode: APPLIED")
	text := applied.Content[0].Text
	if !strings.Contains(text, "Replaced Files: ") || !strings.Contains(text, "  ! lefthook.yml: ") ||
		!strings.Contains(text, "replaced existing content (-") || strings.Contains(text, "  ~ lefthook.yml\n") {
		t.Fatalf("force apply did not report the replace:\n%s", text)
	}
	if after := mustReadFile(t, filepath.Join(root, "lefthook.yml")); after == foreign {
		t.Fatal("force apply left the foreign lefthook.yml in place")
	}
}

// mustReadFile returns the file's content or fails the test.
func mustReadFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}
