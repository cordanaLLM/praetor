package gc

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// =========================================================================
// Positive 3D Tests
// =========================================================================

// mkdirT creates a directory tree, failing the test on error.
func mkdirT(t *testing.T, path string) string {
	t.Helper()
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatalf("failed to create %s: %v", path, err)
	}
	return path
}

// writeFileT writes a file, failing the test on error.
func writeFileT(t *testing.T, path, content string) string {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("failed to write %s: %v", path, err)
	}
	return path
}

// ageTree backdates every entry in root and root itself, walking iteratively (HISS-01:
// no recursion). Backdating only the top-level directory is not enough: staleness is
// measured from the newest mtime anywhere in the tree.
func ageTree(t *testing.T, root string, age time.Duration) {
	t.Helper()
	stamp := time.Now().Add(-age)
	info, err := os.Lstat(root)
	if err != nil {
		t.Fatalf("failed to stat %s: %v", root, err)
	}
	if !info.IsDir() {
		if err := os.Chtimes(root, stamp, stamp); err != nil {
			t.Fatalf("failed to backdate %s: %v", root, err)
		}
		return
	}
	const maxDirs = 128
	dirs := []string{root}
	var pending []string
	for i := 0; i < maxDirs && len(dirs) > 0; i++ {
		curr := dirs[0]
		dirs = dirs[1:]
		pending = append(pending, curr)
		entries, err := os.ReadDir(curr)
		if err != nil {
			t.Fatalf("failed to read %s: %v", curr, err)
		}
		for _, entry := range entries {
			child := filepath.Join(curr, entry.Name())
			if entry.IsDir() {
				dirs = append(dirs, child)
				continue
			}
			if chErr := os.Chtimes(child, stamp, stamp); chErr != nil {
				t.Fatalf("failed to backdate %s: %v", child, chErr)
			}
		}
	}
	// Directories last: writing to a file inside a directory refreshes that directory.
	for i := len(pending) - 1; i >= 0; i-- {
		if chErr := os.Chtimes(pending[i], stamp, stamp); chErr != nil {
			t.Fatalf("failed to backdate %s: %v", pending[i], chErr)
		}
	}
}

func setupStaleAndEphemeralFixtures(t *testing.T) (string, string, string) {
	t.Helper()
	root, staleWT := gcGitFixture(t)
	ephDir := mkdirT(t, filepath.Join(root, ".standards", "ephemeral"))
	writeFileT(t, filepath.Join(staleWT, "code.go"), "package main\n")
	runGCTestGit(t, staleWT, "-c", "user.name=gc-test", "-c", "user.email=gc@example.invalid", "add", "code.go")
	runGCTestGit(t, staleWT, "-c", "user.name=gc-test", "-c", "user.email=gc@example.invalid", "commit", "-q", "-m", "fixture")
	ageTree(t, staleWT, 48*time.Hour)

	sarifFile := writeFileT(t, filepath.Join(ephDir, "diagnostics.sarif"), `{"version":"2.1.0","runs":[]}`)
	ageTree(t, sarifFile, 48*time.Hour)
	return root, staleWT, sarifFile
}

func TestCollect_Positive_PruneStaleAndEphemeral(t *testing.T) {
	ctx := context.Background()
	tmpDir, staleWT, sarifFile := setupStaleAndEphemeralFixtures(t)

	opts := Options{
		RootDir:        tmpDir,
		MaxWorktreeAge: 24 * time.Hour,
		MaxArtifactAge: 24 * time.Hour,
		ReleasedPaths:  []string{".standards/worktrees/released-old", ".standards/ephemeral/diagnostics.sarif"},
		DryRun:         false,
		SkipGitPrune:   true,
		SkipTestCache:  true,
	}

	report, err := Collect(ctx, opts)
	if err != nil || report == nil {
		t.Fatalf("Collect failed: %v", err)
	}

	if report.ReclaimedBytes <= 0 || len(report.PrunedWorktrees) != 1 || len(report.PurgedEphemeralFiles) != 1 {
		t.Errorf("unexpected report results: %+v", report)
	}

	if _, statErr := os.Stat(staleWT); !os.IsNotExist(statErr) {
		t.Errorf("expected stale worktree to be removed from disk, but still exists")
	}
	if _, statErr := os.Stat(sarifFile); !os.IsNotExist(statErr) {
		t.Errorf("expected sarif file to be removed from disk, but still exists")
	}
}

func TestCollect_Positive_DryRunSimulation(t *testing.T) {
	ctx := context.Background()
	tmpDir, staleWT := gcGitFixture(t)
	ephDir := mkdirT(t, filepath.Join(tmpDir, ".standards", "ephemeral"))
	writeFileT(t, filepath.Join(staleWT, "dummy.txt"), "payload")
	runGCTestGit(t, staleWT, "-c", "user.name=gc-test", "-c", "user.email=gc@example.invalid", "add", "dummy.txt")
	runGCTestGit(t, staleWT, "-c", "user.name=gc-test", "-c", "user.email=gc@example.invalid", "commit", "-q", "-m", "fixture")
	ageTree(t, staleWT, 36*time.Hour)

	sarifFile := writeFileT(t, filepath.Join(ephDir, "dry-run.sarif"), "sarif-data")
	ageTree(t, sarifFile, 36*time.Hour)

	opts := Options{
		RootDir:        tmpDir,
		MaxWorktreeAge: 24 * time.Hour,
		MaxArtifactAge: 24 * time.Hour,
		ReleasedPaths:  []string{".standards/worktrees/released-old", ".standards/ephemeral/dry-run.sarif"},
		DryRun:         true,
		SkipGitPrune:   true,
		SkipTestCache:  true,
	}

	report, err := Collect(ctx, opts)
	if err != nil {
		t.Fatalf("Collect failed: %v", err)
	}

	if !report.DryRun {
		t.Errorf("expected report.DryRun == true")
	}
	if report.ReclaimedBytes != 0 || len(report.PrunedWorktrees) != 0 {
		t.Errorf("dry-run must not report completed removals: %+v", report)
	}
	if report.PlannedReclaimedBytes <= 0 || len(report.PlannedWorktrees) != 1 {
		t.Errorf("expected planned worktree removal, got %+v", report)
	}

	// Files MUST still exist on disk under DryRun
	if _, statErr := os.Stat(staleWT); os.IsNotExist(statErr) {
		t.Errorf("expected stale worktree to remain on disk during dry-run, but it was deleted")
	}
	if _, statErr := os.Stat(sarifFile); os.IsNotExist(statErr) {
		t.Errorf("expected sarif file to remain on disk during dry-run, but it was deleted")
	}
}

func TestCollect_Positive_TestCacheAndBinaries(t *testing.T) {
	ctx := context.Background()
	tmpDir := t.TempDir()

	tmpSubDir := mkdirT(t, filepath.Join(tmpDir, ".standards", "tmp"))
	tempFile := writeFileT(t, filepath.Join(tmpSubDir, "scratch.tmp"), "temp data")
	ageTree(t, tempFile, 48*time.Hour)
	binDir := mkdirT(t, filepath.Join(tmpDir, "bin"))
	testBinary := writeFileT(t, filepath.Join(binDir, "package.test"), "must survive")

	opts := Options{
		RootDir:       tmpDir,
		DryRun:        false,
		SkipGitPrune:  true,
		SkipTestCache: false, CleanGoTestCache: false, MaxArtifactAge: 24 * time.Hour,
		ReleasedPaths: []string{".standards/tmp/scratch.tmp"},
	}

	report, err := Collect(ctx, opts)
	if err != nil {
		t.Fatalf("Collect failed: %v", err)
	}

	if len(report.CleanedCacheArtifacts) != 1 {
		t.Errorf("expected exactly one released artifact, got %d: %v",
			len(report.CleanedCacheArtifacts), report.CleanedCacheArtifacts)
	}

	if _, statErr := os.Stat(testBinary); statErr != nil {
		t.Errorf("default bin sweep must preserve binary: %v", statErr)
	}
	if _, statErr := os.Stat(tempFile); !os.IsNotExist(statErr) {
		t.Errorf("expected temporary scratch file to be deleted")
	}
}

// =========================================================================
// Negative 3D Tests
// =========================================================================

func TestCollect_Negative_ContextCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	opts := Options{
		RootDir: t.TempDir(),
	}

	report, err := Collect(ctx, opts)
	if err == nil {
		t.Errorf("expected error when context is cancelled, got nil")
	}
	if report == nil {
		t.Errorf("expected partial report on cancelled context")
	} else if report.Complete {
		t.Errorf("cancelled collection reported complete: %+v", report)
	}
	if !strings.Contains(err.Error(), "context") && !strings.Contains(err.Error(), "cancelled") {
		t.Errorf("expected cancellation message, got %v", err)
	}
}

func TestCollect_Negative_NilContext(t *testing.T) {
	opts := Options{
		RootDir: t.TempDir(),
	}

	// A nil context variable rather than a literal nil: the call is the point of the
	// test (Collect must reject it), not an accidental nil-context bug.
	var nilCtx context.Context
	report, err := Collect(nilCtx, opts)
	if err == nil {
		t.Errorf("expected error when context is nil, got nil")
	}
	if report == nil {
		t.Errorf("expected partial report for nil context")
	} else if report.Complete {
		t.Errorf("nil-context collection reported complete: %+v", report)
	}
	if !strings.Contains(err.Error(), "context cannot be nil") {
		t.Errorf("expected 'context cannot be nil' message, got %v", err)
	}
}

func TestCollect_Negative_NonexistentRootDir(t *testing.T) {
	ctx := context.Background()
	opts := Options{
		RootDir: "/path/to/nonexistent/directory/unlikely/to/exist/12345",
	}

	report, err := Collect(ctx, opts)
	if err == nil {
		t.Errorf("expected error for nonexistent root dir, got nil")
	}
	if report == nil {
		t.Errorf("expected partial report for invalid root dir")
	} else if report.Complete {
		t.Errorf("invalid root collection reported complete: %+v", report)
	}
	if !strings.Contains(err.Error(), "does not exist") {
		t.Errorf("expected 'does not exist' error message, got %v", err)
	}
}

func TestCollect_Negative_ResilienceToNonGitWorkspace(t *testing.T) {
	ctx := context.Background()
	tmpDir := t.TempDir()

	// Root dir without .git folder, SkipGitPrune is false
	opts := Options{
		RootDir:       tmpDir,
		SkipGitPrune:  false,
		SkipTestCache: true,
	}

	report, err := Collect(ctx, opts)
	if err != nil {
		t.Fatalf("expected graceful resilience when not a git repository, got error: %v", err)
	}
	if report == nil {
		t.Fatalf("expected non-nil report")
	}
	// Git prune should simply be bypassed
	if len(report.PrunedWorktrees) != 0 {
		t.Errorf("expected zero pruned worktrees in empty non-git dir, got %d", len(report.PrunedWorktrees))
	}
}

// =========================================================================
// Boundary 3D Tests
// =========================================================================

func TestCollect_Boundary_WorktreeAgeThreshold(t *testing.T) {
	ctx := context.Background()
	tmpDir, staleWT := gcGitFixture(t)
	wtDir := filepath.Dir(staleWT)

	// Fresh worktree: 1 hour old (below 24h boundary)
	freshWT := mkdirT(t, filepath.Join(wtDir, "fresh-worktree"))
	writeFileT(t, filepath.Join(freshWT, "fresh.txt"), "fresh")
	ageTree(t, freshWT, 1*time.Hour)

	// Stale worktree: 25 hours old (above 24h boundary)
	writeFileT(t, filepath.Join(staleWT, "stale.txt"), "stale")
	runGCTestGit(t, staleWT, "-c", "user.name=gc-test", "-c", "user.email=gc@example.invalid", "add", "stale.txt")
	runGCTestGit(t, staleWT, "-c", "user.name=gc-test", "-c", "user.email=gc@example.invalid", "commit", "-q", "-m", "fixture")
	ageTree(t, staleWT, 25*time.Hour)

	opts := Options{
		RootDir:        tmpDir,
		MaxWorktreeAge: 24 * time.Hour,
		DryRun:         false,
		SkipGitPrune:   true,
		SkipTestCache:  true,
		ReleasedPaths:  []string{".standards/worktrees/released-old"},
	}

	report, err := Collect(ctx, opts)
	if err != nil {
		t.Fatalf("Collect failed: %v", err)
	}

	if len(report.PrunedWorktrees) != 1 {
		t.Fatalf("expected exactly 1 pruned worktree, got %d: %v", len(report.PrunedWorktrees), report.PrunedWorktrees)
	}

	// Fresh must NOT be removed
	if _, statErr := os.Stat(freshWT); statErr != nil {
		t.Errorf("fresh worktree was unexpectedly removed: %v", statErr)
	}
	// Stale must BE removed
	if _, statErr := os.Stat(staleWT); !os.IsNotExist(statErr) {
		t.Errorf("stale worktree was not removed")
	}
}

func TestCollect_Boundary_EmptyDirectories(t *testing.T) {
	ctx := context.Background()
	tmpDir := t.TempDir()

	// Empty worktrees and ephemeral directories
	mkdirT(t, filepath.Join(tmpDir, ".standards", "worktrees"))
	mkdirT(t, filepath.Join(tmpDir, ".standards", "ephemeral"))

	opts := Options{
		RootDir:       tmpDir,
		DryRun:        false,
		SkipGitPrune:  true,
		SkipTestCache: true,
	}

	report, err := Collect(ctx, opts)
	if err != nil {
		t.Fatalf("Collect failed on empty directories: %v", err)
	}

	if report.ReclaimedBytes != 0 {
		t.Errorf("expected 0 reclaimed bytes on empty directories, got %d", report.ReclaimedBytes)
	}
	if len(report.PrunedWorktrees) != 0 {
		t.Errorf("expected 0 pruned worktrees, got %d", len(report.PrunedWorktrees))
	}
	if len(report.PurgedEphemeralFiles) != 0 {
		t.Errorf("expected 0 purged files, got %d", len(report.PurgedEphemeralFiles))
	}
}

func TestCollect_Boundary_HighVolumeEphemeralFiles(t *testing.T) {
	ctx := context.Background()
	tmpDir := t.TempDir()

	ephDir := mkdirT(t, filepath.Join(tmpDir, ".standards", "ephemeral"))

	const fileCount = 50
	released := make([]string, 0, fileCount)
	for i := 0; i < fileCount; i++ {
		name := fmt.Sprintf("trace-%03d.log", i)
		writeFileT(t, filepath.Join(ephDir, name), "trace payload log entry\n")
		ageTree(t, filepath.Join(ephDir, name), 48*time.Hour)
		released = append(released, filepath.Join(".standards", "ephemeral", name))
	}

	opts := Options{
		RootDir:       tmpDir,
		DryRun:        false,
		SkipGitPrune:  true,
		SkipTestCache: true,
		ReleasedPaths: released, MaxArtifactAge: 24 * time.Hour,
	}

	report, err := Collect(ctx, opts)
	if err != nil {
		t.Fatalf("Collect failed: %v", err)
	}

	if len(report.PurgedEphemeralFiles) != fileCount {
		t.Errorf("expected %d purged ephemeral files, got %d", fileCount, len(report.PurgedEphemeralFiles))
	}
	if report.ReclaimedBytes <= 0 {
		t.Errorf("expected positive reclaimed bytes, got %d", report.ReclaimedBytes)
	}

	entries, readErr := os.ReadDir(ephDir)
	if readErr != nil {
		t.Fatalf("failed to read ephemeral directory: %v", readErr)
	}
	if len(entries) != 0 {
		t.Errorf("expected ephemeral directory to be empty, found %d entries", len(entries))
	}
}

// =========================================================================
// Worktree deletion safety (3D: positive / negative / boundary)
// =========================================================================

// staleOptions returns Options that only exercise the worktree sweep.
func staleOptions(root string) Options {
	return Options{
		RootDir:        root,
		MaxWorktreeAge: 24 * time.Hour,
		DryRun:         false,
		SkipGitPrune:   true,
		SkipTestCache:  true,
	}
}

func TestCollect_Negative_KeepsWorktreeWithRecentNestedEdit(t *testing.T) {
	ctx := context.Background()
	tmpDir, activeWT := gcGitFixture(t)

	// The worktree root is old, but a file in a subdirectory was edited moments ago:
	// a directory's own mtime does not change when a nested file is written.
	nested := mkdirT(t, filepath.Join(activeWT, "src"))
	ageTree(t, activeWT, 72*time.Hour)
	writeFileT(t, filepath.Join(nested, "edited.go"), "package src\n")

	opts := staleOptions(tmpDir)
	opts.ReleasedPaths = []string{".standards/worktrees/released-old"}
	report, err := Collect(ctx, opts)
	if err != nil {
		t.Fatalf("Collect failed: %v", err)
	}
	if len(report.PrunedWorktrees) != 0 {
		t.Errorf("expected an actively edited worktree to survive, pruned: %v", report.PrunedWorktrees)
	}
	if _, statErr := os.Stat(activeWT); statErr != nil {
		t.Fatalf("actively edited worktree was deleted: %v", statErr)
	}
}

func TestCollect_Negative_SkipsUninspectableGitWorktree(t *testing.T) {
	ctx := context.Background()
	tmpDir := t.TempDir()
	wtDir := mkdirT(t, filepath.Join(tmpDir, ".standards", "worktrees"))

	// A stale directory that claims to be a git worktree but cannot be inspected must
	// fail closed: gc reports it as skipped instead of deleting it.
	brokenWT := mkdirT(t, filepath.Join(wtDir, "broken"))
	writeFileT(t, filepath.Join(brokenWT, ".git"), "gitdir: /nonexistent/admin/dir\n")
	writeFileT(t, filepath.Join(brokenWT, "work.txt"), "unsaved work\n")
	ageTree(t, brokenWT, 72*time.Hour)

	report, err := Collect(ctx, staleOptions(tmpDir))
	if err != nil {
		t.Fatalf("Collect failed: %v", err)
	}
	if len(report.PrunedWorktrees) != 0 {
		t.Errorf("expected no deletion of an uninspectable git worktree, pruned: %v", report.PrunedWorktrees)
	}
	if len(report.SkippedWorktrees) != 1 {
		t.Fatalf("expected exactly 1 skipped worktree, got %v", report.SkippedWorktrees)
	}
	if _, statErr := os.Stat(brokenWT); statErr != nil {
		t.Fatalf("uninspectable git worktree was deleted: %v", statErr)
	}
}

func TestCollect_Negative_SkipsDirtyGitWorktree(t *testing.T) {
	ctx := context.Background()
	tmpDir, dirtyWT := gcGitFixture(t)
	writeFileT(t, filepath.Join(dirtyWT, "unsaved.txt"), "work in progress\n")
	ageTree(t, dirtyWT, 72*time.Hour)

	opts := staleOptions(tmpDir)
	opts.ReleasedPaths = []string{".standards/worktrees/released-old"}
	report, err := Collect(ctx, opts)
	if err == nil {
		t.Fatalf("dirty released worktree must make collection incomplete")
	}
	if len(report.PrunedWorktrees) != 0 {
		t.Errorf("expected a dirty git worktree to survive, pruned: %v", report.PrunedWorktrees)
	}
	if len(report.SkippedWorktrees) != 1 {
		t.Fatalf("expected exactly 1 skipped worktree, got %v", report.SkippedWorktrees)
	}
	if _, statErr := os.Stat(filepath.Join(dirtyWT, "unsaved.txt")); statErr != nil {
		t.Fatalf("uncommitted work was deleted: %v", statErr)
	}
}

func TestCollect_Boundary_PrunesStaleTreeWithFreshUnrelatedSibling(t *testing.T) {
	ctx := context.Background()
	tmpDir, staleWT := gcGitFixture(t)
	wtDir := filepath.Dir(staleWT)
	writeFileT(t, filepath.Join(mkdirT(t, filepath.Join(staleWT, "deep")), "old.txt"), "old")
	runGCTestGit(t, staleWT, "-c", "user.name=gc-test", "-c", "user.email=gc@example.invalid", "add", "deep/old.txt")
	runGCTestGit(t, staleWT, "-c", "user.name=gc-test", "-c", "user.email=gc@example.invalid", "commit", "-q", "-m", "fixture")
	ageTree(t, staleWT, 25*time.Hour)

	freshWT := mkdirT(t, filepath.Join(wtDir, "fresh"))
	writeFileT(t, filepath.Join(freshWT, "new.txt"), "new")

	opts := staleOptions(tmpDir)
	opts.ReleasedPaths = []string{".standards/worktrees/released-old"}
	report, err := Collect(ctx, opts)
	if err != nil {
		t.Fatalf("Collect failed: %v", err)
	}
	if len(report.PrunedWorktrees) != 1 {
		t.Fatalf("expected exactly 1 pruned worktree, got %v", report.PrunedWorktrees)
	}
	if _, statErr := os.Stat(staleWT); !os.IsNotExist(statErr) {
		t.Errorf("stale worktree was not removed")
	}
	if _, statErr := os.Stat(freshWT); statErr != nil {
		t.Errorf("fresh worktree was unexpectedly removed: %v", statErr)
	}
}
