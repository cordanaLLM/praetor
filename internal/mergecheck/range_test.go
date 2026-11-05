// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package mergecheck

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
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
	if !hasFindingAgainst(res.Findings, "file1.txt", p3) {
		t.Errorf("expected finding for file1.txt against parent P3: %+v", res.Findings)
	}
}

// hasFindingAgainst reports whether findings name path against the given second parent.
func hasFindingAgainst(findings []Finding, path, parent string) bool {
	for i := 0; i < len(findings) && i < maxDiffFiles; i++ {
		if findings[i].FilePath == path && findings[i].Parent2 == parent {
			return true
		}
	}
	return false
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

// Positive: running Check with RepoDir set to a subdirectory of the repository still resolves
// root-relative paths with --full-tree and detects dropped hunks.
func TestMergeCheck_SubdirectoryRepoDir_DetectsDroppedHunk(t *testing.T) {
	dir := initTestRepo(t)
	writeFile(t, dir, "alpha.txt", "line 1\nline 2\nline 3\nline 4\nline 5\n")
	testsupport.RunFixtureGit(t, dir,
		[]string{"add", "alpha.txt"},
		[]string{"commit", "-m", "chore: base"},
		[]string{"branch", "-M", "main"},
	)
	testsupport.RunFixtureGit(t, dir, []string{"checkout", "-b", "branch-x"})
	writeFile(t, dir, "alpha.txt", "line 1\nline 2 P1\nline 3\nline 4\nline 5\n")
	testsupport.RunFixtureGit(t, dir, []string{"add", "alpha.txt"}, []string{"commit", "-m", "feat: X"})
	testsupport.RunFixtureGit(t, dir, []string{"checkout", "-b", "branch-y", "main"})
	writeFile(t, dir, "alpha.txt", "line 1\nline 2\nline 3\nline 4\nline 5 P2\n")
	testsupport.RunFixtureGit(t, dir, []string{"add", "alpha.txt"}, []string{"commit", "-m", "feat: Y"})
	testsupport.RunFixtureGit(t, dir, []string{"checkout", "branch-x"})
	testsupport.RunFixtureGit(t, dir, []string{"merge", "--no-commit", "branch-y"})
	testsupport.RunFixtureGit(t, dir,
		[]string{"checkout", "branch-y", "--", "alpha.txt"},
		[]string{"add", "alpha.txt"},
		[]string{"commit", "-m", "merge: take Y"},
	)
	subDir := filepath.Join(dir, "nested", "sub")
	// nosemgrep: go.lang.correctness.permissions.file_permission.incorrect-default-permission -- a directory needs the owner search bit to hold entries
	if err := os.MkdirAll(subDir, 0o700); err != nil {
		t.Fatal(err)
	}
	res, err := Check(t.Context(), Options{RepoDir: subDir, Range: "main..HEAD"})
	if err != nil {
		t.Fatalf("Check from subdirectory failed: %v", err)
	}
	if len(res.Findings) != 1 || res.Findings[0].FilePath != "alpha.txt" {
		t.Fatalf("expected 1 finding for alpha.txt from subdirectory RepoDir, got %+v", res.Findings)
	}
}

// Positive/Boundary: treeBlobs chunks path arguments under a 16 KiB command-line byte budget.
func TestMergeCheck_TreeBlobs_ChunksPathsUnderCommandLineBudget(t *testing.T) {
	if os.PathSeparator == '\\' {
		t.Skip("POSIX shell wrapper required")
	}
	dir := initTestRepo(t)
	writeFile(t, dir, "a.txt", "hello\n")
	testsupport.RunFixtureGit(t, dir,
		[]string{"add", "a.txt"},
		[]string{"commit", "-m", "chore: base"},
		[]string{"branch", "-M", "main"},
	)
	head := gitRevParse(t, dir, "HEAD")

	realGit, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	shimDir := t.TempDir()
	shimPath := filepath.Join(shimDir, "git")
	script := "#!/bin/sh\n" +
		"bytes=0\n" +
		"for arg in \"$@\"; do bytes=$((bytes + ${#arg} + 1)); done\n" +
		"if [ \"$bytes\" -gt 16600 ]; then echo \"arg list too long: $bytes\" >&2; exit 128; fi\n" +
		"exec \"" + realGit + "\" \"$@\"\n"
	// nosemgrep: go_file-permissions_rule-fileperm, go.lang.correctness.permissions.file_permission.incorrect-default-permission -- a stub run through PATH needs the owner execute bit
	if err := os.WriteFile(shimPath, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", shimDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	paths := make([]string, 0, 260)
	paths = append(paths, "a.txt")
	for i := 0; i < 259; i++ {
		paths = append(paths, fmt.Sprintf("dir/subdir/very_long_file_name_padding_to_exceed_16k_budget_%04d.txt", i))
	}
	blobs, err := treeBlobs(t.Context(), dir, head, paths)
	if err != nil {
		t.Fatalf("treeBlobs failed under 16 KiB argument cap: %v", err)
	}
	if blobs["a.txt"] == "" {
		t.Fatalf("expected blob for a.txt, got %+v", blobs)
	}
	chunks, err := chunkPaths(paths, maxLsTreeArgBytes)
	if err != nil || len(chunks) < 2 {
		t.Fatalf("expected >= 2 chunks under 16 KiB budget, got len=%d err=%v", len(chunks), err)
	}
	if _, err := chunkPaths(make([]string, maxDiffFiles+1), maxLsTreeArgBytes); err == nil {
		t.Fatalf("expected error when paths exceed maxDiffFiles")
	}
}

// Negative: exceeding maxParents on an octopus merge or maxDiffFiles in filesChanged fails closed.
func TestMergeCheck_Negative_BoundsFailClosedWhenExceeded(t *testing.T) {
	items, err := parseNullSeparated([]byte("a\x00b\x00"), 2)
	if err != nil || len(items) != 2 {
		t.Fatalf("boundary 2 items: got (%v, %v), want 2 items and nil err", items, err)
	}
	if _, err := parseNullSeparated([]byte("a\x00b\x00c\x00"), 2); err == nil || !strings.Contains(err.Error(), "exceeds 2 changed files") {
		t.Fatalf("expected error when parseNullSeparated exceeds maxItems, got: %v", err)
	}
	dir := initTestRepo(t)
	writeFile(t, dir, "a.txt", "base\n")
	testsupport.RunFixtureGit(t, dir,
		[]string{"add", "a.txt"},
		[]string{"commit", "-m", "chore: base"},
		[]string{"branch", "-M", "main"},
	)
	tree := gitRevParse(t, dir, "HEAD^{tree}")
	baseSHA := gitRevParse(t, dir, "HEAD")

	args := []string{"commit-tree", tree}
	for i := 0; i < maxParents+1; i++ {
		parent := strings.TrimSpace(testsupport.RunFixtureGit(t, dir, []string{
			"commit-tree", tree, "-p", baseSHA, "-m", fmt.Sprintf("chore: parent %d", i),
		}))
		args = append(args, "-p", parent)
	}
	args = append(args, "-m", "merge: exceeds maxParents")
	mergeCommit := strings.TrimSpace(testsupport.RunFixtureGit(t, dir, args))
	testsupport.RunFixtureGit(t, dir, []string{"reset", "--hard", mergeCommit})

	_, err = Check(t.Context(), Options{RepoDir: dir, Range: baseSHA + ".." + mergeCommit})
	if err == nil || !strings.Contains(err.Error(), "exceeds limit") {
		t.Fatalf("expected error when merge parent count exceeds maxParents (%d), got: %v", maxParents, err)
	}
}

// Positive/Boundary: ResolveDefaultRange uses GITHUB_EVENT_BEFORE when set to a non-zero commit,
// and falls back to origin/<default>..HEAD when unset or all-zero.
func TestMergeCheck_ResolveDefaultRange_UsesGitHubEventBefore(t *testing.T) {
	dir := initTestRepo(t)
	writeFile(t, dir, "a.txt", "one\n")
	testsupport.RunFixtureGit(t, dir,
		[]string{"add", "a.txt"},
		[]string{"commit", "-m", "chore: one"},
		[]string{"branch", "-M", "main"},
	)
	beforeSHA := gitRevParse(t, dir, "HEAD")
	writeFile(t, dir, "a.txt", "two\n")
	testsupport.RunFixtureGit(t, dir,
		[]string{"add", "a.txt"},
		[]string{"commit", "-m", "feat: two"},
		[]string{"update-ref", "refs/remotes/origin/main", "HEAD"},
	)

	t.Setenv("GITHUB_EVENT_BEFORE", beforeSHA)
	got, err := ResolveDefaultRange(t.Context(), dir)
	if err != nil || got != beforeSHA+"..HEAD" {
		t.Fatalf("with GITHUB_EVENT_BEFORE=%s: got (%q, %v), want (%q, nil)", beforeSHA, got, err, beforeSHA+"..HEAD")
	}

	t.Setenv("GITHUB_EVENT_BEFORE", strings.Repeat("0", 40))
	gotZero, err := ResolveDefaultRange(t.Context(), dir)
	if err != nil || gotZero != "origin/main..HEAD" {
		t.Fatalf("with zero GITHUB_EVENT_BEFORE: got (%q, %v), want (\"origin/main..HEAD\", nil)", gotZero, err)
	}
}

// Negative: ResolveDefaultRange propagates errors from RepositoryDefaultBranch instead of falling back to main.
func TestMergeCheck_ResolveDefaultRange_ErrorPropagated(t *testing.T) {
	dir := initTestRepo(t)
	writeFile(t, dir, ".standards.yaml", "repository:\n  default_branch: \"invalid branch with spaces\"\n")
	_, err := ResolveDefaultRange(t.Context(), dir)
	if err == nil {
		t.Fatalf("expected error from ResolveDefaultRange on invalid default branch declaration, got nil")
	}
	if !strings.Contains(err.Error(), "default_branch") {
		t.Fatalf("expected error mentioning default_branch, got: %v", err)
	}
}

// Positive and Boundary: mergeBase on a SHA-256 repository does not trip the output cap (65 bytes with newline).
func TestMergeCheck_MergeBase_SHA256Repository(t *testing.T) {
	dir := t.TempDir()
	testsupport.RunFixtureGit(t, dir,
		[]string{"init", "--object-format=sha256", "-q"},
		[]string{"config", "user.name", "Praetor Test"},
		[]string{"config", "user.email", "praetor-test@example.com"},
		[]string{"config", "commit.gpgsign", "false"},
	)
	writeFile(t, dir, "a.txt", "base\n")
	testsupport.RunFixtureGit(t, dir,
		[]string{"add", "a.txt"},
		[]string{"commit", "-m", "chore: base"},
		[]string{"branch", "-M", "main"},
	)
	baseSHA := gitRevParse(t, dir, "HEAD")
	if len(baseSHA) != 64 {
		t.Fatalf("expected 64-char sha256 hash, got %d chars: %s", len(baseSHA), baseSHA)
	}

	testsupport.RunFixtureGit(t, dir, []string{"checkout", "-b", "branch-x"})
	writeFile(t, dir, "a.txt", "base\nx\n")
	testsupport.RunFixtureGit(t, dir, []string{"add", "a.txt"}, []string{"commit", "-m", "feat: x"})
	p1SHA := gitRevParse(t, dir, "HEAD")

	testsupport.RunFixtureGit(t, dir, []string{"checkout", "-b", "branch-y", "main"})
	writeFile(t, dir, "a.txt", "base\ny\n")
	testsupport.RunFixtureGit(t, dir, []string{"add", "a.txt"}, []string{"commit", "-m", "feat: y"})
	p2SHA := gitRevParse(t, dir, "HEAD")

	gotBase, err := mergeBase(t.Context(), dir, p1SHA, p2SHA)
	if err != nil {
		t.Fatalf("mergeBase failed on SHA-256 repository: %v", err)
	}
	if gotBase != baseSHA {
		t.Fatalf("expected merge base %s, got %s", baseSHA, gotBase)
	}
}
