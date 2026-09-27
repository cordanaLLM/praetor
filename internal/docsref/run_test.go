// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package docsref

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/testsupport"
)

// fixtureCommands is the fixture CLI's dispatch table, as the binary would hand it over.
var fixtureCommands = map[string]string{
	"state": "runState", "hook": "runHook", "audit": "runAudit", "help": "runHelp", "--help": "runHelp",
}

// fixtureRepository copies testdata/repo into a fresh git work tree and returns its root.
func fixtureRepository(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	if err := os.CopyFS(root, os.DirFS(filepath.Join("testdata", "repo"))); err != nil {
		t.Fatalf("copy fixture: %v", err)
	}
	testsupport.InitGitRepoWithOrigin(t, root, "")
	return root
}

// findingLines renders findings the way the expected-findings file lists them.
func findingLines(findings []Finding) []string {
	lines := make([]string, 0, len(findings))
	for _, finding := range findings {
		lines = append(lines, finding.String())
	}
	return lines
}

// runFixture runs the check over a fresh copy of the fixture repository.
func runFixture(t *testing.T) *Report {
	t.Helper()
	report, err := Run(t.Context(), Options{Root: fixtureRepository(t), Package: "cmd/standardsctl", Commands: fixtureCommands})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	return report
}

// expectedFindings reads the fixture's expected findings, one per line.
func expectedFindings(t *testing.T) []string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", "repo", "expected-findings.txt"))
	if err != nil {
		t.Fatalf("read expected findings: %v", err)
	}
	return strings.Split(strings.TrimSpace(strings.ReplaceAll(string(raw), "\r\n", "\n")), "\n")
}

// TestRun_Positive_FixtureReplaysBothDirections replays the fixture corpus: every reference
// in docs/pass.md and README.md resolves and yields nothing, and every defect planted in
// docs/fail.md, docs/directives.md and the status-less record yields exactly its finding.
func TestRun_Positive_FixtureReplaysBothDirections(t *testing.T) {
	got := findingLines(runFixture(t).Findings)
	want := expectedFindings(t)
	if !slices.Equal(got, want) {
		t.Fatalf("findings differ\n got:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	for _, line := range got {
		if strings.HasPrefix(line, "docs/pass.md:") || strings.HasPrefix(line, "README.md:") {
			t.Errorf("a resolving reference was reported: %s", line)
		}
	}
}

// TestRun_Positive_ReportCountsAndSkips checks that a run says what it read and why it
// skipped a document, so a pass that read nothing cannot pass for one that checked
// everything.
func TestRun_Positive_ReportCountsAndSkips(t *testing.T) {
	report := runFixture(t)
	if report.Documents != 5 || report.Invocations == 0 || report.Paths == 0 {
		t.Errorf("report counts = %d documents, %d invocations, %d paths; want 5 documents and nonzero counts",
			report.Documents, report.Invocations, report.Paths)
	}
	if len(report.Skipped) != 1 || !strings.HasPrefix(report.Skipped[0], "docs/adr/0001-accepted.md: Accepted") {
		t.Errorf("skipped = %q, want only the Accepted record", report.Skipped)
	}
}

// TestRun_Negative_RefusesIncompleteOptionsAndForeignRoots fails closed instead of passing a
// run that could not check anything.
func TestRun_Negative_RefusesIncompleteOptionsAndForeignRoots(t *testing.T) {
	root := fixtureRepository(t)
	if _, err := Run(t.Context(), Options{Root: root, Package: "cmd/standardsctl"}); err == nil {
		t.Error("a run without a command table must fail")
	}
	if _, err := Run(t.Context(), Options{Root: filepath.Join(root, "docs"), Package: "cmd/standardsctl", Commands: fixtureCommands}); err == nil ||
		!strings.Contains(err.Error(), "not the top of its git work tree") {
		t.Errorf("a subdirectory root must be refused, got %v", err)
	}
	commands := map[string]string{"audit": "runMissing"}
	if _, err := Run(t.Context(), Options{Root: root, Package: "cmd/standardsctl", Commands: commands}); err == nil ||
		!strings.Contains(err.Error(), "declares no function runMissing") {
		t.Errorf("a handler the package does not declare must fail the run, got %v", err)
	}
}

// TestRun_Boundary_TrackedDeletionIsSkipped checks that a document still in the index but
// deleted from the work tree is not read, and that removing the only failing documents
// leaves a passing run.
func TestRun_Boundary_TrackedDeletionIsSkipped(t *testing.T) {
	root := fixtureRepository(t)
	for _, rel := range []string{"docs/fail.md", "docs/directives.md", "docs/adr/0002-no-status.md"} {
		if err := os.Remove(filepath.Join(root, filepath.FromSlash(rel))); err != nil {
			t.Fatalf("remove %s: %v", rel, err)
		}
	}
	report, err := Run(t.Context(), Options{Root: root, Package: "cmd/standardsctl", Commands: fixtureCommands})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(report.Findings) != 0 || report.Documents != 2 {
		t.Fatalf("want 2 clean documents, got %d documents and findings %q", report.Documents, findingLines(report.Findings))
	}
}

func TestInScope_Positive_ReadmeAndDocs(t *testing.T) {
	for _, rel := range []string{"README.md", "docs/index.md", "docs/guides/nested/page.md", "docs/README.md"} {
		if !InScope(rel) {
			t.Errorf("InScope(%q) = false, want true", rel)
		}
	}
}

func TestInScope_Negative_RecordsAndOtherFiles(t *testing.T) {
	for _, rel := range []string{"docs/project-records/index.md", "docs/llms.txt", "internal/x/README.md", "AGENTS.md", "sub/README.md"} {
		if InScope(rel) {
			t.Errorf("InScope(%q) = true, want false", rel)
		}
	}
}

func TestFrozenReason_Boundary_StatusSpellings(t *testing.T) {
	cases := []struct {
		rel, content string
		frozen       bool
	}{
		{"docs/adr/0001-a.md", "# A\n\n## Status\n\n**Accepted** — 2026-09-26.\n", true},
		{"docs/adr/0002-b.md", "# B\r\n\r\n## Status\r\n\r\nSuperseded by ADR-0012\r\n", true},
		{"docs/adr/0003-c.md", "# C\n\n## Status\n\nProposed\n", true},
		{"docs/adr/0004-d.md", "# D\n\n## Status\n\nUnderReview\n", false},
		{"docs/adr/0005-e.md", "# E\n\nNo status heading.\n", false},
		{"docs/adr/README.md", "# Index\n\n## Status\n\nAccepted\n", false},
	}
	for _, tc := range cases {
		if got := frozenReason(tc.rel, tc.content) != ""; got != tc.frozen {
			t.Errorf("frozenReason(%q) frozen = %v, want %v", tc.rel, got, tc.frozen)
		}
	}
}

func TestFindingString_Positive_FileLineMessage(t *testing.T) {
	if got := (Finding{Doc: "docs/a.md", Line: 3, Message: "m"}).String(); got != "docs/a.md:3: m" {
		t.Errorf("String() = %q", got)
	}
}
