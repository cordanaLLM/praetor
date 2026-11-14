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

// reviewEventConcurrencyFault requires a supported expression coalescing reviews by PR.
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
	comments := slices.Contains(events, "pull_request_review_comment")
	if group == nil || group.Kind != yaml.ScalarNode || !reviewGroupUsesPR(group.Value, comments) {
		return fmt.Errorf("%s: review-triggered concurrency group must be decided by github.event.pull_request.number", path)
	}
	return nil
}

// reviewGroupUsesPR allows only expressions whose review branch is known to be stable.
// Unknown syntax fails closed; literal mentions and extra per-run expressions cannot pass.
func reviewGroupUsesPR(group string, comments bool) bool {
	usesPR := false
	for i := 0; i < 16; i++ {
		_, expression, found := strings.Cut(group, "${{")
		if !found {
			return usesPR
		}
		expression, rest, closed := strings.Cut(expression, "}}")
		if !closed {
			return false
		}
		switch strings.Join(strings.Fields(expression), " ") {
		case "github.workflow":
		case "github.event_name == 'pull_request_review' && format('review-{0}', github.event.pull_request.number) || (github.event.number || github.run_id)":
			if comments {
				return false
			}
			usesPR = true
		case "github.event.pull_request.number",
			"github.event.pull_request.number || github.event.number || github.run_id",
			"(" + reviewHoldReleaseReviewEvent + ") && format('review-hold-release-pr-{0}', github.event.pull_request.number) || 'review-hold-release-scan'":
			usesPR = true
		default:
			return false
		}
		group = rest
	}
	return false
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
		{"run-first.yml", "pull_request_review", "${{ github.run_id || github.event.pull_request.number }}", true},
		{"run-suffix.yml", "pull_request_review", "${{ github.event.pull_request.number }}-${{ github.run_id }}", true},
		{"attempt-suffix.yml", "pull_request_review", "${{ github.event.pull_request.number }}-${{ github.run_attempt }}", true},
		{"literal.yml", "pull_request_review", "github.event.pull_request.number", true},
		{"quoted-lookup.yml", "pull_request_review", "${{ 'github.event.pull_request.number' }}", true},
		{"unused-lookup.yml", "pull_request_review", "${{ true || github.event.pull_request.number }}", true},
		{"unused-branch.yml", "pull_request_review", "${{ false && github.event.pull_request.number || github.run_id }}", true},
		{"literal-suffix.yml", "pull_request_review", "${{ github.run_id }}-github.event.pull_request.number", true},
		{"review-namespace.yml", "pull_request_review", "${{ github.workflow }}-${{ github.event_name == 'pull_request_review' && format('review-{0}', github.event.pull_request.number) || (github.event.number || github.run_id) }}", false},
		{"comment-wrong-branch.yml", "pull_request_review_comment", "${{ github.event_name == 'pull_request_review' && format('review-{0}', github.event.pull_request.number) || (github.event.number || github.run_id) }}", true},
		{"unclosed.yml", "pull_request_review", "${{ github.event.pull_request.number", true},
		{"too-many.yml", "pull_request_review", strings.Repeat("${{ github.workflow }}-", 16) + "${{ github.event.pull_request.number }}", true},
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
			data, err := readReviewWorkflow(dir, entry.Name())
			if err != nil {
				t.Fatal(err)
			}
			if err := reviewEventConcurrencyFault(path, data); err != nil {
				t.Error(err)
			}
		})
	}
}

// readReviewWorkflow is the repository guard's workflow reader.
func readReviewWorkflow(dir, name string) ([]byte, error) {
	return util.ReadConfinedLimited(dir, name, 64<<10)
}

// TestReviewEventWorkflowConfinement plants readable sibling and symlink escapes.
func TestReviewEventWorkflowConfinement(t *testing.T) {
	base := t.TempDir()
	dir := filepath.Join(base, "workflows")
	sibling := filepath.Join(base, "workflows-other")
	for _, path := range []string{dir, sibling} {
		// nosemgrep: go.lang.correctness.permissions.file_permission.incorrect-default-permission -- a directory the test writes files into needs the owner execute bit; 0o700 is least privilege
		if err := os.Mkdir(path, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(path, "fixture.yml"), []byte("on: pull_request\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := readReviewWorkflow(dir, "fixture.yml"); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"../workflows-other/fixture.yml", filepath.Join(sibling, "fixture.yml")} {
		if _, err := readReviewWorkflow(dir, name); err == nil {
			t.Errorf("escape accepted: %s", name)
		}
	}
	if err := os.Symlink(filepath.Join(sibling, "fixture.yml"), filepath.Join(dir, "escape.yml")); err != nil {
		t.Skipf("symlink fixture unavailable: %v", err)
	}
	if _, err := readReviewWorkflow(dir, "escape.yml"); err == nil {
		t.Fatal("symlink escape accepted")
	}
}

// TestReviewEventPortabilityNoCancelOfPRRun keeps reviews outside the matrix's group.
func TestReviewEventPortabilityNoCancelOfPRRun(t *testing.T) {
	spec := loadPortabilityWorkflow(t)
	want := map[string]string{
		"group":              "${{ github.workflow }}-${{ github.event_name == 'pull_request_review' && format('review-{0}', github.event.pull_request.number) || (github.event.number || github.run_id) }}",
		"cancel-in-progress": "${{ github.event_name == 'pull_request' || github.event_name == 'pull_request_review' }}",
	}
	for key, value := range want {
		node := util.YAMLMappingValue(&spec.Concurrency, key)
		if node == nil || node.Value != value {
			t.Errorf("portability %s=%v; want %s", key, node, value)
		}
	}
}
