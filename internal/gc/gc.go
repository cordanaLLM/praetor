package gc

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/cordanaLLM/praetor/internal/util"
)

// Invariant configurations and scalar loop bounds.
const (
	DefaultMaxWorktreeAge = 24 * time.Hour
	MaxDirectoryTraversal = 10000
	MaxFileScanLimit      = 50000
	MaxEntriesLimit       = 2000
	// MaxPorcelainLines bounds the git porcelain output parsed per invocation (HISS-02).
	MaxPorcelainLines = 1000
)

// Options specifies workstation garbage collection operational parameters.
type Options struct {
	RootDir        string        `json:"root_dir"`
	MaxWorktreeAge time.Duration `json:"max_worktree_age"`
	DryRun         bool          `json:"dry_run"`
	EphemeralDir   string        `json:"ephemeral_dir"`
	WorktreesDir   string        `json:"worktrees_dir"`
	SkipGitPrune   bool          `json:"skip_git_prune"`
	SkipTestCache  bool          `json:"skip_test_cache"`
}

// GCReport records actions performed, bytes reclaimed, and diagnostics.
type GCReport struct {
	DryRun                bool     `json:"dry_run"`
	ReclaimedBytes        int64    `json:"reclaimed_bytes"`
	PrunedWorktrees       []string `json:"pruned_worktrees"`
	SkippedWorktrees      []string `json:"skipped_worktrees,omitempty"`
	PurgedEphemeralFiles  []string `json:"purged_ephemeral_files"`
	CleanedCacheArtifacts []string `json:"cleaned_cache_artifacts"`
	Errors                []string `json:"errors,omitempty"`
}

// Collect executes workstation garbage collection respecting timeouts and dry-run preferences.
func Collect(ctx context.Context, opts Options) (*GCReport, error) {
	if ctx == nil {
		return nil, fmt.Errorf("gc: context cannot be nil")
	}
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("gc cancelled: %w", err)
	}

	normOpts, err := normalizeOptions(opts)
	if err != nil {
		return nil, fmt.Errorf("gc: invalid options: %w", err)
	}

	report := &GCReport{
		DryRun:                normOpts.DryRun,
		PrunedWorktrees:       make([]string, 0),
		SkippedWorktrees:      make([]string, 0),
		PurgedEphemeralFiles:  make([]string, 0),
		CleanedCacheArtifacts: make([]string, 0),
		Errors:                make([]string, 0),
	}

	if !normOpts.SkipGitPrune {
		pruneGitWorktrees(ctx, normOpts, report)
	}

	cleanStaleWorktrees(ctx, normOpts, report)
	purgeEphemeralFiles(ctx, normOpts, report)

	if !normOpts.SkipTestCache {
		trimTestCachesAndBinaries(ctx, normOpts, report)
	}

	return report, nil
}

// normalizeOptions sets baseline defaults and resolves absolute paths.
func normalizeOptions(opts Options) (Options, error) {
	if opts.RootDir == "" {
		opts.RootDir = "."
	}
	absRoot, err := filepath.Abs(opts.RootDir)
	if err != nil {
		return opts, fmt.Errorf("resolve root dir %q: %w", opts.RootDir, err)
	}
	if _, err := os.Stat(absRoot); err != nil {
		return opts, fmt.Errorf("root dir %q does not exist: %w", absRoot, err)
	}
	opts.RootDir = absRoot

	if opts.MaxWorktreeAge <= 0 {
		opts.MaxWorktreeAge = DefaultMaxWorktreeAge
	}
	if opts.WorktreesDir == "" {
		opts.WorktreesDir = filepath.Join(opts.RootDir, ".standards", "worktrees")
	}
	if opts.EphemeralDir == "" {
		opts.EphemeralDir = filepath.Join(opts.RootDir, ".standards", "ephemeral")
	}
	return opts, nil
}

// pruneGitWorktrees invokes git worktree prune on repository workspaces.
func pruneGitWorktrees(ctx context.Context, opts Options, report *GCReport) {
	if err := ctx.Err(); err != nil {
		report.Errors = append(report.Errors, err.Error())
		return
	}

	gitDir := filepath.Join(opts.RootDir, ".git")
	if _, err := os.Stat(gitDir); err != nil {
		return
	}

	args := []string{"worktree", "prune", "-v"}
	if opts.DryRun {
		args = append(args, "--dry-run")
	}

	out, err := util.RunGit(ctx, opts.RootDir, args...)
	if err != nil {
		report.Errors = append(report.Errors, fmt.Sprintf("git worktree prune: %v", err))
		return
	}

	lines := strings.Split(out, "\n")
	limit := len(lines)
	if limit > MaxPorcelainLines {
		limit = MaxPorcelainLines
	}
	for i := 0; i < limit; i++ {
		line := strings.TrimSpace(lines[i])
		if line != "" {
			report.PrunedWorktrees = append(report.PrunedWorktrees, line)
		}
	}
}

// cleanStaleWorktrees removes ephemeral worktrees exceeding MaxWorktreeAge.
func cleanStaleWorktrees(ctx context.Context, opts Options, report *GCReport) {
	if err := ctx.Err(); err != nil {
		report.Errors = append(report.Errors, err.Error())
		return
	}

	entries, err := os.ReadDir(opts.WorktreesDir)
	if err != nil {
		return
	}

	limit := len(entries)
	if limit > MaxEntriesLimit {
		limit = MaxEntriesLimit
	}

	for i := 0; i < limit; i++ {
		pruneStaleWorktree(ctx, opts, report, filepath.Join(opts.WorktreesDir, entries[i].Name()))
	}
}

// pruneStaleWorktree deletes one candidate worktree when it is both stale and safe to
// remove.
//
// Age is measured from the newest modification time anywhere in the tree, not from the
// top-level directory's own mtime: editing a file in a subdirectory never touches the
// worktree root, so the old top-level check deleted worktrees that were still in daily
// use. Before any removal the candidate must also prove it holds no unsaved work: a
// directory that git manages is skipped unless `git status --porcelain` is empty and git
// does not report it as locked, and any git failure is fail-closed (skip, never delete).
func pruneStaleWorktree(ctx context.Context, opts Options, report *GCReport, path string) {
	stats, err := scanDir(path)
	if err != nil {
		report.Errors = append(report.Errors, fmt.Sprintf("scan worktree %s: %v", filepath.Base(path), err))
		return
	}
	if time.Since(stats.NewestMod) <= opts.MaxWorktreeAge {
		return
	}
	if reason := worktreeSafety(ctx, path); reason != "" {
		report.SkippedWorktrees = append(report.SkippedWorktrees, fmt.Sprintf("%s: %s", path, reason))
		return
	}
	if !opts.DryRun {
		if remErr := os.RemoveAll(path); remErr != nil {
			report.Errors = append(report.Errors, fmt.Sprintf("remove worktree %s: %v", filepath.Base(path), remErr))
			return
		}
	}
	report.ReclaimedBytes += stats.TotalSize
	report.PrunedWorktrees = append(report.PrunedWorktrees, path)
}

// worktreeSafety returns the reason a directory must not be deleted, or "" when deleting
// it cannot lose work. Directories that git does not manage carry no reason.
func worktreeSafety(ctx context.Context, path string) string {
	if !util.PathExists(filepath.Join(path, ".git")) {
		return ""
	}
	status, err := util.RunGit(ctx, path, "status", "--porcelain")
	if err != nil {
		return fmt.Sprintf("git status failed (%v); refusing to delete a git worktree that cannot be inspected", err)
	}
	if strings.TrimSpace(status) != "" {
		return "uncommitted or untracked changes present"
	}
	locked, lockErr := worktreeLocked(ctx, path)
	if lockErr != nil {
		return fmt.Sprintf("git worktree list failed (%v); refusing to delete a git worktree that cannot be inspected", lockErr)
	}
	if locked {
		return "worktree is locked"
	}
	return ""
}

// worktreeLocked reports whether git registers path as a locked worktree.
func worktreeLocked(ctx context.Context, path string) (bool, error) {
	out, err := util.RunGit(ctx, path, "worktree", "list", "--porcelain")
	if err != nil {
		return false, err
	}
	target, absErr := filepath.Abs(path)
	if absErr != nil {
		return false, fmt.Errorf("resolve %s: %w", path, absErr)
	}

	lines := strings.Split(out, "\n")
	limit := len(lines)
	if limit > MaxPorcelainLines {
		limit = MaxPorcelainLines
	}
	inTarget := false
	for i := 0; i < limit; i++ {
		line := strings.TrimSpace(lines[i])
		if rest, ok := strings.CutPrefix(line, "worktree "); ok {
			inTarget = filepath.Clean(rest) == filepath.Clean(target)
			continue
		}
		if inTarget && (line == "locked" || strings.HasPrefix(line, "locked ")) {
			return true, nil
		}
	}
	return false, nil
}

// purgeEphemeralFiles deletes SARIF logs, traces, and temporary cache dumps.
func purgeEphemeralFiles(ctx context.Context, opts Options, report *GCReport) {
	if err := ctx.Err(); err != nil {
		report.Errors = append(report.Errors, err.Error())
		return
	}

	entries, err := os.ReadDir(opts.EphemeralDir)
	if err != nil {
		return
	}

	limit := len(entries)
	if limit > MaxEntriesLimit {
		limit = MaxEntriesLimit
	}

	for i := 0; i < limit; i++ {
		entry := entries[i]
		filePath := filepath.Join(opts.EphemeralDir, entry.Name())

		size, sizeErr := calculateDirSize(filePath)
		if sizeErr != nil {
			report.Errors = append(report.Errors, fmt.Sprintf("size ephemeral %s: %v", entry.Name(), sizeErr))
		}
		if !opts.DryRun {
			if remErr := os.RemoveAll(filePath); remErr != nil {
				report.Errors = append(report.Errors, fmt.Sprintf("remove ephemeral %s: %v", entry.Name(), remErr))
				continue
			}
		}
		report.ReclaimedBytes += size
		report.PurgedEphemeralFiles = append(report.PurgedEphemeralFiles, filePath)
	}
}

// trimTestCachesAndBinaries flushes test cache and prunes temporary compiler artifacts.
func trimTestCachesAndBinaries(ctx context.Context, opts Options, report *GCReport) {
	if err := ctx.Err(); err != nil {
		report.Errors = append(report.Errors, err.Error())
		return
	}

	if opts.DryRun {
		report.CleanedCacheArtifacts = append(report.CleanedCacheArtifacts, "go testcache (simulated)")
	} else if _, err := util.RunCommand(ctx, opts.RootDir, "go", "clean", "-testcache"); err != nil {
		report.Errors = append(report.Errors, fmt.Sprintf("go clean -testcache: %v", err))
	} else {
		report.CleanedCacheArtifacts = append(report.CleanedCacheArtifacts, "go testcache")
	}

	cleanTempBinaries(opts, report)
}

// cleanTempBinaries scans directories for ephemeral test binaries and debug dumps.
func cleanTempBinaries(opts Options, report *GCReport) {
	targetDirs := []string{
		filepath.Join(opts.RootDir, ".standards", "tmp"),
		filepath.Join(opts.RootDir, "bin"),
		opts.RootDir,
	}

	for _, dir := range targetDirs {
		cleanTempBinariesIn(dir, opts, report)
	}
}

// cleanTempBinariesIn removes the temporary binaries of a single directory.
func cleanTempBinariesIn(dir string, opts Options, report *GCReport) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	limit := len(entries)
	if limit > MaxEntriesLimit {
		limit = MaxEntriesLimit
	}

	for i := 0; i < limit; i++ {
		entry := entries[i]
		if entry.IsDir() || !isTempBinary(entry.Name()) {
			continue
		}
		removeTempBinary(filepath.Join(dir, entry.Name()), entry.Name(), opts, report)
	}
}

// removeTempBinary deletes one temporary binary and records the reclaimed bytes.
func removeTempBinary(path, name string, opts Options, report *GCReport) {
	size, sizeErr := calculateDirSize(path)
	if sizeErr != nil {
		report.Errors = append(report.Errors, fmt.Sprintf("size binary %s: %v", name, sizeErr))
	}
	if !opts.DryRun {
		if remErr := os.Remove(path); remErr != nil {
			report.Errors = append(report.Errors, fmt.Sprintf("remove binary %s: %v", name, remErr))
			return
		}
	}
	report.ReclaimedBytes += size
	report.CleanedCacheArtifacts = append(report.CleanedCacheArtifacts, path)
}

// isTempBinary returns true for test executables and debug compiler artifacts.
func isTempBinary(name string) bool {
	return strings.HasSuffix(name, ".test") ||
		strings.HasSuffix(name, ".tmp") ||
		strings.HasPrefix(name, "__debug_bin") ||
		strings.HasPrefix(name, "tmp.")
}

// dirStats aggregates the total size and the newest modification time of a tree.
type dirStats struct {
	TotalSize int64
	NewestMod time.Time
}

// calculateDirSize iteratively sums file sizes in a directory tree without recursion.
func calculateDirSize(root string) (int64, error) {
	stats, err := scanDir(root)
	return stats.TotalSize, err
}

// scanDir walks root breadth-first (no recursion, HISS-01; bounded by
// MaxDirectoryTraversal and MaxFileScanLimit, HISS-02) and returns the total size of the
// tree together with the newest modification time found anywhere inside it.
func scanDir(root string) (dirStats, error) {
	info, err := os.Lstat(root)
	if err != nil {
		return dirStats{}, fmt.Errorf("lstat %s: %w", root, err)
	}
	stats := dirStats{NewestMod: info.ModTime()}
	if !info.IsDir() {
		stats.TotalSize = info.Size()
		return stats, nil
	}

	queue := []string{root}
	filesProcessed := 0
	for i := 0; i < MaxDirectoryTraversal && len(queue) > 0; i++ {
		curr := queue[0]
		queue = queue[1:]

		entries, readErr := os.ReadDir(curr)
		if readErr != nil {
			continue
		}
		filesProcessed += foldEntries(curr, entries, filesProcessed, &stats, &queue)
	}
	return stats, nil
}

// foldEntries folds one directory listing into stats, queues the subdirectories it finds
// and returns how many entries it consumed before hitting MaxFileScanLimit.
func foldEntries(dir string, entries []os.DirEntry, processed int, stats *dirStats, queue *[]string) int {
	consumed := 0
	for _, entry := range entries {
		if processed+consumed >= MaxFileScanLimit {
			break
		}
		consumed++
		info, err := entry.Info()
		if err != nil {
			continue
		}
		if info.ModTime().After(stats.NewestMod) {
			stats.NewestMod = info.ModTime()
		}
		if entry.IsDir() {
			*queue = append(*queue, filepath.Join(dir, entry.Name()))
			continue
		}
		stats.TotalSize += info.Size()
	}
	return consumed
}
