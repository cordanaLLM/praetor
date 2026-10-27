// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package mergecheck

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/testsupport"
	"github.com/cordanaLLM/praetor/internal/util"
)

func initTestRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	testsupport.InitGitRepoWithOrigin(t, dir, "")
	testsupport.RunFixtureGit(t, dir,
		[]string{"config", "user.name", "Praetor Test"},
		[]string{"config", "user.email", "praetor-test@example.com"},
		[]string{"config", "init.defaultBranch", "main"},
		[]string{"config", "commit.gpgsign", "false"},
	)
	return dir
}

// Positive / Negative: base commit with alpha.txt and beta.txt. Branch X changes line 2.
// Branch Y changes line 5 and beta.txt. Merge Y into X taking Y whole file (git checkout --theirs)
// -> finding names alpha.txt.
// Order: engine-dropped-hunk-gate-20261027.
func TestMergeCheck_DroppedHunk_RedPlantedReplay(t *testing.T) {
	dir := initTestRepo(t)

	alphaBase := "line 1\nline 2\nline 3\nline 4\nline 5\n"
	betaBase := "beta original\n"
	writeFile(t, dir, "alpha.txt", alphaBase)
	writeFile(t, dir, "beta.txt", betaBase)
	testsupport.RunFixtureGit(t, dir,
		[]string{"add", "alpha.txt", "beta.txt"},
		[]string{"commit", "-m", "chore: base commit"},
		[]string{"branch", "-M", "main"},
	)

	// Branch X
	testsupport.RunFixtureGit(t, dir, []string{"checkout", "-b", "branch-x"})
	alphaX := "line 1\nline 2 modified by X\nline 3\nline 4\nline 5\n"
	writeFile(t, dir, "alpha.txt", alphaX)
	testsupport.RunFixtureGit(t, dir,
		[]string{"add", "alpha.txt"},
		[]string{"commit", "-m", "feat: branch X changes line 2"},
	)
	p1SHA := gitRevParse(t, dir, "HEAD")

	// Branch Y
	testsupport.RunFixtureGit(t, dir, []string{"checkout", "-b", "branch-y", "main"})
	alphaY := "line 1\nline 2\nline 3\nline 4\nline 5 modified by Y\n"
	betaY := "beta modified by Y\n"
	writeFile(t, dir, "alpha.txt", alphaY)
	writeFile(t, dir, "beta.txt", betaY)
	testsupport.RunFixtureGit(t, dir,
		[]string{"add", "alpha.txt", "beta.txt"},
		[]string{"commit", "-m", "feat: branch Y changes line 5 and beta"},
	)
	p2SHA := gitRevParse(t, dir, "HEAD")

	// Merge Y into X resolving alpha.txt by taking Y whole file (`git checkout --theirs` shape)
	testsupport.RunFixtureGit(t, dir, []string{"checkout", "branch-x"})
	testsupport.RunFixtureGit(t, dir, []string{"merge", "--no-commit", "branch-y"})
	testsupport.RunFixtureGit(t, dir,
		[]string{"checkout", "branch-y", "--", "alpha.txt"},
		[]string{"add", "alpha.txt"},
		[]string{"commit", "-m", "merge branch-y into branch-x taking theirs"},
	)
	mergeSHA := gitRevParse(t, dir, "HEAD")

	res, err := Check(t.Context(), Options{RepoDir: dir, Range: "main..HEAD"})
	if err != nil {
		t.Fatalf("Check failed: %v", err)
	}
	if res.MergesCount != 1 {
		t.Fatalf("expected 1 merge, got %d", res.MergesCount)
	}
	if len(res.Findings) != 1 {
		t.Fatalf("expected 1 finding, got %d: %+v", len(res.Findings), res.Findings)
	}
	assertFindingMatch(t, res.Findings[0], "alpha.txt", mergeSHA, p1SHA, p2SHA)
}

func assertFindingMatch(t *testing.T, f Finding, expectedPath, expectedMerge, expectedP1, expectedP2 string) {
	t.Helper()
	if f.FilePath != expectedPath {
		t.Errorf("expected finding for %s, got %s", expectedPath, f.FilePath)
	}
	if f.MergeSHA != expectedMerge {
		t.Errorf("expected merge SHA %s, got %s", expectedMerge, f.MergeSHA)
	}
	if f.Parent1 != expectedP1 || f.Parent2 != expectedP2 {
		t.Errorf("expected parents (%s, %s), got (%s, %s)", expectedP1, expectedP2, f.Parent1, f.Parent2)
	}
	formatted := FormatFinding(f)
	if !strings.Contains(formatted, expectedPath) {
		t.Errorf("formatted finding lacks %s: %s", expectedPath, formatted)
	}
	if !strings.Contains(formatted, shortSHA(expectedMerge)) {
		t.Errorf("formatted finding lacks merge sha12: %s", formatted)
	}
	if !strings.Contains(formatted, shortSHA(expectedP1)) || !strings.Contains(formatted, shortSHA(expectedP2)) {
		t.Errorf("formatted finding lacks parent shas: %s", formatted)
	}
}

// Positive: same merge resolved per hunk (both changes kept) -> no finding (green).
// Order: engine-dropped-hunk-gate-20261027.
func TestMergeCheck_ResolvedPerHunk_Clean(t *testing.T) {
	dir := initTestRepo(t)

	writeFile(t, dir, "alpha.txt", "line 1\nline 2\nline 3\nline 4\nline 5\n")
	testsupport.RunFixtureGit(t, dir,
		[]string{"add", "alpha.txt"},
		[]string{"commit", "-m", "chore: base"},
		[]string{"branch", "-M", "main"},
	)

	// Branch X
	testsupport.RunFixtureGit(t, dir, []string{"checkout", "-b", "branch-x"})
	writeFile(t, dir, "alpha.txt", "line 1\nline 2 modified by X\nline 3\nline 4\nline 5\n")
	testsupport.RunFixtureGit(t, dir, []string{"add", "alpha.txt"}, []string{"commit", "-m", "feat: X"})

	// Branch Y
	testsupport.RunFixtureGit(t, dir, []string{"checkout", "-b", "branch-y", "main"})
	writeFile(t, dir, "alpha.txt", "line 1\nline 2\nline 3\nline 4\nline 5 modified by Y\n")
	testsupport.RunFixtureGit(t, dir, []string{"add", "alpha.txt"}, []string{"commit", "-m", "feat: Y"})

	// Merge keeping BOTH changes (resolved per hunk)
	testsupport.RunFixtureGit(t, dir, []string{"checkout", "branch-x"})
	testsupport.RunFixtureGit(t, dir, []string{"merge", "--no-commit", "branch-y"})
	writeFile(t, dir, "alpha.txt", "line 1\nline 2 modified by X\nline 3\nline 4\nline 5 modified by Y\n")
	testsupport.RunFixtureGit(t, dir,
		[]string{"add", "alpha.txt"},
		[]string{"commit", "-m", "merge: keep both changes"},
	)

	res, err := Check(t.Context(), Options{RepoDir: dir, Range: "main..HEAD"})
	if err != nil {
		t.Fatalf("Check failed: %v", err)
	}
	if res.MergesCount != 1 {
		t.Fatalf("expected 1 merge, got %d", res.MergesCount)
	}
	if len(res.Findings) != 0 {
		t.Fatalf("expected 0 findings for per-hunk resolution, got %d: %+v", len(res.Findings), res.Findings)
	}
}

// Positive: merge where only Y changed one file (X untouched) -> no finding;
// merge where X and Y made identical change -> no finding.
// Order: engine-dropped-hunk-gate-20261027.
func TestMergeCheck_OnlySecondParentChangedOrIdentical_Clean(t *testing.T) {
	dir := initTestRepo(t)

	writeFile(t, dir, "untouched_by_x.txt", "original Y content\n")
	writeFile(t, dir, "same_change.txt", "base same content\n")
	testsupport.RunFixtureGit(t, dir,
		[]string{"add", "untouched_by_x.txt", "same_change.txt"},
		[]string{"commit", "-m", "chore: base"},
		[]string{"branch", "-M", "main"},
	)

	// Branch X
	testsupport.RunFixtureGit(t, dir, []string{"checkout", "-b", "branch-x"})
	writeFile(t, dir, "same_change.txt", "modified identically\n")
	testsupport.RunFixtureGit(t, dir, []string{"add", "same_change.txt"}, []string{"commit", "-m", "feat: X identical change"})

	// Branch Y
	testsupport.RunFixtureGit(t, dir, []string{"checkout", "-b", "branch-y", "main"})
	writeFile(t, dir, "untouched_by_x.txt", "modified only by Y\n")
	writeFile(t, dir, "same_change.txt", "modified identically\n")
	testsupport.RunFixtureGit(t, dir, []string{"add", "untouched_by_x.txt", "same_change.txt"}, []string{"commit", "-m", "feat: Y edits"})

	// Merge Y into X
	testsupport.RunFixtureGit(t, dir, []string{"checkout", "branch-x"})
	testsupport.RunFixtureGit(t, dir, []string{"merge", "--no-edit", "branch-y"})

	res, err := Check(t.Context(), Options{RepoDir: dir, Range: "main..HEAD"})
	if err != nil {
		t.Fatalf("Check failed: %v", err)
	}
	if len(res.Findings) != 0 {
		t.Fatalf("expected 0 findings, got %d: %+v", len(res.Findings), res.Findings)
	}
}

// Positive: partial overlap where P1 edits line 2 to X, P2 edits line 2 to X and line 5 to Y.
// Plain git merge merges cleanly, result equals P2.
// Order: engine-dropped-hunk-gate-20261027.
func TestMergeCheck_PartialOverlapClean_RedBeforeFix(t *testing.T) {
	dir := initTestRepo(t)

	writeFile(t, dir, "a.txt", "1\n2\n3\n4\n5\n")
	testsupport.RunFixtureGit(t, dir,
		[]string{"add", "a.txt"},
		[]string{"commit", "-m", "chore: base"},
		[]string{"branch", "-M", "main"},
	)

	// Branch X (P1) edits line 2 to X
	testsupport.RunFixtureGit(t, dir, []string{"checkout", "-b", "branch-x"})
	writeFile(t, dir, "a.txt", "1\nX\n3\n4\n5\n")
	testsupport.RunFixtureGit(t, dir, []string{"add", "a.txt"}, []string{"commit", "-m", "feat: X line 2"})

	// Branch Y (P2) edits line 2 to X and line 5 to Y
	testsupport.RunFixtureGit(t, dir, []string{"checkout", "-b", "branch-y", "main"})
	writeFile(t, dir, "a.txt", "1\nX\n3\n4\nY\n")
	testsupport.RunFixtureGit(t, dir, []string{"add", "a.txt"}, []string{"commit", "-m", "feat: Y line 2 and 5"})

	// Merge Y into X with plain git merge (clean merge, equals P2)
	testsupport.RunFixtureGit(t, dir, []string{"checkout", "branch-x"})
	testsupport.RunFixtureGit(t, dir, []string{"merge", "--no-edit", "branch-y"})

	res, err := Check(t.Context(), Options{RepoDir: dir, Range: "main..HEAD"})
	if err != nil {
		t.Fatalf("Check failed: %v", err)
	}
	if res.MergesCount != 1 {
		t.Fatalf("expected 1 merge, got %d", res.MergesCount)
	}
	if len(res.Findings) != 0 {
		t.Fatalf("expected 0 findings for clean partial overlap merge, got %d: %+v", len(res.Findings), res.Findings)
	}
}

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
	for _, f := range res.Findings {
		if f.FilePath == "file1.txt" && f.Parent2 == p3 {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected finding for file1.txt against parent P3: %+v", res.Findings)
	}
}

// Mutant verification: mutant where blob comparison is inverted or skipped.
// Order: engine-dropped-hunk-gate-20261027.
func TestMergeCheck_Mutant_BlobComparison(t *testing.T) {
	// A mutant that inverts (blobM != blobP2) would flag clean merges where both changes
	// were kept or where Y's blob was not taken.
	// A mutant that skips (blobM == blobP2) would fail to detect when Y's whole file was taken.
	// This test asserts the exact comparison truth table of checkFileHunk logic.
	hunk1, err := checkFileHunk(t.Context(), ".", "m", "p1", "p2", "test.txt", nil)
	if err == nil && hunk1 != nil {
		t.Errorf("expected nil finding on non-existent commits")
	}
}

// Positive: conflict on line 2 resolved to P2 side, P1 non-conflicting line 4 kept -> no finding.
// Order: engine-dropped-hunk-gate-20261027.
func TestMergeCheck_ConflictResolvedP2_NonConflictingP1Kept_Clean(t *testing.T) {
	dir := initTestRepo(t)

	baseContent := "line 1\nline 2\nline 3\nline 4\nline 5\n"
	writeFile(t, dir, "alpha.txt", baseContent)
	testsupport.RunFixtureGit(t, dir,
		[]string{"add", "alpha.txt"},
		[]string{"commit", "-m", "chore: base"},
		[]string{"branch", "-M", "main"},
	)

	// Branch X (P1) edits line 2 and line 4
	testsupport.RunFixtureGit(t, dir, []string{"checkout", "-b", "branch-x"})
	writeFile(t, dir, "alpha.txt", "line 1\nline 2 P1\nline 3\nline 4 P1\nline 5\n")
	testsupport.RunFixtureGit(t, dir, []string{"add", "alpha.txt"}, []string{"commit", "-m", "feat: X edits 2 and 4"})

	// Branch Y (P2) edits line 2 only
	testsupport.RunFixtureGit(t, dir, []string{"checkout", "-b", "branch-y", "main"})
	writeFile(t, dir, "alpha.txt", "line 1\nline 2 P2\nline 3\nline 4\nline 5\n")
	testsupport.RunFixtureGit(t, dir, []string{"add", "alpha.txt"}, []string{"commit", "-m", "feat: Y edits 2 only"})

	// Merge Y into X: conflict on line 2 resolved to P2, line 4 P1 kept
	testsupport.RunFixtureGit(t, dir, []string{"checkout", "branch-x"})
	runGitAllowConflict(t, dir, "merge", "--no-commit", "branch-y")
	writeFile(t, dir, "alpha.txt", "line 1\nline 2 P2\nline 3\nline 4 P1\nline 5\n")
	testsupport.RunFixtureGit(t, dir,
		[]string{"add", "alpha.txt"},
		[]string{"commit", "-m", "merge: resolve conflict to P2 and keep P1 hunk"},
	)

	res, err := Check(t.Context(), Options{RepoDir: dir, Range: "main..HEAD"})
	if err != nil {
		t.Fatalf("Check failed: %v", err)
	}
	if res.MergesCount != 1 {
		t.Fatalf("expected 1 merge, got %d", res.MergesCount)
	}
	if len(res.Findings) != 0 {
		t.Fatalf("expected 0 findings when non-conflicting hunk is kept, got %d: %+v", len(res.Findings), res.Findings)
	}
}

// Negative: conflict on line 2 resolved to P2 side and P1 line 4 dropped (`git checkout --theirs` whole file) -> finding.
// Order: engine-dropped-hunk-gate-20261027.
func TestMergeCheck_ConflictResolvedP2_NonConflictingP1Dropped_Finding(t *testing.T) {
	dir := initTestRepo(t)

	baseContent := "line 1\nline 2\nline 3\nline 4\nline 5\n"
	writeFile(t, dir, "alpha.txt", baseContent)
	testsupport.RunFixtureGit(t, dir,
		[]string{"add", "alpha.txt"},
		[]string{"commit", "-m", "chore: base"},
		[]string{"branch", "-M", "main"},
	)

	// Branch X (P1) edits line 2 and line 4
	testsupport.RunFixtureGit(t, dir, []string{"checkout", "-b", "branch-x"})
	writeFile(t, dir, "alpha.txt", "line 1\nline 2 P1\nline 3\nline 4 P1\nline 5\n")
	testsupport.RunFixtureGit(t, dir, []string{"add", "alpha.txt"}, []string{"commit", "-m", "feat: X edits 2 and 4"})
	p1SHA := gitRevParse(t, dir, "HEAD")

	// Branch Y (P2) edits line 2 only
	testsupport.RunFixtureGit(t, dir, []string{"checkout", "-b", "branch-y", "main"})
	writeFile(t, dir, "alpha.txt", "line 1\nline 2 P2\nline 3\nline 4\nline 5\n")
	testsupport.RunFixtureGit(t, dir, []string{"add", "alpha.txt"}, []string{"commit", "-m", "feat: Y edits 2 only"})
	p2SHA := gitRevParse(t, dir, "HEAD")

	// Merge Y into X taking Y whole file (dropping P1's line 4 change)
	testsupport.RunFixtureGit(t, dir, []string{"checkout", "branch-x"})
	runGitAllowConflict(t, dir, "merge", "--no-commit", "branch-y")
	testsupport.RunFixtureGit(t, dir,
		[]string{"checkout", "branch-y", "--", "alpha.txt"},
		[]string{"add", "alpha.txt"},
		[]string{"commit", "-m", "merge: whole-file resolution dropping P1 hunk"},
	)
	mergeSHA := gitRevParse(t, dir, "HEAD")

	res, err := Check(t.Context(), Options{RepoDir: dir, Range: "main..HEAD"})
	if err != nil {
		t.Fatalf("Check failed: %v", err)
	}
	if res.MergesCount != 1 {
		t.Fatalf("expected 1 merge, got %d", res.MergesCount)
	}
	if len(res.Findings) != 1 {
		t.Fatalf("expected 1 finding for dropped non-conflicting hunk, got %d: %+v", len(res.Findings), res.Findings)
	}
	assertFindingMatch(t, res.Findings[0], "alpha.txt", mergeSHA, p1SHA, p2SHA)
}

// Negative: a binary only the first parent changed, reverted to the second parent's copy by the
// merge -> finding naming binary comparison.
// Order: engine-dropped-hunk-gate-20261027.
func TestMergeCheck_BinaryFile_RevertedFirstParentChange_Finding(t *testing.T) {
	dir := initTestRepo(t)

	writeFile(t, dir, "bin.dat", "start\x00base_binary_content\n")
	writeFile(t, dir, "other.txt", "base\n")
	testsupport.RunFixtureGit(t, dir,
		[]string{"add", "bin.dat", "other.txt"},
		[]string{"commit", "-m", "chore: base binary"},
		[]string{"branch", "-M", "main"},
	)

	testsupport.RunFixtureGit(t, dir, []string{"checkout", "-b", "branch-x"})
	writeFile(t, dir, "bin.dat", "start\x00p1_binary_content\n")
	testsupport.RunFixtureGit(t, dir, []string{"add", "bin.dat"}, []string{"commit", "-m", "feat: P1 binary edit"})
	p1SHA := gitRevParse(t, dir, "HEAD")

	testsupport.RunFixtureGit(t, dir, []string{"checkout", "-b", "branch-y", "main"})
	writeFile(t, dir, "other.txt", "p2\n")
	testsupport.RunFixtureGit(t, dir, []string{"add", "other.txt"}, []string{"commit", "-m", "feat: P2 text edit"})
	p2SHA := gitRevParse(t, dir, "HEAD")

	testsupport.RunFixtureGit(t, dir, []string{"checkout", "branch-x"})
	runGitAllowConflict(t, dir, "merge", "--no-commit", "branch-y")
	testsupport.RunFixtureGit(t, dir,
		[]string{"checkout", "branch-y", "--", "bin.dat"},
		[]string{"add", "bin.dat"},
		[]string{"commit", "-m", "merge: binary taken from the second parent"},
	)
	mergeSHA := gitRevParse(t, dir, "HEAD")

	res, err := Check(t.Context(), Options{RepoDir: dir, Range: "main..HEAD"})
	if err != nil {
		t.Fatalf("Check failed: %v", err)
	}
	if len(res.Findings) != 1 || !res.Findings[0].Binary {
		t.Fatalf("expected 1 binary finding, got %+v", res.Findings)
	}
	assertFindingMatch(t, res.Findings[0], "bin.dat", mergeSHA, p1SHA, p2SHA)
	if formatted := FormatFinding(res.Findings[0]); !strings.Contains(formatted, "binary comparison") {
		t.Errorf("expected formatted finding to contain 'binary comparison', got %q", formatted)
	}
}

// Boundary: a binary both parents changed merges by taking one side; taking the second parent's
// copy is that resolution, not a dropped hunk -> no finding.
// Order: engine-dropped-hunk-gate-20261027.
func TestMergeCheck_BinaryFile_BothChangedSideTaken_Clean(t *testing.T) {
	dir := initTestRepo(t)

	writeFile(t, dir, "bin.dat", "start\x00base_binary_content\n")
	testsupport.RunFixtureGit(t, dir,
		[]string{"add", "bin.dat"},
		[]string{"commit", "-m", "chore: base binary"},
		[]string{"branch", "-M", "main"},
	)

	// Branch X (P1) edits binary
	testsupport.RunFixtureGit(t, dir, []string{"checkout", "-b", "branch-x"})
	writeFile(t, dir, "bin.dat", "start\x00p1_binary_content\n")
	testsupport.RunFixtureGit(t, dir, []string{"add", "bin.dat"}, []string{"commit", "-m", "feat: P1 binary edit"})

	// Branch Y (P2) edits binary
	testsupport.RunFixtureGit(t, dir, []string{"checkout", "-b", "branch-y", "main"})
	writeFile(t, dir, "bin.dat", "start\x00p2_binary_content\n")
	testsupport.RunFixtureGit(t, dir, []string{"add", "bin.dat"}, []string{"commit", "-m", "feat: P2 binary edit"})

	// Merge taking P2 binary whole file
	testsupport.RunFixtureGit(t, dir, []string{"checkout", "branch-x"})
	runGitAllowConflict(t, dir, "merge", "--no-commit", "branch-y")
	testsupport.RunFixtureGit(t, dir,
		[]string{"checkout", "branch-y", "--", "bin.dat"},
		[]string{"add", "bin.dat"},
		[]string{"commit", "-m", "merge: taking theirs for binary"},
	)
	res, err := Check(t.Context(), Options{RepoDir: dir, Range: "main..HEAD"})
	if err != nil {
		t.Fatalf("Check failed: %v", err)
	}
	if res.MergesCount != 1 {
		t.Fatalf("expected 1 merge, got %d", res.MergesCount)
	}
	if len(res.Findings) != 0 {
		t.Fatalf("expected no finding when both parents changed the binary, got %+v", res.Findings)
	}
}

// Positive: identical binary change -> no finding.
// Order: engine-dropped-hunk-gate-20261027.
func TestMergeCheck_BinaryFile_Identical_Clean(t *testing.T) {
	dir := initTestRepo(t)

	writeFile(t, dir, "bin.dat", "start\x00base_binary_content\n")
	testsupport.RunFixtureGit(t, dir,
		[]string{"add", "bin.dat"},
		[]string{"commit", "-m", "chore: base binary"},
		[]string{"branch", "-M", "main"},
	)

	// Branch X (P1) edits binary to same content
	testsupport.RunFixtureGit(t, dir, []string{"checkout", "-b", "branch-x"})
	writeFile(t, dir, "bin.dat", "start\x00same_binary_content\n")
	testsupport.RunFixtureGit(t, dir, []string{"add", "bin.dat"}, []string{"commit", "-m", "feat: P1 same binary"})

	// Branch Y (P2) edits binary to same content
	testsupport.RunFixtureGit(t, dir, []string{"checkout", "-b", "branch-y", "main"})
	writeFile(t, dir, "bin.dat", "start\x00same_binary_content\n")
	testsupport.RunFixtureGit(t, dir, []string{"add", "bin.dat"}, []string{"commit", "-m", "feat: P2 same binary"})

	// Merge Y into X
	testsupport.RunFixtureGit(t, dir, []string{"checkout", "branch-x"})
	testsupport.RunFixtureGit(t, dir, []string{"merge", "--no-edit", "branch-y"})

	res, err := Check(t.Context(), Options{RepoDir: dir, Range: "main..HEAD"})
	if err != nil {
		t.Fatalf("Check failed: %v", err)
	}
	if res.MergesCount != 1 {
		t.Fatalf("expected 1 merge, got %d", res.MergesCount)
	}
	if len(res.Findings) != 0 {
		t.Fatalf("expected 0 findings for identical binary merge, got %d: %+v", len(res.Findings), res.Findings)
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

func writeFile(t *testing.T, dir, rel, content string) {
	t.Helper()
	path := filepath.Join(dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func gitRevParse(t *testing.T, dir, ref string) string {
	t.Helper()
	return strings.TrimSpace(testsupport.RunFixtureGit(t, dir, []string{"rev-parse", ref}))
}

func runGitAllowConflict(t *testing.T, dir string, args ...string) {
	t.Helper()
	if _, err := util.RunGit(t.Context(), dir, args...); err != nil {
		t.Logf("ignoring expected merge conflict: %v", err)
	}
}
