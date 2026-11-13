// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package ghworkflow

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/util"
	"go.yaml.in/yaml/v3"
)

// reviewEventConcurrencyFault refuses review groups without the nested PR number (HISS-18).
func reviewEventConcurrencyFault(path string, data []byte) error {
	spec, err := Parse(data)
	if err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	events := StringList(&spec.On)
	if spec.On.Kind == yaml.MappingNode {
		for i := 0; i < len(spec.On.Content); i += 2 {
			events = append(events, spec.On.Content[i].Value)
		}
	}
	if !slices.Contains(events, "pull_request_review") && !slices.Contains(events, "pull_request_review_comment") {
		return nil
	}
	group := &spec.Concurrency
	if group.Kind == yaml.MappingNode {
		group = util.YAMLMappingValue(group, "group")
	}
	if group == nil || group.Kind != yaml.ScalarNode || !strings.Contains(group.Value, "github.event.pull_request.number") {
		return fmt.Errorf("%s: review-triggered concurrency group must use github.event.pull_request.number", path)
	}
	return nil
}

// TestReviewEventConcurrencyFixtures plants the old group and covers each trigger shape.
func TestReviewEventConcurrencyFixtures(t *testing.T) {
	cases := []struct {
		name, on, group string
		refused         bool
	}{
		{"old-portability.yml", "pull_request_review: {types: [submitted]}", "${{ github.workflow }}-${{ github.event.number || github.run_id }}", true},
		{"fixed-portability.yml", "pull_request_review: {types: [submitted]}", "${{ github.workflow }}-${{ github.event.pull_request.number || github.event.number || github.run_id }}", false},
		{"no-review.yml", "pull_request: {}", "${{ github.workflow }}-${{ github.event.number || github.run_id }}", false},
		{"review-comment.yml", "[pull_request_review_comment]", "${{ github.event.number || github.run_id }}", true},
		{"scalar-review.yml", "pull_request_review", "review-hold-release", true},
		{"fixed-comment.yml", "pull_request_review_comment", "review-hold-release-pr-${{ github.event.pull_request.number }}", false},
		{"missing-group.yml", "pull_request_review", "", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			on := "on: " + tc.on + "\n"
			if strings.Contains(tc.on, ":") {
				on = "on:\n  " + tc.on + "\n"
			}
			err := reviewEventConcurrencyFault(tc.name, []byte(on+"concurrency:\n  group: "+tc.group+"\n"))
			if (err != nil) != tc.refused || (err != nil && !strings.Contains(err.Error(), tc.name)) {
				t.Fatalf("fault=%v; want refused=%t naming %s", err, tc.refused, tc.name)
			}
		})
	}
}

// TestRepositoryReviewEventConcurrency loads every committed workflow, including new ones.
func TestRepositoryReviewEventConcurrency(t *testing.T) {
	dir := filepath.Join("../..", Dir)
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) > MaxFiles {
		t.Fatalf("%s exceeds %d entries", dir, MaxFiles)
	}
	for _, entry := range entries {
		path := Dir + "/" + entry.Name()
		if entry.IsDir() || !IsWorkflowPath(path) {
			continue
		}
		t.Run(entry.Name(), func(t *testing.T) {
			data, err := os.ReadFile(filepath.Join(dir, entry.Name()))
			if err != nil {
				t.Fatal(err)
			}
			if err := reviewEventConcurrencyFault(path, data); err != nil {
				t.Error(err)
			}
		})
	}
}
