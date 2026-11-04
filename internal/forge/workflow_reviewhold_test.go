// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package forge

import (
	"slices"
	"strconv"
	"testing"

	"go.yaml.in/yaml/v3"

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
	for _, mutation := range []string{"missing", "script", "condition", "shell", "environment", "advisory", "reordered", "extra step", "no timeout", "wrong timeout", "timeout expression"} {
		t.Run(mutation, func(t *testing.T) {
			changed := mutateAggregateReviewHold(job, mutation)
			spec.Jobs["windows"] = changed
			if got := provenAggregateNeeds(&spec, &changed); len(got) != 0 || provenDraftStepAggregate(&spec, "windows") {
				t.Fatalf("%s hold accepted: coverage=%v", mutation, got)
			}
		})
	}
}

// mutateAggregateReviewHoldTimeout applies timeout-related mutations to the leading step.
func mutateAggregateReviewHoldTimeout(step *workflowStep, mutation string) bool {
	switch mutation {
	case "no timeout":
		step.TimeoutMinutes = yaml.Node{}
		return true
	case "wrong timeout":
		step.TimeoutMinutes = yaml.Node{Kind: yaml.ScalarNode, Value: "10"}
		return true
	case "timeout expression":
		step.TimeoutMinutes = yaml.Node{Kind: yaml.ScalarNode, Value: "${{ inputs.timeout }}"}
		return true
	default:
		return false
	}
}

// mutateAggregateReviewHold plants a single malformed or absent barrier in an aggregate copy.
func mutateAggregateReviewHold(job workflowJob, mutation string) workflowJob {
	changed := job
	changed.Steps = slices.Clone(job.Steps)
	if mutateAggregateReviewHoldTimeout(&changed.Steps[0], mutation) {
		return changed
	}
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

// TestReviewHoldStepTimeout verifies positive, negative, and boundary timeout-minutes handling.
func TestReviewHoldStepTimeout(t *testing.T) {
	workflows, _ := engineWorkflows(t)
	spec, err := ghworkflow.Parse(workflows["portability.yml"])
	if err != nil {
		t.Fatal(err)
	}
	anchor := spec.Jobs["harness"].Steps[1]
	shardAlias := spec.Jobs["windows-shard"].Steps[1]
	aggregateAlias := spec.Jobs["windows"].Steps[0]
	wantTimeout := strconv.Itoa(ghworkflow.ReviewHoldStepTimeoutMinutes)

	// Positive: rendered step and the workflow anchor carry the canonical timeout.
	if anchor.TimeoutMinutes.Value != wantTimeout {
		t.Fatalf("anchor timeout = %q; want %q", anchor.TimeoutMinutes.Value, wantTimeout)
	}
	if !canonicalReviewHoldStep(&anchor) {
		t.Fatal("canonicalReviewHoldStep rejected canonical anchor step")
	}

	// Boundary: the alias steps (windows-shard and aggregate) carry the timeout.
	if shardAlias.TimeoutMinutes.Value != wantTimeout {
		t.Fatalf("shard alias timeout = %q; want %q", shardAlias.TimeoutMinutes.Value, wantTimeout)
	}
	if aggregateAlias.TimeoutMinutes.Value != wantTimeout {
		t.Fatalf("aggregate alias timeout = %q; want %q", aggregateAlias.TimeoutMinutes.Value, wantTimeout)
	}
	if !canonicalReviewHoldStep(&aggregateAlias) {
		t.Fatal("canonicalReviewHoldStep rejected canonical aggregate alias step")
	}

	// Negative: a step without timeout-minutes fails reviewHoldOptionsMatch and canonicalReviewHoldStep.
	noTimeout := anchor
	noTimeout.TimeoutMinutes = yaml.Node{}
	if canonicalReviewHoldStep(&noTimeout) {
		t.Fatal("canonicalReviewHoldStep accepted step without timeout-minutes")
	}

	// Negative: a step with another timeout value fails reviewHoldOptionsMatch and canonicalReviewHoldStep.
	for _, badVal := range []string{"1", "3", "5", "10", "60"} {
		badTimeout := anchor
		badTimeout.TimeoutMinutes = yaml.Node{Kind: yaml.ScalarNode, Value: badVal}
		if canonicalReviewHoldStep(&badTimeout) {
			t.Fatalf("canonicalReviewHoldStep accepted step with timeout-minutes: %s", badVal)
		}
	}
}
