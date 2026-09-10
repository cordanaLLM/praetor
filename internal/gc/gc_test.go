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

func TestCollect_Positive_PruneStaleAndEphemeral(t *testing.T) {
	ctx := context.Background()
	tmpDir := t.TempDir()

	// Setup directories
	wtDir := filepath.Join(tmpDir, ".standards", "worktrees")
	ephDir := filepath.Join(tmpDir, ".standards", "ephemeral")
	if err := os.MkdirAll(wtDir, 0o755); err != nil {
		t.Fatalf("failed to create worktree dir: %v", err)
	}
	if err := os.MkdirAll(ephDir, 0o755); err != nil {
		t.Fatalf("failed to create ephemeral dir: %v", err)
	}

	// 1. Stale worktree (48 hours old)
	staleWT := filepath.Join(wtDir, "agent-branch-old")
	if err := os.MkdirAll(staleWT, 0o755); err != nil {
		t.Fatalf("failed to create stale worktree: %v", err)
	}
	staleFile := filepath.Join(staleWT, "code.go")
	if err := os.WriteFile(staleFile, []byte("package main\n"), 0o644); err != nil {
		t.Fatalf("failed to write stale file: %v", err)
	}
	oldTime := time.Now().Add(-48 * time.Hour)
	if err := os.Chtimes(staleWT, oldTime, oldTime); err != nil {
		t.Fatalf("failed to change worktree time: %v", err)
	}

	// 2. Ephemeral diagnostic SARIF
	sarifFile := filepath.Join(ephDir, "diagnostics.sarif")
	if err := os.WriteFile(sarifFile, []byte(`{"version":"2.1.0","runs":[]}`), 0o644); err != nil {
		t.Fatalf("failed to write sarif file: %v", err)
	}

	opts := Options{
		RootDir:        tmpDir,
		MaxWorktreeAge: 24 * time.Hour,
		DryRun:         false,
		SkipGitPrune:   true,
		SkipTestCache:  true,
	}

	report, err := Collect(ctx, opts)
	if err != nil {
		t.Fatalf("Collect failed: %v", err)
	}
	if report == nil {
		t.Fatalf("expected non-nil report")
	}

	// Check assertions
	if report.ReclaimedBytes <= 0 {
		t.Errorf("expected ReclaimedBytes > 0, got %d", report.ReclaimedBytes)
	}
	if len(report.PrunedWorktrees) != 1 {
		t.Errorf("expected 1 pruned worktree, got %d", len(report.PrunedWorktrees))
	}
	if len(report.PurgedEphemeralFiles) != 1 {
		t.Errorf("expected 1 purged ephemeral file, got %d", len(report.PurgedEphemeralFiles))
	}

	// Verify real disk removal
	if _, statErr := os.Stat(staleWT); !os.IsNotExist(statErr) {
		t.Errorf("expected stale worktree to be removed from disk, but still exists")
	}
	if _, statErr := os.Stat(sarifFile); !os.IsNotExist(statErr) {
		t.Errorf("expected sarif file to be removed from disk, but still exists")
	}
}

func TestCollect_Positive_DryRunSimulation(t *testing.T) {
	ctx := context.Background()
	tmpDir := t.TempDir()

	wtDir := filepath.Join(tmpDir, ".standards", "worktrees")
	ephDir := filepath.Join(tmpDir, ".standards", "ephemeral")
	_ = os.MkdirAll(wtDir, 0o755)
	_ = os.MkdirAll(ephDir, 0o755)

	staleWT := filepath.Join(wtDir, "dry-run-wt")
	_ = os.MkdirAll(staleWT, 0o755)
	_ = os.WriteFile(filepath.Join(staleWT, "dummy.txt"), []byte("payload"), 0o644)
	oldTime := time.Now().Add(-36 * time.Hour)
	_ = os.Chtimes(staleWT, oldTime, oldTime)

	sarifFile := filepath.Join(ephDir, "dry-run.sarif")
	_ = os.WriteFile(sarifFile, []byte("sarif-data"), 0o644)

	opts := Options{
		RootDir:        tmpDir,
		MaxWorktreeAge: 24 * time.Hour,
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
	if report.ReclaimedBytes <= 0 {
		t.Errorf("expected simulated ReclaimedBytes > 0, got %d", report.ReclaimedBytes)
	}
	if len(report.PrunedWorktrees) != 1 {
		t.Errorf("expected 1 simulated pruned worktree, got %d", len(report.PrunedWorktrees))
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

	binDir := filepath.Join(tmpDir, "bin")
	tmpSubDir := filepath.Join(tmpDir, ".standards", "tmp")
	_ = os.MkdirAll(binDir, 0o755)
	_ = os.MkdirAll(tmpSubDir, 0o755)

	testBinary := filepath.Join(binDir, "package.test")
	_ = os.WriteFile(testBinary, []byte("binary data"), 0o755)

	tempFile := filepath.Join(tmpSubDir, "scratch.tmp")
	_ = os.WriteFile(tempFile, []byte("temp data"), 0o644)

	opts := Options{
		RootDir:       tmpDir,
		DryRun:        false,
		SkipGitPrune:  true,
		SkipTestCache: false,
	}

	report, err := Collect(ctx, opts)
	if err != nil {
		t.Fatalf("Collect failed: %v", err)
	}

	if len(report.CleanedCacheArtifacts) < 2 {
		t.Errorf("expected at least 2 cleaned cache artifacts, got %d: %v",
			len(report.CleanedCacheArtifacts), report.CleanedCacheArtifacts)
	}

	if _, statErr := os.Stat(testBinary); !os.IsNotExist(statErr) {
		t.Errorf("expected temporary test binary to be deleted")
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
	if report != nil {
		t.Errorf("expected nil report on cancelled context, got %v", report)
	}
	if !strings.Contains(err.Error(), "context") && !strings.Contains(err.Error(), "cancelled") {
		t.Errorf("expected cancellation message, got %v", err)
	}
}

func TestCollect_Negative_NilContext(t *testing.T) {
	opts := Options{
		RootDir: t.TempDir(),
	}

	report, err := Collect(nil, opts)
	if err == nil {
		t.Errorf("expected error when context is nil, got nil")
	}
	if report != nil {
		t.Errorf("expected nil report for nil context, got %v", report)
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
	if report != nil {
		t.Errorf("expected nil report for invalid root dir, got %v", report)
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
	tmpDir := t.TempDir()

	wtDir := filepath.Join(tmpDir, ".standards", "worktrees")
	_ = os.MkdirAll(wtDir, 0o755)

	// Fresh worktree: 1 hour old (below 24h boundary)
	freshWT := filepath.Join(wtDir, "fresh-worktree")
	_ = os.MkdirAll(freshWT, 0o755)
	_ = os.WriteFile(filepath.Join(freshWT, "fresh.txt"), []byte("fresh"), 0o644)
	freshTime := time.Now().Add(-1 * time.Hour)
	_ = os.Chtimes(freshWT, freshTime, freshTime)

	// Stale worktree: 25 hours old (above 24h boundary)
	staleWT := filepath.Join(wtDir, "stale-worktree")
	_ = os.MkdirAll(staleWT, 0o755)
	_ = os.WriteFile(filepath.Join(staleWT, "stale.txt"), []byte("stale"), 0o644)
	staleTime := time.Now().Add(-25 * time.Hour)
	_ = os.Chtimes(staleWT, staleTime, staleTime)

	opts := Options{
		RootDir:        tmpDir,
		MaxWorktreeAge: 24 * time.Hour,
		DryRun:         false,
		SkipGitPrune:   true,
		SkipTestCache:  true,
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
	_ = os.MkdirAll(filepath.Join(tmpDir, ".standards", "worktrees"), 0o755)
	_ = os.MkdirAll(filepath.Join(tmpDir, ".standards", "ephemeral"), 0o755)

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

	ephDir := filepath.Join(tmpDir, ".standards", "ephemeral")
	_ = os.MkdirAll(ephDir, 0o755)

	const fileCount = 50
	for i := 0; i < fileCount; i++ {
		p := filepath.Join(ephDir, fmt.Sprintf("trace-%03d.log", i))
		_ = os.WriteFile(p, []byte("trace payload log entry\n"), 0o644)
	}

	opts := Options{
		RootDir:       tmpDir,
		DryRun:        false,
		SkipGitPrune:  true,
		SkipTestCache: true,
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

	entries, _ := os.ReadDir(ephDir)
	if len(entries) != 0 {
		t.Errorf("expected ephemeral directory to be empty, found %d entries", len(entries))
	}
}
