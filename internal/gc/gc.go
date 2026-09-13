// Package gc collects explicitly released resources; age alone never grants
// deletion authority. Callers must keep released resources quiescent throughout
// collection. Cross-process session leases are a separate lifecycle concern.
package gc

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/cordanaLLM/praetor/internal/util"
	"github.com/cordanaLLM/praetor/internal/worktree"
)

const (
	DefaultMaxWorktreeAge = 24 * time.Hour
	DefaultMaxArtifactAge = 24 * time.Hour
	MaxDirectoryTraversal = 10000
	MaxFileScanLimit      = 50000
	MaxEntriesLimit       = 2000
)

// Options specifies a confined collection scope. ReleasedPaths are exact direct
// children of the configured pools, relative to RootDir (or absolute within it).
// Listing a path asserts that its owner released it and will not write to it.
type Options struct {
	RootDir        string        `json:"root_dir"`
	MaxWorktreeAge time.Duration `json:"max_worktree_age"`
	MaxArtifactAge time.Duration `json:"max_artifact_age"`
	DryRun         bool          `json:"dry_run"`
	EphemeralDir   string        `json:"ephemeral_dir"`
	WorktreesDir   string        `json:"worktrees_dir"`
	ReleasedPaths  []string      `json:"released_paths,omitempty"`
	// SkipGitPrune is retained for compatibility. Administrative Git pruning is
	// now always separate: it cannot be scoped to released resources.
	SkipGitPrune     bool `json:"skip_git_prune"`
	SkipTestCache    bool `json:"skip_test_cache"`
	CleanGoTestCache bool `json:"clean_go_test_cache"`
}

// GCReport separates the validated plan from completed actions. Complete means
// the requested scan/apply finished, including reporting protected resources.
// Reclaimed byte counts describe file lengths, not filesystem allocation savings.
type GCReport struct {
	DryRun                bool     `json:"dry_run"`
	Complete              bool     `json:"complete"`
	PlannedReclaimedBytes int64    `json:"planned_reclaimed_bytes"`
	PlannedWorktrees      []string `json:"planned_worktrees"`
	PlannedArtifacts      []string `json:"planned_artifacts"`
	PlannedCacheCleanup   bool     `json:"planned_cache_cleanup"`
	ReclaimedBytes        int64    `json:"reclaimed_bytes"`
	PrunedWorktrees       []string `json:"pruned_worktrees"`
	SkippedWorktrees      []string `json:"skipped_worktrees,omitempty"`
	SkippedArtifacts      []string `json:"skipped_artifacts,omitempty"`
	PurgedEphemeralFiles  []string `json:"purged_ephemeral_files"`
	CleanedCacheArtifacts []string `json:"cleaned_cache_artifacts"`
	Errors                []string `json:"errors,omitempty"`
}

type pool struct {
	path     string
	worktree bool
	cache    bool
}

type candidate struct {
	path    string
	pool    pool
	stats   dirStats
	entries []treeEntry
}

type collector struct {
	opts     Options
	root     *os.Root
	report   *GCReport
	manager  *worktree.Manager
	released map[string]bool
}

// Collect preflights the complete bounded scope before any removal. A scan or
// application failure returns both a partial report and a non-nil error.
func Collect(ctx context.Context, opts Options) (*GCReport, error) {
	report := &GCReport{
		DryRun:           opts.DryRun,
		PlannedWorktrees: []string{}, PlannedArtifacts: []string{},
		PrunedWorktrees: []string{}, PurgedEphemeralFiles: []string{}, CleanedCacheArtifacts: []string{},
	}
	if ctx == nil {
		return failedReport(report, errors.New("gc: context cannot be nil"))
	}
	if err := ctx.Err(); err != nil {
		return failedReport(report, err)
	}
	normalized, pools, released, err := normalizeOptions(opts)
	if err != nil {
		return failedReport(report, err)
	}
	root, err := os.OpenRoot(normalized.RootDir)
	if err != nil {
		return failedReport(report, fmt.Errorf("open collection root: %w", err))
	}
	c := collector{normalized, root, report, worktree.NewManager(normalized.RootDir), released}
	err = c.collect(ctx, pools)
	err = errors.Join(err, root.Close())
	if err != nil {
		return failedReport(report, err)
	}
	report.Complete = true
	return report, nil
}

func failedReport(report *GCReport, err error) (*GCReport, error) {
	report.Errors = append(report.Errors, err.Error())
	return report, err
}

func (c *collector) collect(ctx context.Context, pools []pool) error {
	var plan []candidate
	for _, p := range pools {
		candidates, err := c.planPool(ctx, p)
		if err != nil {
			return err
		}
		plan = append(plan, candidates...)
		if err := validatePlanSize(plan); err != nil {
			return err
		}
	}
	for path, seen := range c.released {
		if !seen {
			return fmt.Errorf("released resource not found: %s", path)
		}
	}
	c.report.PlannedCacheCleanup = c.opts.CleanGoTestCache && !c.opts.SkipTestCache
	if c.opts.DryRun {
		return ctx.Err()
	}
	for _, item := range plan {
		if err := c.apply(ctx, item); err != nil {
			return err
		}
	}
	return c.cleanTestCache(ctx)
}

func (c *collector) planPool(ctx context.Context, p pool) ([]candidate, error) {
	if err := checkPath(c.root, p.path); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	entries, err := readEntries(ctx, c.root, p.path, MaxEntriesLimit)
	if err != nil {
		return nil, err
	}
	var plan []candidate
	for _, entry := range entries {
		path := filepath.Join(p.path, entry.Name())
		if _, released := c.released[path]; !released {
			c.skip(p, path, "owner has not released resource")
			continue
		}
		c.released[path] = true
		item, eligible, inspectErr := c.inspect(ctx, p, path)
		if inspectErr != nil {
			return nil, fmt.Errorf("inspect %s: %w", path, inspectErr)
		}
		if eligible {
			plan = append(plan, item)
			c.recordPlan(item)
		}
	}
	return plan, nil
}

func (c *collector) inspect(ctx context.Context, p pool, path string) (candidate, bool, error) {
	item := candidate{path: path, pool: p}
	if err := checkPath(c.root, path); err != nil {
		return item, false, err
	}
	var err error
	item.stats, item.entries, err = scanTree(ctx, c.root, path, p.worktree)
	if err != nil {
		return item, false, err
	}
	maxAge := c.opts.MaxArtifactAge
	if p.worktree {
		maxAge = c.opts.MaxWorktreeAge
	}
	if time.Since(item.stats.NewestMod) <= maxAge {
		c.skip(p, path, "retention period has not elapsed")
		return item, false, nil
	}
	if p.worktree {
		if err := c.checkLiveRoot(); err != nil {
			return item, false, err
		}
		if err := c.manager.CheckRemoval(ctx, filepath.Join(c.opts.RootDir, path)); err != nil {
			c.skip(p, path, err.Error())
			return item, false, err
		}
	}
	return item, true, nil
}

func (c *collector) skip(p pool, path, reason string) {
	message := path + ": " + reason
	if p.worktree {
		c.report.SkippedWorktrees = append(c.report.SkippedWorktrees, message)
	} else {
		c.report.SkippedArtifacts = append(c.report.SkippedArtifacts, message)
	}
}

func (c *collector) recordPlan(item candidate) {
	c.report.PlannedReclaimedBytes += item.stats.TotalSize
	if item.pool.worktree {
		c.report.PlannedWorktrees = append(c.report.PlannedWorktrees, item.path)
	} else {
		c.report.PlannedArtifacts = append(c.report.PlannedArtifacts, item.path)
	}
}

func (c *collector) apply(ctx context.Context, item candidate) error {
	current, eligible, err := c.inspect(ctx, item.pool, item.path)
	if err != nil {
		return err
	}
	if !eligible || !sameSnapshot(item, current) {
		return fmt.Errorf("released resource changed during collection: %s", item.path)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if item.pool.worktree {
		if err := c.checkLiveRoot(); err != nil {
			return err
		}
		err = c.manager.RemoveReleased(ctx, filepath.Join(c.opts.RootDir, item.path))
	} else {
		err = removeEntries(ctx, c.root, current.entries)
	}
	if err != nil {
		return fmt.Errorf("remove %s (may be partially removed): %w", item.path, err)
	}
	c.report.ReclaimedBytes += item.stats.TotalSize
	switch {
	case item.pool.worktree:
		c.report.PrunedWorktrees = append(c.report.PrunedWorktrees, item.path)
	case item.pool.cache:
		c.report.CleanedCacheArtifacts = append(c.report.CleanedCacheArtifacts, item.path)
	default:
		c.report.PurgedEphemeralFiles = append(c.report.PurgedEphemeralFiles, item.path)
	}
	return nil
}

func (c *collector) cleanTestCache(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if !c.report.PlannedCacheCleanup {
		return nil
	}
	bounded, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if _, err := util.RunCommand(bounded, c.opts.RootDir, "go", "clean", "-testcache"); err != nil {
		return fmt.Errorf("go clean -testcache: %w", err)
	}
	c.report.CleanedCacheArtifacts = append(c.report.CleanedCacheArtifacts, "go testcache")
	return nil
}

// External Git commands use a pathname rather than the pinned os.Root handle.
// Refuse a renamed or replaced repository before crossing that boundary.
func (c *collector) checkLiveRoot() error {
	pinned, err := c.root.Stat(".")
	if err != nil {
		return err
	}
	current, err := os.Lstat(c.opts.RootDir)
	if err != nil {
		return err
	}
	if !current.IsDir() || !os.SameFile(pinned, current) {
		return errors.New("collection root changed before Git inspection or removal")
	}
	return nil
}
