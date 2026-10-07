// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package forge

import (
	"fmt"
	"slices"
	"strings"
	"testing"
)

// TestWorkflowRuns_Positive: one-line commands are listed verbatim, a multi-line script by its
// step name, an unnamed one by its first line, both marked as steps and carrying the whole
// script, jobs in ID order, and `uses:` steps not at all. Each run carries its step name, its
// job's ID and name, and its position among the job's steps, the `uses:` step counted.
func TestWorkflowRuns_Positive(t *testing.T) {
	const doc = `name: CI
on: pull_request
jobs:
  test:
    name: Test
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v7
      - name: Install toolchain
        run: |
          rustup toolchain install stable
          rustup default stable
      - name: Lint
        run: cargo clippy -- -D warnings
  build:
    runs-on: ubuntu-latest
    steps:
      - run: |
          make build
          make package
`
	runs, err := WorkflowRuns([]byte(doc))
	if err != nil {
		t.Fatal(err)
	}
	want := []WorkflowRun{
		{Label: "make build", Step: true, Script: "make build\nmake package", Job: "build"},
		{Label: "Install toolchain", Step: true, Script: "rustup toolchain install stable\nrustup default stable",
			Name: "Install toolchain", Job: "test", JobName: "Test", Index: 1},
		{Label: "cargo clippy -- -D warnings", Script: "cargo clippy -- -D warnings", Name: "Lint", Job: "test", JobName: "Test", Index: 2},
	}
	if !slices.Equal(runs, want) {
		t.Fatalf("WorkflowRuns = %+v, want %+v", runs, want)
	}
}

// TestWorkflowRuns_Negative: a document that does not parse is an error, never an empty list.
func TestWorkflowRuns_Negative(t *testing.T) {
	if _, err := WorkflowRuns([]byte("jobs: [\n")); err == nil {
		t.Fatal("malformed workflow must be an error")
	}
	if _, err := WorkflowRuns([]byte("jobs:\n  test:\n    steps: {}\n")); err == nil {
		t.Fatal("steps of the wrong type must be an error")
	}
	// A blank step or job name is no name: a caller finding a step by name never matches it.
	runs, err := WorkflowRuns([]byte("jobs:\n  test:\n    name: '  '\n    steps:\n      - name: ' '\n        run: make\n"))
	if err != nil || len(runs) != 1 || runs[0].Name != "" || runs[0].JobName != "" {
		t.Fatalf("blank names: %+v, %v", runs, err)
	}
}

// TestWorkflowRuns_Boundary: a workflow of actions only runs no command; a job at the step bound
// is read whole, one past it is refused rather than read in part.
func TestWorkflowRuns_Boundary(t *testing.T) {
	runs, err := WorkflowRuns([]byte("jobs:\n  test:\n    steps:\n      - uses: actions/checkout@v7\n"))
	if err != nil || len(runs) != 0 {
		t.Fatalf("actions-only workflow: %+v, %v", runs, err)
	}
	stepsDoc := func(n int) []byte {
		var b strings.Builder
		b.WriteString("jobs:\n  test:\n    steps:\n")
		for i := 0; i < n; i++ {
			fmt.Fprintf(&b, "      - run: echo %d\n", i)
		}
		return []byte(b.String())
	}
	runs, err = WorkflowRuns(stepsDoc(maxStepsPerJob))
	if err != nil || len(runs) != maxStepsPerJob {
		t.Fatalf("job at the bound: %d runs, %v", len(runs), err)
	}
	if last := runs[len(runs)-1]; last.Index != maxStepsPerJob-1 || last.Job != "test" {
		t.Fatalf("last step at the bound sits at %s[%d], want test[%d]", last.Job, last.Index, maxStepsPerJob-1)
	}
	if _, err := WorkflowRuns(stepsDoc(maxStepsPerJob + 1)); err == nil {
		t.Fatal("job past the step bound must be refused")
	}
}

// WorkflowActions lists what a workflow's steps use. Positive: every `uses:` value, job by job in
// job ID order and step by step in file order, run steps skipped. Negative: a document that does
// not parse is an error. Boundary: a job at the step bound is read whole, one past it refused.
func TestWorkflowActions_3D(t *testing.T) {
	actions, err := WorkflowActions([]byte("jobs:\n  b:\n    steps:\n      - uses: x/second@v1\n  a:\n    steps:\n" +
		"      - uses: ' actions/checkout@v7 '\n      - run: make\n      - uses: fsfe/reuse-action@v6\n"))
	if want := []string{"actions/checkout@v7", "fsfe/reuse-action@v6", "x/second@v1"}; err != nil || !slices.Equal(actions, want) {
		t.Fatalf("WorkflowActions = %v, %v; want %v", actions, err, want)
	}
	if _, err := WorkflowActions([]byte("jobs: [\n")); err == nil {
		t.Fatal("malformed workflow must be an error")
	}
	usesDoc := func(n int) []byte {
		var b strings.Builder
		b.WriteString("jobs:\n  test:\n    steps:\n")
		for i := 0; i < n; i++ {
			fmt.Fprintf(&b, "      - uses: a/b@v%d\n", i)
		}
		return []byte(b.String())
	}
	if actions, err := WorkflowActions(usesDoc(maxStepsPerJob)); err != nil || len(actions) != maxStepsPerJob {
		t.Fatalf("job at the bound: %d actions, %v", len(actions), err)
	}
	if _, err := WorkflowActions(usesDoc(maxStepsPerJob + 1)); err == nil {
		t.Fatal("a job past the step bound was read in part")
	}
}
