package gc

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// Invariant configurations and scalar loop bounds.
const (
	DefaultMaxWorktreeAge = 24 * time.Hour
	MaxDirectoryTraversal = 10000
	MaxFileScanLimit      = 50000
	MaxEntriesLimit       = 2000
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

	args := []string{"-C", opts.RootDir, "worktree", "prune"}
	if opts.DryRun {
		args = append(args, "--dry-run", "-v")
	} else {
		args = append(args, "-v")
	}

	cmd := exec.CommandContext(ctx, "git", args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		report.Errors = append(report.Errors, fmt.Sprintf("git worktree prune: %v", err))
		return
	}

	lines := strings.Split(string(out), "\n")
	const maxLines = 1000
	limit := len(lines)
	if limit > maxLines {
		limit = maxLines
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
		entry := entries[i]
		entryPath := filepath.Join(opts.WorktreesDir, entry.Name())
		info, err := entry.Info()
		if err != nil {
			report.Errors = append(report.Errors, fmt.Sprintf("stat worktree %s: %v", entry.Name(), err))
			continue
		}

		if time.Since(info.ModTime()) > opts.MaxWorktreeAge {
			size, _ := calculateDirSize(entryPath)
			if !opts.DryRun {
				if remErr := os.RemoveAll(entryPath); remErr != nil {
					report.Errors = append(report.Errors, fmt.Sprintf("remove worktree %s: %v", entry.Name(), remErr))
					continue
				}
			}
			report.ReclaimedBytes += size
			report.PrunedWorktrees = append(report.PrunedWorktrees, entryPath)
		}
	}
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

		size, _ := calculateDirSize(filePath)
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
	} else {
		cmd := exec.CommandContext(ctx, "go", "clean", "-testcache")
		cmd.Dir = opts.RootDir
		if err := cmd.Run(); err != nil {
			report.Errors = append(report.Errors, fmt.Sprintf("go clean -testcache: %v", err))
		} else {
			report.CleanedCacheArtifacts = append(report.CleanedCacheArtifacts, "go testcache")
		}
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
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		limit := len(entries)
		if limit > MaxEntriesLimit {
			limit = MaxEntriesLimit
		}

		for i := 0; i < limit; i++ {
			entry := entries[i]
			if entry.IsDir() {
				continue
			}
			name := entry.Name()
			if isTempBinary(name) {
				p := filepath.Join(dir, name)
				size, _ := calculateDirSize(p)
				if !opts.DryRun {
					_ = os.Remove(p)
				}
				report.ReclaimedBytes += size
				report.CleanedCacheArtifacts = append(report.CleanedCacheArtifacts, p)
			}
		}
	}
}

// isTempBinary returns true for test executables and debug compiler artifacts.
func isTempBinary(name string) bool {
	return strings.HasSuffix(name, ".test") ||
		strings.HasSuffix(name, ".tmp") ||
		strings.HasPrefix(name, "__debug_bin") ||
		strings.HasPrefix(name, "tmp.")
}

// calculateDirSize iteratively sums file sizes in a directory tree without recursion.
func calculateDirSize(root string) (int64, error) {
	info, err := os.Lstat(root)
	if err != nil {
		return 0, fmt.Errorf("lstat %s: %w", root, err)
	}
	if !info.IsDir() {
		return info.Size(), nil
	}

	var totalSize int64
	queue := []string{root}
	dirsProcessed := 0
	filesProcessed := 0

	for len(queue) > 0 && dirsProcessed < MaxDirectoryTraversal {
		dirsProcessed++
		curr := queue[0]
		queue = queue[1:]

		entries, err := os.ReadDir(curr)
		if err != nil {
			continue
		}

		for _, entry := range entries {
			filesProcessed++
			if filesProcessed >= MaxFileScanLimit {
				break
			}
			fullPath := filepath.Join(curr, entry.Name())
			if entry.IsDir() {
				queue = append(queue, fullPath)
			} else {
				eInfo, eErr := entry.Info()
				if eErr == nil {
					totalSize += eInfo.Size()
				}
			}
		}
	}
	return totalSize, nil
}
