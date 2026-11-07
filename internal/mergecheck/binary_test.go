// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package mergecheck

import (
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/testsupport"
)

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
