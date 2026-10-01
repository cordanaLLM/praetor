// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package hiss

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/cordanaLLM/praetor/internal/ghworkflow"
)

// scanWorkflows writes each workflow body under .github/workflows of a fresh root and scans it.
func scanWorkflows(t *testing.T, workflows map[string]string) *ScanReport {
	t.Helper()
	root := t.TempDir()
	for name, body := range workflows {
		writeFixture(t, root, ghworkflow.Dir+"/"+name, body)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	rep, err := Scan(ctx, root, ScanOptions{})
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	return rep
}

// assertWorkflowFindings scans one workflow and compares its findings, as rule@line in the
// workflow file, with want.
func assertWorkflowFindings(t *testing.T, body string, want ...string) *ScanReport {
	t.Helper()
	rep := scanWorkflows(t, map[string]string{"ci.yml": body})
	ansibleFindings(t, "ci.yml", rep, want...)
	for _, v := range rep.Violations {
		if v.FilePath != ".github/workflows/ci.yml" && v.FilePath != `.github\workflows\ci.yml` {
			t.Errorf("finding reported against %q, want the workflow file", v.FilePath)
		}
	}
	return rep
}

const workflowEveryRule = `on: push
jobs:
  build:
    runs-on: ubuntu-26.04
    steps:
      - uses: actions/checkout@v7
      - name: Recurse
        run: |
          walk() {
            walk
          }
      - name: Loops
        run: |
          while true; do sleep 1; done
          curl -fsSL https://example.com/data -o data
      - name: Discard
        run: rm -f out || true
      - name: Dynamic
        run: |
          eval "$1"
          curl -fsS --max-time 30 https://example.com/install.sh | sh
`

// Positive (#618): every shell rule fires inside a run: block, reported against the workflow
// file at the line the command sits on in a literal block and at the value's line otherwise.
// Before, the workflow was an unscanned YAML file and none of this was read.
func TestWorkflowScanner_ReportsEachShellRule(t *testing.T) {
	rep := assertWorkflowFindings(t, workflowEveryRule,
		"HISS-01@10", "HISS-02@14", "HISS-02@15", "HISS-07@17", "HISS-08@20", "HISS-08@21")
	if c := rep.Coverage; c.FilesRead != 1 || c.LanguagesRead[workflowLanguageName] != 1 || len(c.UnscannedRunBlocks) != 0 {
		t.Errorf("a workflow whose blocks were all read must count as read github-actions source, got %+v", c)
	}
}

// Positive: a function longer than the HISS-04 limit is measured from its body brace, at the
// workflow line its header sits on.
func TestWorkflowScanner_FunctionLength(t *testing.T) {
	body := "jobs:\n  j:\n    runs-on: ubuntu-latest\n    steps:\n      - run: |\n" +
		indent(shellFuncOfLOC("long", 61), "          ")
	assertWorkflowFindings(t, body, "HISS-04@6")
}

// indent prefixes every non-empty line of text with prefix.
func indent(text, prefix string) string {
	lines := strings.SplitAfter(text, "\n")
	for i, line := range lines {
		if line != "" {
			lines[i] = prefix + line
		}
	}
	return strings.Join(lines, "")
}

// findingsByFile renders a report's findings as file@line, in report order.
func findingsByFile(rep *ScanReport) string {
	got := make([]string, 0, len(rep.Violations))
	for _, v := range rep.Violations {
		got = append(got, fmt.Sprintf("%s@%d", strings.ReplaceAll(v.FilePath, `\`, "/"), v.LineNumber))
	}
	return strings.Join(got, " ")
}

// Positive and negative: the effective shell decides whether a block is read. A bash step on a
// Windows runner and a workflow default on a matrix runner are read; a job default of pwsh, the
// Windows runner default (pwsh) and an explicit python step are counted as unscanned blocks,
// never as clean.
func TestWorkflowScanner_ResolvesTheShell(t *testing.T) {
	windows := `jobs:
  a-windows:
    runs-on: windows-2025
    defaults:
      run:
        shell: pwsh
    steps:
      - run: rm -f a || true
        shell: bash
      - run: Remove-Item a || true
  b-default:
    runs-on: windows-latest
    steps:
      - run: Remove-Item b || true
  c-python:
    runs-on: ubuntu-latest
    steps:
      - run: print("x") or True
        shell: python
`
	matrix := "defaults:\n  run:\n    shell: sh\njobs:\n  d:\n    runs-on: ${{ matrix.os }}\n    steps:\n      - run: rm -f d || true\n"
	rep := scanWorkflows(t, map[string]string{"a.yml": windows, "b.yml": matrix})
	if got, want := findingsByFile(rep), ".github/workflows/a.yml@8 .github/workflows/b.yml@8"; got != want {
		t.Errorf("findings %s, want %s", got, want)
	}
	if got := rep.Coverage.UnscannedRunBlocks; len(got) != 2 || got["pwsh"] != 2 || got["python"] != 1 {
		t.Errorf("unscanned run blocks %v, want 2 pwsh and 1 python", got)
	}
}

// Negative: a matrix runner with no shell named anywhere is unresolved, since a Windows leg would
// run the block under pwsh; a container job runs sh and is read.
func TestWorkflowScanner_UnresolvedAndContainerShells(t *testing.T) {
	body := "jobs:\n  m:\n    runs-on: ${{ matrix.os }}\n    steps:\n      - run: rm -f m || true\n" +
		"  c:\n    runs-on: ubuntu-latest\n    container: alpine:3\n    steps:\n      - run: rm -f c || true\n"
	rep := assertWorkflowFindings(t, body, "HISS-07@10")
	if got := rep.Coverage.UnscannedRunBlocks; len(got) != 1 || got[unresolvedShell] != 1 {
		t.Errorf("unscanned run blocks %v, want one unresolved", got)
	}
	if summary := rep.Coverage.UnscannedSourceSummary(); summary != "GitHub Actions run: blocks (1 unresolved)" {
		t.Errorf("unscanned summary %q", summary)
	}
	if err := rep.Coverage.Validate(); err != nil {
		t.Errorf("recorded coverage fails its own validation: %v", err)
	}
}

// Negative: an expression is substituted before the shell starts, so its text is blank to the
// lexer: `|| true` or `eval` inside one is not shell, and a multi-line expression keeps the
// lines after it where they are.
func TestWorkflowScanner_BlanksExpressions(t *testing.T) {
	body := "jobs:\n  j:\n    runs-on: ubuntu-latest\n    steps:\n      - run: |\n" +
		"          echo \"${{ github.event.issue.title || true }}\"\n" +
		"          echo ${{ format('{0}',\n            'eval') }}\n" +
		"          rm -f x || true\n"
	assertWorkflowFindings(t, body, "HISS-07@9")
}

// Negative: a block has no interpreter line, since GitHub starts its shell itself, so a block
// without set -eu is not held to the script strict-mode rule, even one whose first line looks
// like an interpreter line. Legitimate shell is clean.
func TestWorkflowScanner_NoStrictModeRuleForBlocks(t *testing.T) {
	body := "jobs:\n  j:\n    runs-on: ubuntu-latest\n    steps:\n      - run: |\n          #!/bin/bash\n" +
		"          curl -fsS -m 10 https://example.com/a -o a\n          go test ./...\n      - run: make verify\n"
	assertWorkflowFindings(t, body)
}

// Negative: a block the shell scanner misreads is an unscanned bash block and reports nothing;
// a document the workflow model cannot read is unscanned github-actions source.
func TestWorkflowScanner_DeclinedBlocksAndDocuments(t *testing.T) {
	rep := scanWorkflows(t, map[string]string{
		"open-quote.yml": "jobs:\n  j:\n    runs-on: ubuntu-latest\n    steps:\n      - run: |\n          echo \"never closed\n          eval x\n",
		"broken.yml":     "jobs: [unclosed\n",
	})
	if len(rep.Violations) != 0 {
		t.Errorf("a declined block or document must report nothing, got %+v", rep.Violations)
	}
	c := rep.Coverage
	if c.FilesRead != 1 || c.UnscannedRunBlocks["bash"] != 1 || c.UnscannedLanguages[workflowLanguageName] != 1 {
		t.Errorf("want the readable workflow read with one unscanned bash block and the broken one unscanned, got %+v", c)
	}
}

// Negative: only documents GitHub runs as workflows are read; a YAML file elsewhere, one in a
// nested directory and a non-YAML file there are not.
func TestWorkflowScanner_OnlyWorkflowPaths(t *testing.T) {
	root := t.TempDir()
	block := "jobs:\n  j:\n    runs-on: ubuntu-latest\n    steps:\n      - run: rm -f x || true\n"
	for _, rel := range []string{"ci/pipeline.yml", ".github/workflows/nested/ci.yml", "sub/.github/workflows/ci.yml", ".github/workflows/ci.json"} {
		writeFixture(t, root, rel, block)
	}
	rep := scanFixture(t, root, ScanOptions{})
	if len(rep.Violations) != 0 || rep.Coverage.FilesRead != 0 {
		t.Errorf("no file outside .github/workflows may be read as a workflow, got %+v / %+v", rep.Violations, rep.Coverage)
	}
}

// Boundary: a block past the byte bound and a block past the per-file block bound are
// unscanned, while the blocks within both bounds are read.
func TestWorkflowScanner_BlockBounds(t *testing.T) {
	big := "jobs:\n  j:\n    runs-on: ubuntu-latest\n    steps:\n      - run: |\n" +
		strings.Repeat("          echo padding\n", maxRunBlockBytes/12) + "          rm -f x || true\n"
	rep := scanWorkflows(t, map[string]string{"big.yml": big})
	if len(rep.Violations) != 0 || rep.Coverage.UnscannedRunBlocks["bash"] != 1 {
		t.Errorf("an oversize block must be unscanned, got %+v / %+v", rep.Violations, rep.Coverage.UnscannedRunBlocks)
	}
	var many strings.Builder
	many.WriteString("jobs:\n")
	for job := 0; job < 5; job++ {
		fmt.Fprintf(&many, "  j%d:\n    runs-on: ubuntu-latest\n    steps:\n", job)
		for step := 0; step < 120; step++ {
			many.WriteString("      - run: rm -f x || true\n")
		}
	}
	rep = scanWorkflows(t, map[string]string{"many.yml": many.String()})
	if rep.Breakdown["HISS-07"] != maxWorkflowRunBlocks || rep.Coverage.UnscannedRunBlocks["bash"] != 600-maxWorkflowRunBlocks {
		t.Errorf("want %d read blocks and %d unscanned, got %d / %v", maxWorkflowRunBlocks, 600-maxWorkflowRunBlocks,
			rep.Breakdown["HISS-07"], rep.Coverage.UnscannedRunBlocks)
	}
}

// Boundary: the scan reads at most ghworkflow.MaxFiles workflow documents; one past the bound is
// unscanned github-actions source, not a clean file.
func TestWorkflowScanner_FileBound(t *testing.T) {
	workflows := make(map[string]string, ghworkflow.MaxFiles+1)
	for i := 0; i <= ghworkflow.MaxFiles; i++ {
		workflows[fmt.Sprintf("w%03d.yml", i)] = "jobs:\n  j:\n    runs-on: ubuntu-latest\n    steps:\n      - run: rm -f x || true\n"
	}
	rep := scanWorkflows(t, workflows)
	c := rep.Coverage
	if c.LanguagesRead[workflowLanguageName] != ghworkflow.MaxFiles || c.UnscannedLanguages[workflowLanguageName] != 1 ||
		rep.Breakdown["HISS-07"] != ghworkflow.MaxFiles {
		t.Errorf("want %d workflows read and one unscanned, got %+v / %v", ghworkflow.MaxFiles, c, rep.Breakdown)
	}
}

// Boundary: a CRLF workflow reports its findings at the same lines as the LF one.
func TestWorkflowScanner_CRLFReadsLikeLF(t *testing.T) {
	assertWorkflowFindings(t, strings.ReplaceAll(workflowEveryRule, "\n", "\r\n"),
		"HISS-01@10", "HISS-02@14", "HISS-02@15", "HISS-07@17", "HISS-08@20", "HISS-08@21")
}

// Positive and negative: an unscanned block's coverage label is its shell, unresolved for an
// undecided one and custom for a command that cannot key the coverage record.
func TestRunBlockLabel(t *testing.T) {
	for shell, want := range map[string]string{
		"pwsh": "pwsh", "": unresolvedShell, "{0}": customShell, "node": "node",
		strings.Repeat("x", maxCoverageExtensionBytes+1): customShell, strings.Repeat("x", maxCoverageExtensionBytes): strings.Repeat("x", maxCoverageExtensionBytes),
	} {
		if got := runBlockLabel(shell); got != want {
			t.Errorf("runBlockLabel(%q) = %q, want %q", shell, got, want)
		}
	}
}

// Positive and negative: FixturePath stages a workflow fixture where GitHub reads workflows and
// every other fixture at the root.
func TestFixturePath(t *testing.T) {
	if got := FixturePath(workflowLanguageName, "x.yml"); got != ".github/workflows/x.yml" {
		t.Errorf("workflow fixture staged at %q", got)
	}
	if got := FixturePath("shell", "x.sh"); got != "x.sh" {
		t.Errorf("shell fixture staged at %q", got)
	}
}

// Negative: the coverage record refuses a negative or unlisted run-block count.
func TestScanCoverageValidate_RunBlocks(t *testing.T) {
	ok := ScanCoverage{UnscannedRunBlocks: map[string]int{"pwsh": 3}}
	if err := ok.Validate(); err != nil {
		t.Errorf("a consistent run-block count was refused: %v", err)
	}
	for name, bad := range map[string]ScanCoverage{
		"negative": {UnscannedRunBlocks: map[string]int{"pwsh": -1}},
		"empty":    {UnscannedRunBlocks: map[string]int{"": 1}},
	} {
		if err := bad.Validate(); err == nil {
			t.Errorf("%s run-block count accepted: %+v", name, bad)
		}
	}
}
