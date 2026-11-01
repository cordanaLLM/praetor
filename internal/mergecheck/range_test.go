// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package mergecheck

import (
	"fmt"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/testsupport"
)

// Boundary: range without merges -> stated line, exit 0.
// Order: engine-dropped-hunk-gate-20261027.
func TestMergeCheck_RangeWithoutMerges_StatedLine(t *testing.T) {
	dir := initTestRepo(t)

	writeFile(t, dir, "file.txt", "one\n")
	testsupport.RunFixtureGit(t, dir,
		[]string{"add", "file.txt"},
		[]string{"commit", "-m", "chore: base"},
		[]string{"branch", "-M", "main"},
	)
	writeFile(t, dir, "file.txt", "two\n")
	testsupport.RunFixtureGit(t, dir,
		[]string{"add", "file.txt"},
		[]string{"commit", "-m", "feat: linear commit"},
	)

	res, err := Check(t.Context(), Options{RepoDir: dir, Range: "main~1..main"})
	if err != nil {
		t.Fatalf("Check failed: %v", err)
	}
	if res.MergesCount != 0 {
		t.Fatalf("expected 0 merges, got %d", res.MergesCount)
	}
	if len(res.Findings) != 0 {
		t.Fatalf("expected 0 findings, got %d", len(res.Findings))
	}
}

// Negative: octopus merge judged against each parent pair with P1.
// Order: engine-dropped-hunk-gate-20261027.
func TestMergeCheck_OctopusMerge_DroppedHunk(t *testing.T) {
	dir := initTestRepo(t)

	writeFile(t, dir, "file1.txt", "line 1\nline 2\nline 3\nline 4\nline 5\n")
	writeFile(t, dir, "file2.txt", "base 2\n")
	testsupport.RunFixtureGit(t, dir,
		[]string{"add", "file1.txt", "file2.txt"},
		[]string{"commit", "-m", "chore: base"},
		[]string{"branch", "-M", "main"},
	)

	// Branch 1 (P1)
	testsupport.RunFixtureGit(t, dir, []string{"checkout", "-b", "branch-1"})
	writeFile(t, dir, "file1.txt", "line 1\nline 2 branch 1 edit\nline 3\nline 4\nline 5\n")
	testsupport.RunFixtureGit(t, dir, []string{"add", "file1.txt"}, []string{"commit", "-m", "feat: branch 1"})

	// Branch 2 (P2)
	testsupport.RunFixtureGit(t, dir, []string{"checkout", "-b", "branch-2", "main"})
	writeFile(t, dir, "file2.txt", "branch 2 edit\n")
	testsupport.RunFixtureGit(t, dir, []string{"add", "file2.txt"}, []string{"commit", "-m", "feat: branch 2"})

	// Branch 3 (P3) - also changes file1.txt on line 5
	testsupport.RunFixtureGit(t, dir, []string{"checkout", "-b", "branch-3", "main"})
	writeFile(t, dir, "file1.txt", "line 1\nline 2\nline 3\nline 4\nline 5 branch 3 edit\n")
	testsupport.RunFixtureGit(t, dir, []string{"add", "file1.txt"}, []string{"commit", "-m", "feat: branch 3"})

	// Checkout branch-1 and create octopus merge taking branch-3's file1.txt
	testsupport.RunFixtureGit(t, dir, []string{"checkout", "branch-1"})
	// Create merge commit with 3 parents: branch-1, branch-2, branch-3
	// We can use commit-tree to synthesize the octopus merge accurately
	tree := gitRevParse(t, dir, "branch-3^{tree}")
	p1 := gitRevParse(t, dir, "branch-1")
	p2 := gitRevParse(t, dir, "branch-2")
	p3 := gitRevParse(t, dir, "branch-3")
	mergeCommit := strings.TrimSpace(testsupport.RunFixtureGit(t, dir,
		[]string{"commit-tree", tree, "-p", p1, "-p", p2, "-p", p3, "-m", "merge: octopus merge"},
	))
	testsupport.RunFixtureGit(t, dir, []string{"reset", "--hard", mergeCommit})

	res, err := Check(t.Context(), Options{RepoDir: dir, Range: "main..HEAD"})
	if err != nil {
		t.Fatalf("Check failed: %v", err)
	}
	if res.MergesCount != 1 {
		t.Fatalf("expected 1 merge, got %d", res.MergesCount)
	}
	if len(res.Findings) == 0 {
		t.Fatalf("expected findings for dropped hunk in octopus merge")
	}
	found := false
	for i := 0; i < len(res.Findings) && i < maxDiffFiles; i++ {
		f := res.Findings[i]
		if f.FilePath == "file1.txt" && f.Parent2 == p3 {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected finding for file1.txt against parent P3: %+v", res.Findings)
	}
}

// Boundary: unrelated histories (no merge base) -> stated line, exit 0.
// Order: engine-dropped-hunk-gate-20261027.
func TestMergeCheck_UnrelatedHistories_StatedLineExitZero(t *testing.T) {
	dir := initTestRepo(t)

	writeFile(t, dir, "a.txt", "history 1\n")
	testsupport.RunFixtureGit(t, dir,
		[]string{"add", "a.txt"},
		[]string{"commit", "-m", "chore: root 1"},
		[]string{"branch", "-M", "main"},
	)

	// Create orphan branch with unrelated history
	testsupport.RunFixtureGit(t, dir, []string{"checkout", "--orphan", "orphan-branch"})
	testsupport.RunFixtureGit(t, dir, []string{"rm", "-f", "a.txt"})
	writeFile(t, dir, "b.txt", "history 2\n")
	testsupport.RunFixtureGit(t, dir,
		[]string{"add", "b.txt"},
		[]string{"commit", "-m", "chore: root 2"},
	)

	// Merge orphan branch into main with --allow-unrelated-histories
	testsupport.RunFixtureGit(t, dir, []string{"checkout", "main"})
	testsupport.RunFixtureGit(t, dir,
		[]string{"merge", "--allow-unrelated-histories", "--no-edit", "orphan-branch"},
	)
	mergeSHA := gitRevParse(t, dir, "HEAD")

	res, err := Check(t.Context(), Options{RepoDir: dir, Range: "main~1..HEAD"})
	if err != nil {
		t.Fatalf("Check failed: %v", err)
	}
	if res.MergesCount != 1 {
		t.Fatalf("expected 1 merge, got %d", res.MergesCount)
	}
	if len(res.Findings) != 0 {
		t.Fatalf("expected 0 findings, got %d: %+v", len(res.Findings), res.Findings)
	}
	if len(res.Notices) != 1 {
		t.Fatalf("expected 1 notice, got %d: %+v", len(res.Notices), res.Notices)
	}
	expectedNotice := fmt.Sprintf("merge %s: no merge base, not judged", shortSHA(mergeSHA))
	if res.Notices[0] != expectedNotice {
		t.Errorf("expected notice %q, got %q", expectedNotice, res.Notices[0])
	}
}
