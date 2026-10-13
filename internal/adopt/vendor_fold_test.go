package adopt

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/clientid"
)

// authoredRules is a CLAUDE.md a person wrote, the case #1073 found replaced and dropped.
const authoredRules = "# Project rules for Claude\n\n- Always run make test before committing.\n- Never touch the vendored/ directory.\n"

// foldSession returns a session over a fresh directory holding files, for the fold step alone.
func foldSession(t *testing.T, dryRun bool, files map[string]string) *adoptSession {
	t.Helper()
	repo := t.TempDir()
	for rel, content := range files {
		mustWrite(t, filepath.Join(repo, filepath.FromSlash(rel)), content)
	}
	return &adoptSession{repoPath: repo, report: &AdoptReport{}, opts: AdoptOptions{Path: repo, DryRun: dryRun}}
}

// fold runs the fold step for the Claude and Gemini vendor files with prior as the projections
// of AGENTS.md the run found, and returns AGENTS.md as the run leaves it.
func fold(t *testing.T, s *adoptSession, prior priorVendorProjections) string {
	t.Helper()
	if err := foldAuthoredVendorContext(t.Context(), s, []string{string(clientid.Claude), string(clientid.Gemini)}, prior); err != nil {
		t.Fatalf("fold: %v", err)
	}
	data, _, err := s.agentsSource(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// Positive: an authored CLAUDE.md with no AGENTS.md becomes AGENTS.md under its fold notice; with
// a hand-written AGENTS.md it is appended below it, which stays as written; the fold is warned.
func TestFoldAuthoredVendorContext_Positive_KeepsAuthoredText(t *testing.T) {
	s := foldSession(t, false, map[string]string{vendorContextFile: authoredRules})
	got := fold(t, s, priorVendorProjections{})
	want := "<!-- Folded by praetorctl adopt from CLAUDE.md: text that file held and AGENTS.md lacked. -->\n\n" + authoredRules
	if got != want || mustRead(t, filepath.Join(s.repoPath, agentsFile)) != want {
		t.Fatalf("AGENTS.md = %q, want %q", got, want)
	}
	if !hasWarningContaining(s.report, "CLAUDE.md held text AGENTS.md lacked") {
		t.Fatalf("fold not warned: %v", s.report.Warnings)
	}

	own := "# Team instructions\n\nUse the staging cluster for load tests.\n"
	s = foldSession(t, false, map[string]string{agentsFile: own, vendorContextFile: authoredRules})
	if got := fold(t, s, priorVendorProjections{}); !strings.HasPrefix(got, own+"\n<!-- Folded") || !strings.HasSuffix(got, authoredRules) {
		t.Fatalf("AGENTS.md = %q, want its own text first and the folded rules after", got)
	}
}

// Negative: nothing is folded, and AGENTS.md is not written, for a vendor file that is the
// projection of AGENTS.md the run found, an authored one whose every line AGENTS.md already
// holds, or none at all.
func TestFoldAuthoredVendorContext_Negative_FoldsNothingAGENTSHolds(t *testing.T) {
	for name, files := range map[string]map[string]string{
		"prior projection": {agentsFile: "x\n", vendorContextFile: "projection\n"},
		"copy of AGENTS":   {agentsFile: "# Shared\n\n" + authoredRules, vendorContextFile: authoredRules},
		"no vendor file":   {agentsFile: "x\n"},
	} {
		t.Run(name, func(t *testing.T) {
			s := foldSession(t, false, files)
			prior := priorVendorProjections{}
			prior.record(vendorContextFile, "projection\n", agentsFile)
			if got := fold(t, s, prior); got != files[agentsFile] || len(s.report.Warnings) != 0 {
				t.Fatalf("AGENTS.md = %q, warnings %v; want it unchanged and no warning", got, s.report.Warnings)
			}
		})
	}
}

// Boundary: a dry run plans the folded AGENTS.md and writes nothing; an edited projection folds
// only its added line, not its compile-context frame or the lines AGENTS.md holds; a CRLF
// AGENTS.md keeps CRLF; two vendor files holding the same text fold it once.
func TestFoldAuthoredVendorContext_Boundary_DryRunProjectionsAndEndings(t *testing.T) {
	s := foldSession(t, true, map[string]string{vendorContextFile: authoredRules})
	if got := fold(t, s, priorVendorProjections{}); !strings.HasSuffix(got, authoredRules) || fileExists(filepath.Join(s.repoPath, agentsFile)) {
		t.Fatalf("dry run planned %q and wrote AGENTS.md: %v", got, fileExists(filepath.Join(s.repoPath, agentsFile)))
	}

	agents := "# Harness\n\n- Rule one.\n"
	projection := "<!-- markdownlint-disable MD013 -->\n<!-- Compiled automatically by praetorctl compile-context from AGENTS.md. DO NOT EDIT DIRECTLY. -->\n\n" +
		agents + "- Local note an operator added.\n"
	s = foldSession(t, false, map[string]string{agentsFile: agents, vendorContextFile: projection})
	if got := fold(t, s, priorVendorProjections{}); !strings.HasSuffix(got, "-->\n\n- Local note an operator added.\n") || strings.Count(got, "Rule one") != 1 {
		t.Fatalf("edited projection folded %q, want only its added line", got)
	}

	s = foldSession(t, false, map[string]string{agentsFile: "# Own\r\n", vendorContextFile: authoredRules})
	if got := fold(t, s, priorVendorProjections{}); strings.Count(got, "\r\n") != strings.Count(got, "\n") {
		t.Fatalf("CRLF AGENTS.md came back with mixed endings: %q", got)
	}

	s = foldSession(t, false, map[string]string{vendorContextFile: authoredRules, ".gemini/GEMINI.md": authoredRules})
	if got := fold(t, s, priorVendorProjections{}); strings.Count(got, "Always run make test") != 1 {
		t.Fatalf("the same text from two vendor files was folded twice: %q", got)
	}
}
