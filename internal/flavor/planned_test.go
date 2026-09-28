// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package flavor_test

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/cordanaLLM/praetor/internal/flavor"
)

// plannedPaths returns the paths PlannedWorkflows lists for repo.
func plannedPaths(t *testing.T, repo string) []string {
	t.Helper()
	planned, err := flavor.PlannedWorkflows(t.Context(), repo)
	if err != nil {
		t.Fatalf("PlannedWorkflows: %v", err)
	}
	paths := make([]string, 0, len(planned))
	for _, p := range planned {
		paths = append(paths, p.Path)
	}
	return paths
}

// TestPlannedWorkflows_Positive: a go-service repository without workflows gets both of its
// flavor's workflows, with the body apply then writes byte for byte.
func TestPlannedWorkflows_Positive(t *testing.T) {
	repo := repoWithFiles(t, map[string]string{"go.mod": "module example.com/svc\n", "cmd/svc/main.go": "package main\n"})
	planned, err := flavor.PlannedWorkflows(t.Context(), repo)
	if err != nil {
		t.Fatal(err)
	}
	if len(planned) != 2 || planned[0].Path != ".github/workflows/ci.yml" || planned[1].Path != ".github/workflows/security.yml" {
		t.Fatalf("planned workflows = %+v", planned)
	}
	if _, err := flavor.ApplyFlavor(t.Context(), repo, "go-service", false); err != nil {
		t.Fatal(err)
	}
	written, err := os.ReadFile(filepath.Join(repo, ".github", "workflows", "ci.yml"))
	if err != nil || string(written) != planned[0].Content {
		t.Fatalf("apply wrote a different ci.yml than planned (%v)", err)
	}
}

// TestPlannedWorkflows_Negative: no flavor, a nil context, and a workflow the repository owns
// (present, different from the rendering) are never listed.
func TestPlannedWorkflows_Negative(t *testing.T) {
	if got := plannedPaths(t, repoWithFiles(t, map[string]string{"README.md": "# none\n"})); len(got) != 0 {
		t.Errorf("repository no flavor matches lists %q", got)
	}
	var nilContext context.Context
	if _, err := flavor.PlannedWorkflows(nilContext, t.TempDir()); err == nil {
		t.Error("nil context must be an error")
	}
	repo := repoWithFiles(t, map[string]string{
		"go.mod": "module example.com/lib\n", "internal/lib.go": "package lib\n",
		".github/workflows/ci.yml": "name: Own CI\non: push\njobs:\n  own:\n    runs-on: ubuntu-latest\n    steps:\n      - run: make ci\n",
	})
	if got := plannedPaths(t, repo); len(got) != 0 {
		t.Errorf("repository-owned ci.yml listed as the flavor's: %q", got)
	}
}

// TestPlannedWorkflows_Boundary: a workflow an earlier apply wrote stays listed, and one whose
// requirement the repository does not meet (typescript-node without an npm lockfile) is not.
func TestPlannedWorkflows_Boundary(t *testing.T) {
	repo := repoWithFiles(t, map[string]string{"go.mod": "module example.com/lib\n", "internal/lib.go": "package lib\n"})
	if _, err := flavor.ApplyFlavor(t.Context(), repo, "go-library", false); err != nil {
		t.Fatal(err)
	}
	if got := plannedPaths(t, repo); !slices.Equal(got, []string{".github/workflows/ci.yml"}) {
		t.Errorf("earlier apply's ci.yml not listed: %q", got)
	}
	node := repoWithFiles(t, map[string]string{"package.json": `{"name":"x","scripts":{"test":"vitest"}}`})
	if got := plannedPaths(t, node); len(got) != 0 {
		t.Errorf("unmet node workflow listed: %q", got)
	}
}
