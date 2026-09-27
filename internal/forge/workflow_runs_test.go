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
// script, jobs in ID order, and `uses:` steps not at all.
func TestWorkflowRuns_Positive(t *testing.T) {
	const doc = `name: CI
on: pull_request
jobs:
  test:
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
		{Label: "make build", Step: true, Script: "make build\nmake package"},
		{Label: "Install toolchain", Step: true, Script: "rustup toolchain install stable\nrustup default stable"},
		{Label: "cargo clippy -- -D warnings", Script: "cargo clippy -- -D warnings"},
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
	if runs, err := WorkflowRuns(stepsDoc(maxStepsPerJob)); err != nil || len(runs) != maxStepsPerJob {
		t.Fatalf("job at the bound: %d runs, %v", len(runs), err)
	}
	if _, err := WorkflowRuns(stepsDoc(maxStepsPerJob + 1)); err == nil {
		t.Fatal("job past the step bound must be refused")
	}
}
