// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package mergecheck

import (
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/testsupport"
)

// Negative: file modified on X, deleted in merge M while present on X -> finding.
// Order: engine-dropped-hunk-gate-20261027.
func TestMergeCheck_P1ModifiedAndMergeDeletes_Finding(t *testing.T) {
	dir := initTestRepo(t)

	writeFile(t, dir, "file_x.txt", "base file x\n")
	testsupport.RunFixtureGit(t, dir,
		[]string{"add", "file_x.txt"},
		[]string{"commit", "-m", "chore: base"},
		[]string{"branch", "-M", "main"},
	)

	// Branch X modifies file_x.txt
	testsupport.RunFixtureGit(t, dir, []string{"checkout", "-b", "branch-x"})
	writeFile(t, dir, "file_x.txt", "file x modified by X\n")
	testsupport.RunFixtureGit(t, dir, []string{"add", "file_x.txt"}, []string{"commit", "-m", "feat: X modifies file_x"})

	// Branch Y changes something else
	testsupport.RunFixtureGit(t, dir, []string{"checkout", "-b", "branch-y", "main"})
	writeFile(t, dir, "other.txt", "other file\n")
	testsupport.RunFixtureGit(t, dir, []string{"add", "other.txt"}, []string{"commit", "-m", "feat: Y other"})

	// Merge Y into X and delete file_x.txt
	testsupport.RunFixtureGit(t, dir, []string{"checkout", "branch-x"})
	testsupport.RunFixtureGit(t, dir, []string{"merge", "--no-commit", "branch-y"})
	testsupport.RunFixtureGit(t, dir,
		[]string{"rm", "file_x.txt"},
		[]string{"commit", "-m", "merge: deleted file_x"},
	)

	res, err := Check(t.Context(), Options{RepoDir: dir, Range: "main..HEAD"})
	if err != nil {
		t.Fatalf("Check failed: %v", err)
	}
	if len(res.Findings) != 1 {
		t.Fatalf("expected 1 finding, got %d: %+v", len(res.Findings), res.Findings)
	}
	f := res.Findings[0]
	if f.FilePath != "file_x.txt" || !f.Deleted {
		t.Errorf("expected deleted finding for file_x.txt, got %+v", f)
	}
	formatted := FormatFinding(f)
	if !strings.Contains(formatted, "deleted in merge") || !strings.Contains(formatted, "file_x.txt") {
		t.Errorf("unexpected formatted output: %s", formatted)
	}
}

// Positive: both parents deleted the file -> merge passes.
// Order: engine-review-threads-first-20261101.
func TestMergeCheck_BothParentsDeleted_Passes(t *testing.T) {
	dir := initTestRepo(t)

	writeFile(t, dir, "both_gone.txt", "base content\n")
	testsupport.RunFixtureGit(t, dir,
		[]string{"add", "both_gone.txt"},
		[]string{"commit", "-m", "chore: base"},
		[]string{"branch", "-M", "main"},
	)

	testsupport.RunFixtureGit(t, dir,
		[]string{"checkout", "-b", "branch-x"},
		[]string{"rm", "both_gone.txt"},
		[]string{"commit", "-m", "feat: X deletes both_gone"},
		[]string{"checkout", "-b", "branch-y", "main"},
		[]string{"rm", "both_gone.txt"},
		[]string{"commit", "-m", "feat: Y deletes both_gone"},
		[]string{"checkout", "branch-x"},
		[]string{"merge", "--no-edit", "branch-y"},
	)

	res, err := Check(t.Context(), Options{RepoDir: dir, Range: "main..HEAD"})
	if err != nil {
		t.Fatalf("Check failed: %v", err)
	}
	if res.MergesCount != 1 {
		t.Fatalf("expected 1 merge, got %d", res.MergesCount)
	}
	if len(res.Findings) != 0 {
		t.Fatalf("expected 0 findings when both parents deleted file, got %d: %+v", len(res.Findings), res.Findings)
	}
}

// Negative: only one parent deleted the file and the merge drops the other's content -> refused.
// Order: engine-review-threads-first-20261101.
func TestMergeCheck_OneParentDeletedAndMergeDropsOtherContent_Refused(t *testing.T) {
	dir := initTestRepo(t)

	writeFile(t, dir, "p1_del.txt", "base p1_del\n")
	writeFile(t, dir, "p2_del.txt", "base p2_del\n")
	testsupport.RunFixtureGit(t, dir,
		[]string{"add", "p1_del.txt", "p2_del.txt"},
		[]string{"commit", "-m", "chore: base"},
		[]string{"branch", "-M", "main"},
	)

	testsupport.RunFixtureGit(t, dir, []string{"checkout", "-b", "branch-x"})
	testsupport.RunFixtureGit(t, dir, []string{"rm", "p1_del.txt"})
	writeFile(t, dir, "p2_del.txt", "modified by X\n")
	testsupport.RunFixtureGit(t, dir,
		[]string{"add", "p2_del.txt"},
		[]string{"commit", "-m", "feat: X deletes p1_del and modifies p2_del"},
	)

	testsupport.RunFixtureGit(t, dir, []string{"checkout", "-b", "branch-y", "main"})
	writeFile(t, dir, "p1_del.txt", "modified by Y\n")
	testsupport.RunFixtureGit(t, dir,
		[]string{"add", "p1_del.txt"},
		[]string{"rm", "p2_del.txt"},
		[]string{"commit", "-m", "feat: Y modifies p1_del and deletes p2_del"},
	)

	testsupport.RunFixtureGit(t, dir, []string{"checkout", "branch-x"})
	runGitAllowConflict(t, dir, "merge", "--no-commit", "branch-y")
	testsupport.RunFixtureGit(t, dir,
		[]string{"rm", "-f", "p1_del.txt", "p2_del.txt"},
		[]string{"commit", "-m", "merge: delete both files dropping modified content"},
	)

	res, err := Check(t.Context(), Options{RepoDir: dir, Range: "main..HEAD"})
	if err != nil {
		t.Fatalf("Check failed: %v", err)
	}
	if len(res.Findings) != 2 {
		t.Fatalf("expected 2 findings (both one-parent deletions dropping content), got %d: %+v", len(res.Findings), res.Findings)
	}
	if !res.Findings[0].Deleted || !res.Findings[1].Deleted {
		t.Fatalf("expected deleted findings, got %+v", res.Findings)
	}
	assertDroppedParentFinding(t, res.Findings, "p1_del.txt", "dropped second-parent changes (deleted in merge;")
	assertDroppedParentFinding(t, res.Findings, "p2_del.txt", "dropped first-parent changes (deleted in merge;")
}

// assertDroppedParentFinding asserts that findings contain a finding for path with expected text.
func assertDroppedParentFinding(t *testing.T, findings []Finding, path, expected string) {
	t.Helper()
	for _, f := range findings {
		if f.FilePath == path {
			msg := FormatFinding(f)
			if !strings.Contains(msg, expected) {
				t.Errorf("expected %s to contain %q, got %q", path, expected, msg)
			}
			return
		}
	}
	t.Fatalf("missing finding for %s", path)
}
