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

// TestReviewHoldAggregateRequiresBarrier adds a review-aware need without an inline barrier.
func TestReviewHoldAggregateRequiresBarrier(t *testing.T) {
	workflows, _ := engineWorkflows(t)
	spec, err := ghworkflow.Parse(workflows["portability.yml"])
	if err != nil {
		t.Fatal(err)
	}
	job := spec.Jobs["windows"]
	if got := provenAggregateNeeds(&spec, &job); !slices.Equal(got, []string{"windows-shard"}) {
		t.Fatalf("baseline aggregate coverage = %v, want [windows-shard]", got)
	}
	job.Needs = yaml.Node{
		Kind: yaml.SequenceNode,
		Content: append(job.Needs.Content,
			&yaml.Node{Kind: yaml.ScalarNode, Value: "review-gate"}),
	}
	spec.Jobs["windows"] = job
	if got := provenAggregateNeeds(&spec, &job); len(got) != 0 {
		t.Fatalf("aggregate needing review-gate without inline hold accepted: coverage=%v", got)
	}
}

// TestPortabilityAggregateReviewHold verifies the gate-job aggregate pattern: the aggregate
// needs only windows-shard (review-gate gates transitively), has one alls-green step, and
// provenAggregateNeeds covers windows-shard.
func TestPortabilityAggregateReviewHold(t *testing.T) {
	workflows, _ := engineWorkflows(t)
	spec, err := ghworkflow.Parse(workflows["portability.yml"])
	if err != nil {
		t.Fatal(err)
	}
	job := spec.Jobs["windows"]
	if len(job.Steps) != 1 {
		t.Fatalf("windows aggregate has %d steps, want 1 (alls-green only)", len(job.Steps))
	}
	if got := provenAggregateNeeds(&spec, &job); !slices.Equal(got, []string{"windows-shard"}) || !provenDraftStepAggregate(&spec, "windows") {
		t.Fatalf("gate-job aggregate lost shard/draft proof: %v", got)
	}
}

// TestReviewHoldStepTimeout verifies positive, negative, and boundary timeout-minutes handling.
func TestReviewHoldStepTimeout(t *testing.T) {
	workflows, _ := engineWorkflows(t)
	spec, err := ghworkflow.Parse(workflows["portability.yml"])
	if err != nil {
		t.Fatal(err)
	}
	gate := spec.Jobs["review-gate"]
	if len(gate.Steps) < 2 {
		t.Fatalf("review-gate has %d steps, want at least 2", len(gate.Steps))
	}
	hold := gate.Steps[1]
	wantTimeout := strconv.Itoa(ghworkflow.ReviewHoldStepTimeoutMinutes)

	// Positive: the review-gate's hold step carries the canonical timeout.
	if hold.ID != "review-hold" {
		t.Fatalf("review-gate.Steps[1].ID = %q; want review-hold", hold.ID)
	}
	if hold.TimeoutMinutes.Value != wantTimeout {
		t.Fatalf("hold timeout = %q; want %q", hold.TimeoutMinutes.Value, wantTimeout)
	}
	if !canonicalReviewHoldStep(&hold) {
		t.Fatal("canonicalReviewHoldStep rejected review-gate hold step")
	}

	// Negative: a step without timeout-minutes fails reviewHoldOptionsMatch and canonicalReviewHoldStep.
	noTimeout := hold
	noTimeout.TimeoutMinutes = yaml.Node{}
	if canonicalReviewHoldStep(&noTimeout) {
		t.Fatal("canonicalReviewHoldStep accepted step without timeout-minutes")
	}

	// Negative: a step with another timeout value fails reviewHoldOptionsMatch and canonicalReviewHoldStep.
	for _, badVal := range []string{"1", "3", "5", "10", "60"} {
		badTimeout := hold
		badTimeout.TimeoutMinutes = yaml.Node{Kind: yaml.ScalarNode, Value: badVal}
		if canonicalReviewHoldStep(&badTimeout) {
			t.Fatalf("canonicalReviewHoldStep accepted step with timeout-minutes: %s", badVal)
		}
	}
}
