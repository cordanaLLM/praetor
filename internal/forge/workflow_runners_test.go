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

// TestWorkflowRunnerLabels_Positive: a runs-on string, every string of a runs-on list and
// every label of a runs-on mapping are listed, jobs in ID order, each label once, a trailing
// comment and quotes not part of a label.
func TestWorkflowRunnerLabels_Positive(t *testing.T) {
	const doc = `name: CI
on: pull_request
jobs:
  test:
    runs-on: ubuntu-26.04  # hosted
    steps:
      - run: make test
  build:
    runs-on: [self-hosted, 'linux', ubuntu-26.04]
    steps:
      - run: make build
  deploy:
    runs-on:
      group: deployers
      labels:
        - gpu
        - linux
    steps:
      - run: make deploy
`
	labels, err := WorkflowRunnerLabels([]byte(doc))
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"self-hosted", "linux", "ubuntu-26.04", "gpu"}
	if !slices.Equal(labels, want) {
		t.Fatalf("labels = %q, want %q", labels, want)
	}
}

// TestWorkflowRunnerLabels_Negative: a document that is not YAML is an error, and neither an
// expression, a runner group's name nor a job without runs-on names a label.
func TestWorkflowRunnerLabels_Negative(t *testing.T) {
	if _, err := WorkflowRunnerLabels([]byte("jobs: [\n")); err == nil {
		t.Fatal("a document that is not YAML was read")
	}
	const doc = `on: push
jobs:
  matrix:
    runs-on: ${{ matrix.os }}
    strategy:
      matrix:
        os: [ubuntu-26.04]
  mixed:
    runs-on: [self-hosted, "${{ inputs.runner }}"]
  group:
    runs-on:
      group: deployers
  reusable:
    uses: ./.github/workflows/build.yml
  nested:
    runs-on:
      labels:
        other: gpu
`
	labels, err := WorkflowRunnerLabels([]byte(doc))
	if err != nil || !slices.Equal(labels, []string{"self-hosted"}) {
		t.Fatalf("labels = %q (%v), want only self-hosted", labels, err)
	}
}

// TestWorkflowRunnerLabels_Boundary: an empty document and one without jobs name no label, a
// label mapping holding one string names it, and a workflow over the job bound is refused
// rather than read in part while one at the bound is read.
func TestWorkflowRunnerLabels_Boundary(t *testing.T) {
	for name, doc := range map[string]string{"empty": "", "no jobs": "name: CI\non: push\n"} {
		if labels, err := WorkflowRunnerLabels([]byte(doc)); err != nil || len(labels) != 0 {
			t.Errorf("%s: labels = %q (%v), want none", name, labels, err)
		}
	}
	single := "jobs:\n  one:\n    runs-on:\n      labels: gpu\n"
	if labels, err := WorkflowRunnerLabels([]byte(single)); err != nil || !slices.Equal(labels, []string{"gpu"}) {
		t.Errorf("single mapping label = %q (%v)", labels, err)
	}
	var jobs strings.Builder
	for i := 0; i < maxJobsPerFile; i++ {
		fmt.Fprintf(&jobs, "  j%02d:\n    runs-on: runner-%02d\n", i, i)
	}
	atBound := "jobs:\n" + jobs.String()
	if labels, err := WorkflowRunnerLabels([]byte(atBound)); err != nil || len(labels) != maxJobsPerFile {
		t.Errorf("%d jobs: %d labels (%v)", maxJobsPerFile, len(labels), err)
	}
	overBound := atBound + "  extra:\n    runs-on: runner-extra\n"
	if _, err := WorkflowRunnerLabels([]byte(overBound)); err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Errorf("%d jobs read: %v", maxJobsPerFile+1, err)
	}
}
