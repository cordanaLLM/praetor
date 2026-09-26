package gc

import (
	"context"
	"fmt"
	"io/fs"
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
//
// Symlinks are skipped. os.Chtimes follows them, so a link inside a fixture — the nested
// symlink case in gc_bounds_test.go plants one on purpose — used to backdate its target,
// which lies outside the fixture and on a developer's machine is an arbitrary file. A test
// helper may not write outside the tree it was handed.
func ageTree(t *testing.T, root string, age time.Duration) {
	t.Helper()
	stamp := time.Now().Add(-age)
	info, err := os.Lstat(root)
	if err != nil {
		t.Fatalf("failed to stat %s: %v", root, err)
	}
	if info.Mode()&fs.ModeSymlink != 0 {
		t.Fatalf("ageTree refuses a symlink root: backdating %s would write to its target", root)
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
			if entry.Type()&fs.ModeSymlink != 0 {
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

// =========================================================================
// Pool presence, default root and the Errors channel (BUG-959, BUG-232)
// =========================================================================

// TestCollect_Negative_MistypedWorktreesDirIsAnError pins BUG-959: a configured pool that does
// not exist fails collection instead of producing a clean, empty report.
func TestCollect_Negative_MistypedWorktreesDirIsAnError(t *testing.T) {
	root := t.TempDir()
	mkdirT(t, filepath.Join(root, ".standards", "worktrees"))
	report, err := Collect(context.Background(), Options{RootDir: root, WorktreesDir: ".standards/worktress", DryRun: true})
	want := "configured collection pool " + filepath.Join(".standards", "worktress") + " does not exist"
	if err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("mistyped --worktrees-dir was accepted: err=%v", err)
	}
	if report == nil || report.Complete || len(report.Errors) != 1 || report.Errors[0] != err.Error() {
		t.Fatalf("failure must reach report.Errors exactly once: %+v", report)
	}
}

// TestCollect_Boundary_AbsentDefaultPoolsAreListed: default pools a repository never created
// are named in the report rather than silently passed over, and do not fail collection.
func TestCollect_Boundary_AbsentDefaultPoolsAreListed(t *testing.T) {
	root := t.TempDir()
	mkdirT(t, filepath.Join(root, ".standards", "ephemeral"))
	report, err := Collect(context.Background(), Options{RootDir: root, DryRun: true})
	if err != nil || !report.Complete {
		t.Fatalf("absent default pools must not fail collection: %v %+v", err, report)
	}
	want := []string{filepath.Join(".standards", "worktrees"), filepath.Join(".standards", "tmp")}
	if strings.Join(report.MissingPools, ",") != strings.Join(want, ",") {
		t.Fatalf("missing pools = %v, want %v", report.MissingPools, want)
	}
}

// TestCollect_Positive_ConfiguredPoolThatExistsIsCollected: an explicit pool that exists is
// planned from and is not listed as missing.
func TestCollect_Positive_ConfiguredPoolThatExistsIsCollected(t *testing.T) {
	root := t.TempDir()
	artifact := writeFileT(t, filepath.Join(mkdirT(t, filepath.Join(root, "scratch")), "run-1"), "payload")
	ageTree(t, artifact, 48*time.Hour)
	report, err := Collect(context.Background(), Options{RootDir: root, EphemeralDir: "scratch", DryRun: true,
		ReleasedPaths: []string{filepath.Join("scratch", "run-1")}})
	if err != nil {
		t.Fatalf("Collect failed: %v", err)
	}
	if len(report.PlannedArtifacts) != 1 || strings.Contains(strings.Join(report.MissingPools, ","), "scratch") {
		t.Fatalf("configured pool was not collected from: %+v", report)
	}
}

// TestCollect_Boundary_EmptyRootDirDefaultsToWorkingDirectory pins the RootDir "." default:
// an empty RootDir collects from the process working directory, with root-relative paths.
func TestCollect_Boundary_EmptyRootDirDefaultsToWorkingDirectory(t *testing.T) {
	root := t.TempDir()
	artifact := writeFileT(t, filepath.Join(mkdirT(t, filepath.Join(root, ".standards", "ephemeral")), "trace.log"), "trace")
	ageTree(t, artifact, 48*time.Hour)
	t.Chdir(root)
	released := filepath.Join(".standards", "ephemeral", "trace.log")
	report, err := Collect(context.Background(), Options{DryRun: true, ReleasedPaths: []string{released}})
	if err != nil {
		t.Fatalf("Collect with the default root failed: %v", err)
	}
	if len(report.PlannedArtifacts) != 1 || report.PlannedArtifacts[0] != released {
		t.Fatalf("default root planned %v, want [%s]", report.PlannedArtifacts, released)
	}
}

// TestNewestModification_Positive_SeesNestedEdits: the newest timestamp comes from anywhere in
// the tree, so a nested edit keeps an otherwise old tree fresh.
func TestNewestModification_Positive_SeesNestedEdits(t *testing.T) {
	root := t.TempDir()
	nested := writeFileT(t, filepath.Join(mkdirT(t, filepath.Join(root, "src", "pkg")), "main.go"), "package main")
	ageTree(t, root, 72*time.Hour)
	fresh := time.Now().Add(-time.Minute)
	if err := os.Chtimes(nested, fresh, fresh); err != nil {
		t.Fatal(err)
	}
	newest, err := NewestModification(context.Background(), root, time.Time{})
	if err != nil || newest.Before(fresh.Add(-2*time.Second)) {
		t.Fatalf("newest = %v (err %v), want the nested edit at %v", newest, err, fresh)
	}
	// A cutoff stops at the first entry at or after it and still answers "not older".
	cutoff := time.Now().Add(-24 * time.Hour)
	early, err := NewestModification(context.Background(), root, cutoff)
	if err != nil || early.Before(cutoff) {
		t.Fatalf("cutoff walk = %v (err %v), want a time at or after %v", early, err, cutoff)
	}
}

// TestNewestModification_Negative_RejectsMissingDirAndNilContext: every failure is an error,
// never a zero or partial time.
func TestNewestModification_Negative_RejectsMissingDirAndNilContext(t *testing.T) {
	if _, err := NewestModification(context.Background(), filepath.Join(t.TempDir(), "absent"), time.Time{}); err == nil {
		t.Fatal("a missing directory must be an error, not a zero time")
	}
	var nilCtx context.Context
	if _, err := NewestModification(nilCtx, t.TempDir(), time.Time{}); err == nil {
		t.Fatal("a nil context must be rejected")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := NewestModification(ctx, t.TempDir(), time.Time{}); err == nil {
		t.Fatal("a cancelled walk must be an error, not a partial answer")
	}
}

// TestWalkTree_Boundary_MeasuresSymlinksWithoutFollowing: a measuring walk counts a symlink as
// one entry and never descends through it, where the collecting walk refuses it outright.
func TestWalkTree_Boundary_MeasuresSymlinksWithoutFollowing(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	for i := 0; i < 5; i++ {
		writeFileT(t, filepath.Join(outside, fmt.Sprintf("file-%d", i)), "outside")
	}
	writeFileT(t, filepath.Join(root, "kept.txt"), "kept")
	if err := os.Symlink(outside, filepath.Join(root, "link")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	pinned, err := os.OpenRoot(root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if closeErr := pinned.Close(); closeErr != nil {
			t.Errorf("close pinned root: %v", closeErr)
		}
	})
	stats, _, err := walkTree(context.Background(), pinned, ".", walkRules{})
	if err != nil || stats.Entries != 3 {
		t.Fatalf("measuring walk: entries=%d err=%v, want root, kept.txt and the link itself", stats.Entries, err)
	}
	if _, _, err := scanTree(context.Background(), pinned, ".", false); err == nil {
		t.Fatal("the collecting walk must still refuse a symlink")
	}
}
