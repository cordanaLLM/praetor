// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package mergecheck

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/testsupport"
)

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

// Mutant verification: mutant where blob comparison is inverted or skipped.
// Order: engine-dropped-hunk-gate-20261027.
func TestMergeCheck_Mutant_BlobComparison(t *testing.T) {
	dir := initTestRepo(t)
	blobBase := writeTestBlob(t, dir, "line 1\nline 2\nline 3\n")
	blobP1 := writeTestBlob(t, dir, "line 1 P1\nline 2\nline 3\n")
	blobP2 := writeTestBlob(t, dir, "line 1\nline 2\nline 3 P2\n")
	blobMerged := writeTestBlob(t, dir, "line 1 P1\nline 2\nline 3 P2\n")

	if hunk, err := checkFileHunk(t.Context(), "test.txt", nil); err != nil || hunk != nil {
		t.Fatalf("nil blobs: got (%+v, %v), want (nil, nil)", hunk, err)
	}
	cases := []struct {
		name      string
		m, p1, p2 string
		wantFile  string
	}{
		{name: "clean merge blobM != blobP2", m: blobMerged, p1: blobP1, p2: blobP2, wantFile: ""},
		{name: "unchanged blobP2 == blobP1", m: blobP1, p1: blobP1, p2: blobP1, wantFile: ""},
		{name: "dropped hunk blobM == blobP2 and blobP2 != blobP1", m: blobP2, p1: blobP1, p2: blobP2, wantFile: "test.txt"},
		{name: "missing p1 blob entry", m: blobP2, p1: "", p2: blobP2, wantFile: ""},
	}
	for _, tc := range cases {
		blobs := buildTestCommitBlobs(dir, "test.txt", tc.m, tc.p1, tc.p2, blobBase)
		hunk, err := checkFileHunk(t.Context(), "test.txt", blobs)
		if err != nil {
			t.Fatalf("%s: unexpected error: %v", tc.name, err)
		}
		gotFile := ""
		if hunk != nil {
			gotFile = hunk.FilePath
		}
		if gotFile != tc.wantFile {
			t.Fatalf("%s: got finding file %q, want %q", tc.name, gotFile, tc.wantFile)
		}
	}
}

// writeTestBlob writes content to a temporary file and stores it as a git blob object.
func writeTestBlob(t *testing.T, dir, content string) string {
	t.Helper()
	writeFile(t, dir, "blob.tmp", content)
	return strings.TrimSpace(testsupport.RunFixtureGit(t, dir, []string{"hash-object", "-w", "blob.tmp"}))
}

// buildTestCommitBlobs constructs a commitBlobs structure for testing hunk comparisons.
func buildTestCommitBlobs(dir, file, m, p1, p2, base string) *commitBlobs {
	cb := &commitBlobs{
		repoDir:  dir,
		mergeSHA: "m",
		p1SHA:    "p1",
		p2SHA:    "p2",
		m:        map[string]string{},
		p1:       map[string]string{},
		p2:       map[string]string{},
		base:     map[string]string{file: base},
	}
	if m != "" {
		cb.m[file] = m
	}
	if p1 != "" {
		cb.p1[file] = p1
	}
	if p2 != "" {
		cb.p2[file] = p2
	}
	return cb
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

// Positive, negative and boundary: a changed file whose name starts with ':' (e.g. ':!alpha.txt'
// or ':(glob)beta.txt') uses literal pathspecs in git ls-tree chunk reads; dropped hunks are found
// and per-hunk resolutions are accepted.
func TestMergeCheck_ColonPathspec_LiteralTreated(t *testing.T) {
	if os.PathSeparator == '\\' {
		t.Skip("Windows filesystem rejects ':' in file names (HISS-21)")
	}
	dir := initTestRepo(t)

	baseContent := "line 1\nline 2\nline 3\nline 4\nline 5\n"
	writeFile(t, dir, ":!alpha.txt", baseContent)
	writeFile(t, dir, ":(glob)beta.txt", baseContent)
	testsupport.RunFixtureGit(t, dir,
		[]string{"add", "."},
		[]string{"commit", "-m", "chore: base commit with colon files"},
		[]string{"branch", "-M", "main"},
	)

	// Branch X: edits line 2 of both files
	testsupport.RunFixtureGit(t, dir, []string{"checkout", "-b", "branch-x"})
	writeFile(t, dir, ":!alpha.txt", "line 1\nline 2 mod X\nline 3\nline 4\nline 5\n")
	writeFile(t, dir, ":(glob)beta.txt", "line 1\nline 2 mod X\nline 3\nline 4\nline 5\n")
	testsupport.RunFixtureGit(t, dir,
		[]string{"add", "."},
		[]string{"commit", "-m", "feat: branch X modifications"},
	)
	p1SHA := gitRevParse(t, dir, "HEAD")

	// Branch Y: edits line 5 of both files
	testsupport.RunFixtureGit(t, dir, []string{"checkout", "-b", "branch-y", "main"})
	writeFile(t, dir, ":!alpha.txt", "line 1\nline 2\nline 3\nline 4\nline 5 mod Y\n")
	writeFile(t, dir, ":(glob)beta.txt", "line 1\nline 2\nline 3\nline 4\nline 5 mod Y\n")
	testsupport.RunFixtureGit(t, dir,
		[]string{"add", "."},
		[]string{"commit", "-m", "feat: branch Y modifications"},
	)
	p2SHA := gitRevParse(t, dir, "HEAD")

	// Merge Y into X: drop hunk on :!alpha.txt (take Y), but keep both hunks on :(glob)beta.txt
	testsupport.RunFixtureGit(t, dir, []string{"checkout", "branch-x"})
	runGitAllowConflict(t, dir, "merge", "--no-commit", "branch-y")
	writeFile(t, dir, ":!alpha.txt", "line 1\nline 2\nline 3\nline 4\nline 5 mod Y\n")
	writeFile(t, dir, ":(glob)beta.txt", "line 1\nline 2 mod X\nline 3\nline 4\nline 5 mod Y\n")
	testsupport.RunFixtureGit(t, dir,
		[]string{"add", "."},
		[]string{"commit", "-m", "merge: drop hunk on :!alpha.txt, resolve :(glob)beta.txt"},
	)
	mergeSHA := gitRevParse(t, dir, "HEAD")

	res, err := Check(t.Context(), Options{RepoDir: dir, Range: "main..HEAD"})
	if err != nil {
		t.Fatalf("Check failed: %v", err)
	}
	if len(res.Findings) != 1 {
		t.Fatalf("expected 1 finding for :!alpha.txt, got %d: %+v", len(res.Findings), res.Findings)
	}
	assertFindingMatch(t, res.Findings[0], ":!alpha.txt", mergeSHA, p1SHA, p2SHA)
}

// Negative & Boundary: planted hunk check failure (unreadable blob) returns an error naming
// the merge (short sha) and the file, and errors.As finds *exec.ExitError.
// Order: engine-1641-open-thread-20261102.
func TestMergeCheck_PlantedHunkFailure_NamesMergeAndFile(t *testing.T) {
	dir := initTestRepo(t)
	writeFile(t, dir, "alpha.txt", "line 1\nline 2\nline 3\nline 4\n")
	testsupport.RunFixtureGit(t, dir,
		[]string{"add", "alpha.txt"},
		[]string{"commit", "-m", "chore: base"},
		[]string{"branch", "-M", "main"},
	)

	// Branch X
	testsupport.RunFixtureGit(t, dir, []string{"checkout", "-b", "branch-x"})
	writeFile(t, dir, "alpha.txt", "line 1\nline 2 X\nline 3\nline 4\n")
	testsupport.RunFixtureGit(t, dir, []string{"add", "alpha.txt"}, []string{"commit", "-m", "feat: X"})
	p1SHA := gitRevParse(t, dir, "HEAD")

	// Branch Y
	testsupport.RunFixtureGit(t, dir, []string{"checkout", "-b", "branch-y", "main"})
	writeFile(t, dir, "alpha.txt", "line 1\nline 2\nline 3\nline 4 Y\n")
	testsupport.RunFixtureGit(t, dir, []string{"add", "alpha.txt"}, []string{"commit", "-m", "feat: Y"})

	// Merge taking Y
	testsupport.RunFixtureGit(t, dir, []string{"checkout", "branch-x"})
	runGitAllowConflict(t, dir, "merge", "--no-commit", "branch-y")
	testsupport.RunFixtureGit(t, dir,
		[]string{"checkout", "branch-y", "--", "alpha.txt"},
		[]string{"add", "alpha.txt"},
		[]string{"commit", "-m", "merge: taking Y"},
	)
	mergeSHA := gitRevParse(t, dir, "HEAD")

	// Plant failure: delete P1 blob object so readBlob fails during checkFileHunk
	blobP1 := strings.TrimSpace(testsupport.RunFixtureGit(t, dir, []string{"rev-parse", p1SHA + ":alpha.txt"}))
	blobPath := filepath.Join(dir, ".git", "objects", blobP1[:2], blobP1[2:])
	if err := os.Remove(blobPath); err != nil {
		t.Fatalf("remove blob %s: %v", blobP1, err)
	}

	_, err := Check(t.Context(), Options{RepoDir: dir, Range: "main..HEAD"})
	if err == nil {
		t.Fatal("expected error for unreadable blob, got nil")
	}
	wantMerge := shortSHA(mergeSHA)
	if !strings.Contains(err.Error(), "merge "+wantMerge) {
		t.Fatalf("error text must name merge %s, got: %v", wantMerge, err)
	}
	if !strings.Contains(err.Error(), "file alpha.txt") {
		t.Fatalf("error text must name file alpha.txt, got: %v", err)
	}
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) || !errors.Is(err, exitErr) {
		t.Fatalf("errors.As and errors.Is must find *exec.ExitError, got: %v", err)
	}
}

// Boundary: hunk check failure on a nested path ('pkg/sub/beta.txt') verifies that the full
// relative path and 12-char short SHA are preserved, and checkParentPair returns the wrapped error.
// Order: engine-1641-open-thread-20261102.
func TestMergeCheck_PlantedHunkFailure_NestedPath_Boundary(t *testing.T) {
	dir := initTestRepo(t)
	nestedPath := filepath.Join("pkg", "sub", "beta.txt")
	writeFile(t, dir, nestedPath, "line 1\nline 2\nline 3\n")
	testsupport.RunFixtureGit(t, dir,
		[]string{"add", "."},
		[]string{"commit", "-m", "chore: base"},
		[]string{"branch", "-M", "main"},
	)

	// Branch X
	testsupport.RunFixtureGit(t, dir, []string{"checkout", "-b", "branch-x"})
	writeFile(t, dir, nestedPath, "line 1\nline 2 X\nline 3\n")
	testsupport.RunFixtureGit(t, dir, []string{"add", "."}, []string{"commit", "-m", "feat: X"})
	p1SHA := gitRevParse(t, dir, "HEAD")

	// Branch Y
	testsupport.RunFixtureGit(t, dir, []string{"checkout", "-b", "branch-y", "main"})
	writeFile(t, dir, nestedPath, "line 1\nline 2\nline 3 Y\n")
	testsupport.RunFixtureGit(t, dir, []string{"add", "."}, []string{"commit", "-m", "feat: Y"})
	p2SHA := gitRevParse(t, dir, "HEAD")

	// Merge taking Y
	testsupport.RunFixtureGit(t, dir, []string{"checkout", "branch-x"})
	runGitAllowConflict(t, dir, "merge", "--no-commit", "branch-y")
	testsupport.RunFixtureGit(t, dir,
		[]string{"checkout", "branch-y", "--", nestedPath},
		[]string{"add", "."},
		[]string{"commit", "-m", "merge: taking Y"},
	)
	mergeSHA := gitRevParse(t, dir, "HEAD")

	blobP1 := strings.TrimSpace(testsupport.RunFixtureGit(t, dir, []string{"rev-parse", p1SHA + ":" + filepath.ToSlash(nestedPath)}))
	blobPath := filepath.Join(dir, ".git", "objects", blobP1[:2], blobP1[2:])
	if err := os.Remove(blobPath); err != nil {
		t.Fatalf("remove blob %s: %v", blobP1, err)
	}

	_, _, err := checkParentPair(t.Context(), dir, mergeSHA, p1SHA, p2SHA)
	if err == nil {
		t.Fatal("expected error from checkParentPair, got nil")
	}
	wantMerge := shortSHA(mergeSHA)
	if len(wantMerge) != 12 {
		t.Fatalf("expected 12-char short SHA, got %q", wantMerge)
	}
	if !strings.Contains(err.Error(), "merge "+wantMerge) {
		t.Fatalf("error must name merge %s, got: %v", wantMerge, err)
	}
	wantFile := "file " + filepath.ToSlash(nestedPath)
	if !strings.Contains(err.Error(), wantFile) {
		t.Fatalf("error must name %s, got: %v", wantFile, err)
	}
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) || !errors.Is(err, exitErr) {
		t.Fatalf("errors.As and errors.Is must find *exec.ExitError, got: %v", err)
	}
}

// Negative: merge retains one P1 hunk but drops another P1 hunk (blobM != blobP2) -> finding.
// Order: engine-dropped-hunk-gate-20261027.
func TestMergeCheck_RetainsOneHunk_DropsAnotherHunk_Finding(t *testing.T) {
	dir := initTestRepo(t)

	baseContent := "line 1\nline 2\nline 3\nline 4\nline 5\n"
	writeFile(t, dir, "alpha.txt", baseContent)
	testsupport.RunFixtureGit(t, dir,
		[]string{"add", "alpha.txt"},
		[]string{"commit", "-m", "chore: base"},
		[]string{"branch", "-M", "main"},
	)

	// Branch X (P1) edits line 1 and line 5
	testsupport.RunFixtureGit(t, dir, []string{"checkout", "-b", "branch-x"})
	writeFile(t, dir, "alpha.txt", "line 1 P1\nline 2\nline 3\nline 4\nline 5 P1\n")
	testsupport.RunFixtureGit(t, dir, []string{"add", "alpha.txt"}, []string{"commit", "-m", "feat: X edits 1 and 5"})
	p1SHA := gitRevParse(t, dir, "HEAD")

	// Branch Y (P2) edits line 3
	testsupport.RunFixtureGit(t, dir, []string{"checkout", "-b", "branch-y", "main"})
	writeFile(t, dir, "alpha.txt", "line 1\nline 2\nline 3 P2\nline 4\nline 5\n")
	testsupport.RunFixtureGit(t, dir, []string{"add", "alpha.txt"}, []string{"commit", "-m", "feat: Y edits 3"})
	p2SHA := gitRevParse(t, dir, "HEAD")

	// Merge Y into X retaining P1 line 1, keeping P2 line 3, but dropping P1 line 5 (keeps base line 5)
	testsupport.RunFixtureGit(t, dir, []string{"checkout", "branch-x"})
	testsupport.RunFixtureGit(t, dir, []string{"merge", "--no-commit", "branch-y"})
	writeFile(t, dir, "alpha.txt", "line 1 P1\nline 2\nline 3 P2\nline 4\nline 5\n")
	testsupport.RunFixtureGit(t, dir,
		[]string{"add", "alpha.txt"},
		[]string{"commit", "-m", "merge: keep line 1 P1, drop line 5 P1"},
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
		t.Fatalf("expected 1 finding for dropped P1 hunk when another hunk is retained, got %d: %+v", len(res.Findings), res.Findings)
	}
	assertFindingMatch(t, res.Findings[0], "alpha.txt", mergeSHA, p1SHA, p2SHA)
}
