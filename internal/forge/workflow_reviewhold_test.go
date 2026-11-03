// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package forge

import (
	"slices"
	"testing"

	"github.com/cordanaLLM/praetor/internal/ghworkflow"
)

// TestReviewHoldAggregateRequiresBarrier plants a review-aware need without its aggregate barrier.
func TestReviewHoldAggregateRequiresBarrier(t *testing.T) {
	workflows, _ := engineWorkflows(t)
	spec, err := ghworkflow.Parse(workflows["portability.yml"])
	if err != nil {
		t.Fatal(err)
	}
	job := spec.Jobs["windows"]
	if job.Steps[0].ID == "review-hold" {
		job.Steps = job.Steps[1:]
	}
	spec.Jobs["windows"] = job
	if got := provenAggregateNeeds(&spec, &job); len(got) != 0 || provenDraftStepAggregate(&spec, "windows") {
		t.Fatalf("aggregate without review hold accepted: coverage=%v", got)
	}
}

// TestPortabilityAggregateReviewHold requires a shared barrier before the shard result step.
func TestPortabilityAggregateReviewHold(t *testing.T) {
	workflows, _ := engineWorkflows(t)
	spec, err := ghworkflow.Parse(workflows["portability.yml"])
	if err != nil {
		t.Fatal(err)
	}
	job := spec.Jobs["windows"]
	if len(job.Steps) != 2 || job.Steps[0].ID != "review-hold" {
		t.Fatal("windows aggregate lacks its leading review hold")
	}
	if got := provenAggregateNeeds(&spec, &job); !slices.Equal(got, []string{"windows-shard"}) || !provenDraftStepAggregate(&spec, "windows") {
		t.Fatalf("review-aware aggregate lost shard/draft proof: %v", got)
	}
	for _, mutation := range []string{"missing", "script", "condition", "shell", "environment", "advisory", "reordered", "extra step"} {
		t.Run(mutation, func(t *testing.T) {
			changed := mutateAggregateReviewHold(job, mutation)
			spec.Jobs["windows"] = changed
			if got := provenAggregateNeeds(&spec, &changed); len(got) != 0 || provenDraftStepAggregate(&spec, "windows") {
				t.Fatalf("%s hold accepted: coverage=%v", mutation, got)
			}
		})
	}
}

// mutateAggregateReviewHold plants a single malformed or absent barrier in an aggregate copy.
func mutateAggregateReviewHold(job workflowJob, mutation string) workflowJob {
	changed := job
	changed.Steps = slices.Clone(job.Steps)
	switch mutation {
	case "missing":
		changed.Steps = changed.Steps[1:]
	case "script":
		changed.Steps[0].Run = "echo clear=true >> \"$GITHUB_OUTPUT\"\n"
	case "condition":
		changed.Steps[0].If = "false"
	case "shell":
		changed.Steps[0].Shell = "sh"
	case "environment":
		changed.Steps[0].Env = job.Steps[1].Env
	case "advisory":
		changed.Steps[0].ContinueOnError = "true"
	case "reordered":
		changed.Steps[0], changed.Steps[1] = changed.Steps[1], changed.Steps[0]
	case "extra step":
		changed.Steps = append(changed.Steps, job.Steps[0])
	}
	return changed
}
