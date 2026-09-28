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

// pullRequestJob is a workflow whose one job is a required check named job.
func pullRequestJob(job string) []byte {
	return []byte("on: pull_request\njobs:\n  " + job + ": {}\n")
}

// A planned workflow adds its checks, one planned over a file on disk replaces that file's, and
// a planned removal drops them; the result is in file name order.
func TestRequiredStatusContextsPlanned_Positive_AppliesWritesAndRemovals(t *testing.T) {
	root := t.TempDir()
	writeWorkflowFixture(t, root, "b.yml", string(pullRequestJob("disk_b")))
	writeWorkflowFixture(t, root, "c.yml", string(pullRequestJob("disk_c")))
	got, err := RequiredStatusContextsPlanned(t.Context(), root, map[string][]byte{
		".github/workflows/a.yaml": pullRequestJob("planned_a"),
		".github/workflows/b.yml":  pullRequestJob("planned_b"),
		".github/workflows/c.yml":  nil,
	})
	if err != nil {
		t.Fatalf("planned contexts: %v", err)
	}
	if want := []string{"planned_a", "planned_b"}; !slices.Equal(got, want) {
		t.Fatalf("contexts = %v, want %v", got, want)
	}
}

// A planned path that is not a workflow document directly under .github/workflows is not read,
// and a planned workflow that does not parse fails the discovery as one on disk does.
func TestRequiredStatusContextsPlanned_Negative_OnlyWorkflowsAreReadAndMustParse(t *testing.T) {
	root := t.TempDir()
	writeWorkflowFixture(t, root, "ci.yml", string(pullRequestJob("verify")))
	got, err := RequiredStatusContextsPlanned(t.Context(), root, map[string][]byte{
		".github/ci.yml":                 pullRequestJob("outside"),
		".github/workflows/sub/deep.yml": pullRequestJob("nested"),
		".github/workflows/notes.md":     pullRequestJob("not_yaml"),
		".standards.yaml":                []byte("version: 1\n"),
	})
	if err != nil || !slices.Equal(got, []string{"verify"}) {
		t.Fatalf("contexts = %v, %v; want only the workflow on disk", got, err)
	}
	_, err = RequiredStatusContextsPlanned(t.Context(), root, map[string][]byte{".github/workflows/bad.yml": []byte("jobs: [")})
	if err == nil || !strings.Contains(err.Error(), "bad.yml") {
		t.Fatalf("a malformed planned workflow must fail naming it, got %v", err)
	}
}

// Nil and empty plans read the disk alone, exactly as RequiredStatusContexts does; a plan that
// grows the inventory past its bound is refused rather than truncated.
func TestRequiredStatusContextsPlanned_Boundary_EmptyPlansAndTheInventoryBound(t *testing.T) {
	root := t.TempDir()
	writeWorkflowFixture(t, root, "ci.yml", string(pullRequestJob("verify")))
	disk, err := RequiredStatusContexts(t.Context(), root)
	if err != nil {
		t.Fatal(err)
	}
	for name, planned := range map[string]map[string][]byte{"nil": nil, "empty": {}} {
		got, err := RequiredStatusContextsPlanned(t.Context(), root, planned)
		if err != nil || !slices.Equal(got, disk) {
			t.Fatalf("%s plan: %v, %v; want %v", name, got, err, disk)
		}
	}
	overflow := make(map[string][]byte, maxWorkflowFiles)
	for i := 0; i < maxWorkflowFiles; i++ {
		overflow[fmt.Sprintf(".github/workflows/p%02d.yml", i)] = pullRequestJob(fmt.Sprintf("p%02d", i))
	}
	if _, err := RequiredStatusContextsPlanned(t.Context(), root, overflow); err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("an inventory past %d workflows must be refused, got %v", maxWorkflowFiles, err)
	}
}
