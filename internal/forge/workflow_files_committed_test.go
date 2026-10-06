// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package forge

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/testsupport"
)

// prWorkflow is a workflow whose one job, named name, reports on every pull request.
func prWorkflow(name string) string {
	return "on:\n  pull_request:\njobs:\n  job:\n    name: " + name + "\n    runs-on: ubuntu-latest\n    steps:\n      - run: make\n"
}

// committedWorkflowRepo is a checkout whose one commit holds files, keyed by a slash path relative
// to the checkout, and returns that commit's name.
func committedWorkflowRepo(t *testing.T, files map[string]string) (string, string) {
	t.Helper()
	dir := t.TempDir()
	testsupport.InitGitRepoWithOrigin(t, dir, "")
	for rel, content := range files {
		writeAdoptFixtureFile(t, filepath.Join(dir, filepath.FromSlash(rel)), content)
	}
	commit := testsupport.RunFixtureGit(t, dir, []string{"add", "-A"},
		[]string{"commit", "--quiet", "--allow-empty", "-m", "workflows"}, []string{"rev-parse", "HEAD"})
	return dir, strings.TrimSpace(commit)
}

// Positive: the checks are the ones the commit's workflows report, not the working tree's: a job
// added after the commit is not one, and a job removed after it still is. A file that is no YAML
// document and a directory under .github/workflows are no workflows, as on disk.
func TestRequiredStatusContextsAt_Positive_ReadsTheCommitNotTheWorkingTree(t *testing.T) {
	dir, commit := committedWorkflowRepo(t, map[string]string{
		".github/workflows/ci.yml":       prWorkflow("CI"),
		".github/workflows/api.yaml":     prWorkflow("API"),
		".github/workflows/README.md":    "not a workflow\n",
		".github/workflows/nested/x.yml": prWorkflow("Nested"),
	})
	if err := os.Remove(filepath.Join(dir, ".github", "workflows", "api.yaml")); err != nil {
		t.Fatal(err)
	}
	writeWorkflowFixture(t, dir, "lint.yml", prWorkflow("Lint"))

	committed, err := RequiredStatusContextsAt(t.Context(), dir, commit, "acme/widgets")
	if err != nil {
		t.Fatalf("RequiredStatusContextsAt: %v", err)
	}
	if want := []string{"API", "CI"}; !slices.Equal(committed, want) {
		t.Fatalf("committed contexts %v, want %v", committed, want)
	}
	working, err := RequiredStatusContextsIn(t.Context(), dir, "acme/widgets")
	if err != nil {
		t.Fatalf("RequiredStatusContextsIn: %v", err)
	}
	if want := []string{"CI", "Lint"}; !slices.Equal(working, want) {
		t.Fatalf("working tree contexts %v, want %v", working, want)
	}
}

// Negative: a workflow the commit records as a symlink, a commit the checkout does not hold, a
// commit name git would read as an option and a malformed repository are refused, never read as a
// commit that reports no checks.
func TestRequiredStatusContextsAt_Negative_UnreadableCommitIsRefused(t *testing.T) {
	dir, commit := committedWorkflowRepo(t, map[string]string{".github/workflows/ci.yml": prWorkflow("CI")})
	blob := strings.TrimSpace(testsupport.RunFixtureGit(t, dir, []string{"hash-object", "-w", ".github/workflows/ci.yml"}))
	linked := strings.TrimSpace(testsupport.RunFixtureGit(t, dir,
		[]string{"update-index", "--add", "--cacheinfo", "120000," + blob + ",.github/workflows/link.yml"},
		[]string{"commit", "--quiet", "-m", "link"}, []string{"rev-parse", "HEAD"}))

	for name, tc := range map[string]struct{ commit, repository, want string }{
		"symlink":    {linked, "acme/widgets", "workflow link.yml at commit " + linked + " is not a regular file (mode 120000)"},
		"unknown":    {strings.Repeat("0", len(commit)), "acme/widgets", "list the workflows of commit"},
		"option":     {"--all", "acme/widgets", "workflows at commit \"--all\""},
		"repository": {commit, "widgets", "need an owner/name repository"},
	} {
		got, err := RequiredStatusContextsAt(t.Context(), dir, tc.commit, tc.repository)
		if err == nil || got != nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: got %v, %v; want an error containing %q", name, got, err, tc.want)
		}
	}
	var missing context.Context
	if _, err := RequiredStatusContextsAt(missing, dir, commit, "acme/widgets"); err == nil {
		t.Error("a nil context was accepted")
	}
}

// Boundary: a commit without .github/workflows reports no checks and is no error; a workflow
// directory of exactly maxWorkflowFiles entries is read, and one entry more is refused rather
// than cut.
func TestRequiredStatusContextsAt_Boundary_InventoryBounds(t *testing.T) {
	dir, commit := committedWorkflowRepo(t, map[string]string{"README.md": "no workflows\n"})
	if got, err := RequiredStatusContextsAt(t.Context(), dir, commit, "acme/widgets"); err != nil || got != nil {
		t.Fatalf("a commit without workflows: got %v, %v; want none", got, err)
	}

	files := map[string]string{".github/workflows/ci.yml": prWorkflow("CI")}
	for i := 1; i < maxWorkflowFiles; i++ {
		files[fmt.Sprintf(".github/workflows/note-%02d.txt", i)] = "not a workflow\n"
	}
	dir, full := committedWorkflowRepo(t, files)
	if got, err := RequiredStatusContextsAt(t.Context(), dir, full, "acme/widgets"); err != nil || !slices.Equal(got, []string{"CI"}) {
		t.Fatalf("%d entries: got %v, %v; want [CI]", maxWorkflowFiles, got, err)
	}
	writeWorkflowFixture(t, dir, "one-more.txt", "not a workflow\n")
	over := strings.TrimSpace(testsupport.RunFixtureGit(t, dir, []string{"add", "-A"},
		[]string{"commit", "--quiet", "-m", "one more"}, []string{"rev-parse", "HEAD"}))
	if got, err := RequiredStatusContextsAt(t.Context(), dir, over, "acme/widgets"); err == nil || got != nil {
		t.Fatalf("%d entries: got %v, %v; want a refusal", maxWorkflowFiles+1, got, err)
	}
}
