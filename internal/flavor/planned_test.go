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

// plannedPaths returns the paths PlannedWorkflows lists for repo adopted under profile.
func plannedPaths(t *testing.T, repo, profile string) []string {
	t.Helper()
	planned, err := flavor.PlannedWorkflows(t.Context(), repo, profile)
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
	planned, err := flavor.PlannedWorkflows(t.Context(), repo, "framework")
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

// TestPlannedWorkflows_Positive_RendersTheResolvedPackageManager: the planned Node job is the
// one apply writes for the repository's package manager, so adoption names what CI runs there,
// and the job an earlier apply wrote stays the flavor's own.
func TestPlannedWorkflows_Positive_RendersTheResolvedPackageManager(t *testing.T) {
	repo := gitRepoWithFiles(t, map[string]string{"package.json": withTest, "pnpm-lock.yaml": pnpmLock})
	planned, err := flavor.PlannedWorkflows(t.Context(), repo, "app-service")
	if err != nil {
		t.Fatal(err)
	}
	if len(planned) != 1 || planned[0].Path != nodeCIPath {
		t.Fatalf("planned workflows = %+v", planned)
	}
	assertJobRuns(t, planned[0].Content, []string{"pnpm install --frozen-lockfile", "pnpm run test"}, []string{"npm ci", "npm test"})
	if _, err := flavor.ApplyFlavor(t.Context(), repo, "typescript-node", false); err != nil {
		t.Fatal(err)
	}
	written, err := os.ReadFile(filepath.Join(repo, filepath.FromSlash(nodeCIPath)))
	if err != nil || string(written) != planned[0].Content {
		t.Fatalf("apply wrote a different %s than planned (%v)", nodeCIPath, err)
	}
	if got := plannedPaths(t, repo, "app-service"); !slices.Equal(got, []string{nodeCIPath}) {
		t.Errorf("earlier apply's pnpm job not listed: %q", got)
	}
}

// TestPlannedWorkflows_Negative: no flavor, a nil context, and a workflow the repository owns
// (present, different from the rendering) are never listed.
func TestPlannedWorkflows_Negative(t *testing.T) {
	if got := plannedPaths(t, repoWithFiles(t, map[string]string{"README.md": "# none\n"}), "framework"); len(got) != 0 {
		t.Errorf("repository no flavor matches lists %q", got)
	}
	var nilContext context.Context
	if _, err := flavor.PlannedWorkflows(nilContext, t.TempDir(), "framework"); err == nil {
		t.Error("nil context must be an error")
	}
	repo := repoWithFiles(t, map[string]string{
		"go.mod": "module example.com/lib\n", "internal/lib.go": "package lib\n",
		".github/workflows/ci.yml": "name: Own CI\non: push\njobs:\n  own:\n    runs-on: ubuntu-latest\n    steps:\n      - run: make ci\n",
	})
	if got := plannedPaths(t, repo, "framework"); len(got) != 0 {
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
	if got := plannedPaths(t, repo, "framework"); !slices.Equal(got, []string{".github/workflows/ci.yml"}) {
		t.Errorf("earlier apply's ci.yml not listed: %q", got)
	}
	node := repoWithFiles(t, map[string]string{"package.json": `{"name":"x","scripts":{"test":"vitest"}}`})
	if got := plannedPaths(t, node, "app-service"); len(got) != 0 {
		t.Errorf("unmet node workflow listed: %q", got)
	}
}

// TestPlannedWorkflows_Positive_TheProfileDecidesTheFlavor: the flavor planned is the one of the
// profile adoption records, as its flavor step applies it, not the one the markers alone would
// pick. A repository carrying a Go service and an npm package plans go-service's workflows under
// framework and typescript-node's npm job under app-service, and nothing under an empty profile
// or one without a flavor.
func TestPlannedWorkflows_Positive_TheProfileDecidesTheFlavor(t *testing.T) {
	repo := gitRepoWithFiles(t, map[string]string{
		"go.mod": "module example.com/svc\n", "cmd/svc/main.go": "package main\n",
		"package.json": withTest, "package-lock.json": npmLock,
	})
	if got := plannedPaths(t, repo, "framework"); !slices.Equal(got, []string{".github/workflows/ci.yml", ".github/workflows/security.yml"}) {
		t.Errorf("framework planned %q, want go-service's workflows", got)
	}
	planned, err := flavor.PlannedWorkflows(t.Context(), repo, "app-service")
	if err != nil || len(planned) != 1 || planned[0].Path != nodeCIPath {
		t.Fatalf("app-service planned %+v, %v; want typescript-node's job", planned, err)
	}
	assertJobRuns(t, planned[0].Content, []string{"npm ci", "npm test"}, []string{"go test"})
	for _, profile := range []string{"", "no-such-profile"} {
		if got := plannedPaths(t, repo, profile); len(got) != 0 {
			t.Errorf("profile %q planned %q", profile, got)
		}
	}
}
