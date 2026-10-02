// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package ghworkflow

import (
	"fmt"
	"strings"
	"testing"
)

const runPositions = `on: push
jobs:
  build:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v7
      - run: |

          echo one
          echo two
      - name: Folded
        run: >
          echo a
          echo b
      - run: echo plain
      - run: "   "
`

// Positive: a literal block records the line of its indicator, so script line n sits on document
// line RunLine+n, a blank first line included; a folded and a plain value record the line they
// start on and are not literal.
func TestParse_RecordsRunPositions(t *testing.T) {
	spec, err := Parse([]byte(runPositions))
	if err != nil {
		t.Fatal(err)
	}
	steps := spec.Jobs["build"].Steps
	want := []struct {
		line    int
		literal bool
	}{{0, false}, {7, true}, {12, false}, {15, false}, {16, false}}
	if len(steps) != len(want) {
		t.Fatalf("decoded %d steps, want %d", len(steps), len(want))
	}
	for i, w := range want {
		if steps[i].RunLine != w.line || steps[i].RunLiteral != w.literal {
			t.Errorf("step %d: line %d literal %t, want line %d literal %t", i, steps[i].RunLine, steps[i].RunLiteral, w.line, w.literal)
		}
	}
	if first, _, _ := strings.Cut(steps[1].Run, "echo one"); strings.Count(first, "\n") != 1 {
		t.Errorf("the literal block must keep its blank first line, got %q", steps[1].Run)
	}
}

// jobsDocument returns a workflow with n jobs of steps steps each.
func jobsDocument(n, steps int) string {
	var sb strings.Builder
	sb.WriteString("jobs:\n")
	for i := 0; i < n; i++ {
		fmt.Fprintf(&sb, "  job%03d:\n    runs-on: ubuntu-latest\n    steps:\n", i)
		for j := 0; j < steps; j++ {
			sb.WriteString("      - run: echo step\n")
		}
	}
	return sb.String()
}

// Negative and boundary: a malformed document and one past the job bound are refused, one at
// the bound is read.
func TestParse_RefusesMalformedAndOversizedDocuments(t *testing.T) {
	if _, err := Parse([]byte("jobs: [unclosed\n")); err == nil || !strings.Contains(err.Error(), "parse workflow") {
		t.Errorf("a malformed document must be refused as unparsable, got %v", err)
	}
	if _, err := Parse([]byte(jobsDocument(MaxJobsPerFile+1, 1))); err == nil {
		t.Error("a document past the job bound must be refused")
	}
	if spec, err := Parse([]byte(jobsDocument(MaxJobsPerFile, 1))); err != nil || len(spec.Jobs) != MaxJobsPerFile {
		t.Errorf("a document at the job bound must be read, got %d jobs, %v", len(spec.Jobs), err)
	}
}

// Positive: RunSteps lists the steps that run a script, jobs in ID order and steps in file
// order, each with its index among all steps of its job. Negative: a uses: step and a blank
// run: are left out.
func TestRunSteps_OrderAndIndex(t *testing.T) {
	spec, err := Parse([]byte("jobs:\n  zeta:\n    steps:\n      - run: echo z\n  alpha:\n    steps:\n" +
		"      - uses: actions/checkout@v7\n      - run: ''\n      - run: echo a\n"))
	if err != nil {
		t.Fatal(err)
	}
	runs, err := RunSteps(&spec)
	if err != nil {
		t.Fatal(err)
	}
	got := make([]string, 0, len(runs))
	for _, run := range runs {
		got = append(got, fmt.Sprintf("%s/%d=%s", run.JobID, run.Index, strings.TrimSpace(run.Step.Run)))
	}
	if strings.Join(got, " ") != "alpha/2=echo a zeta/0=echo z" {
		t.Errorf("run steps %v", got)
	}
}

// Boundary: a job at the step bound is read; one past it is refused rather than read in part.
func TestRunSteps_StepBound(t *testing.T) {
	for steps, refused := range map[int]bool{MaxStepsPerJob: false, MaxStepsPerJob + 1: true} {
		spec, err := Parse([]byte(jobsDocument(1, steps)))
		if err != nil {
			t.Fatal(err)
		}
		runs, err := RunSteps(&spec)
		if (err != nil) != refused || (!refused && len(runs) != steps) {
			t.Errorf("%d steps: %d runs, error %v, want refused %t", steps, len(runs), err, refused)
		}
	}
}

// Positive and negative: only a YAML document directly in .github/workflows is a workflow.
func TestIsWorkflowPath(t *testing.T) {
	for rel, want := range map[string]bool{
		".github/workflows/ci.yml": true, ".github/workflows/release.yaml": true,
		".github/workflows/nested/ci.yml": false, "sub/.github/workflows/ci.yml": false,
		".github/workflows/README.md": false, ".github/ci.yml": false, ".github/workflows/": false,
	} {
		if got := IsWorkflowPath(rel); got != want {
			t.Errorf("IsWorkflowPath(%q) = %t, want %t", rel, got, want)
		}
	}
}
