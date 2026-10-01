// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package forge

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// onNode parses text as the value of a workflow's `on:` key.
func onNode(t *testing.T, text string) *yaml.Node {
	t.Helper()
	var spec workflowSpec
	if err := yaml.Unmarshal([]byte("on: "+text+"\n"), &spec); err != nil {
		t.Fatalf("parse on: %s: %v", text, err)
	}
	return &spec.On
}

// Positive: the one walk reads every shape an `on:` key takes, in file order. A mapping entry
// carries its value node, which eventTrigger returns; a scalar or sequence entry carries none.
// triggerNames and eventTrigger both read that walk.
func TestWorkflowTriggers_Positive_EveryShapeInFileOrder(t *testing.T) {
	cases := map[string][]string{
		"push":                 {"push"},
		"[push, pull_request]": {"push", "pull_request"},
		"{schedule: [{cron: '0 2 * * 1'}], workflow_dispatch: {}}": {"schedule", "workflow_dispatch"},
	}
	for text, want := range cases {
		if got := triggerNames(onNode(t, text)); !slices.Equal(got, want) {
			t.Errorf("triggerNames(%s) = %v, want %v", text, got, want)
		}
	}
	value, declared := eventTrigger(onNode(t, "{pull_request: {paths: [docs/**]}}"), pullRequestEvent)
	if !declared || value == nil || value.Kind != yaml.MappingNode {
		t.Fatalf("mapping trigger: declared=%v value=%v", declared, value)
	}
	if value, declared := eventTrigger(onNode(t, "[push, pull_request]"), pullRequestEvent); !declared || value != nil {
		t.Fatalf("sequence trigger: declared=%v value=%v", declared, value)
	}
}

// Negative: an empty scalar and a missing `on:` key name no trigger, and an undeclared event
// is not found.
func TestWorkflowTriggers_Negative_NothingDeclared(t *testing.T) {
	if got := workflowTriggers(onNode(t, "''")); len(got) != 0 {
		t.Errorf("an empty scalar named %v", got)
	}
	if got := triggerNames(&yaml.Node{}); got != nil {
		t.Errorf("a missing on: key named %v", got)
	}
	if _, declared := eventTrigger(onNode(t, "{push: {}}"), pullRequestEvent); declared {
		t.Error("an undeclared event was found")
	}
}

// Boundary (HISS-02): the walk stops at maxJobsPerFile entries in both collection shapes, so
// the last event inside the bound is found and the first one past it is not.
func TestWorkflowTriggers_Boundary_BoundedAtMaxJobsPerFile(t *testing.T) {
	events := make([]string, maxJobsPerFile+1)
	for i := range events {
		events[i] = fmt.Sprintf("e%d", i)
	}
	last, past := events[maxJobsPerFile-1], events[maxJobsPerFile]
	for _, text := range []string{"[" + strings.Join(events, ", ") + "]", "{" + strings.Join(events, ": {}, ") + ": {}}"} {
		on := onNode(t, text)
		if got := len(workflowTriggers(on)); got != maxJobsPerFile {
			t.Errorf("%d triggers read, want %d", got, maxJobsPerFile)
		}
		if _, declared := eventTrigger(on, last); !declared {
			t.Errorf("%s inside the bound not found", last)
		}
		if _, declared := eventTrigger(on, past); declared {
			t.Errorf("%s past the bound found", past)
		}
	}
}
