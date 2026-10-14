package state

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/cordanaLLM/praetor/internal/contextopt"
	"github.com/cordanaLLM/praetor/internal/worktree"
)

// apply carries out a conflict-free plan step by step. A step that fails stops the run with
// the steps before it done; each finished step left its legacy copy gone, so a second run
// plans only what is left.
func (p *legacyPlan) apply(ctx context.Context) error {
	if err := os.MkdirAll(p.working, 0o700); err != nil {
		return fmt.Errorf("create %s: %w", WorkingDirName, err)
	}
	for i := 0; i < len(p.items) && i < maxLegacyEntries; i++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := p.applyItem(ctx, p.items[i]); err != nil {
			return fmt.Errorf("migrate %s/%s: %w; earlier steps were applied, run the command again to continue",
				LegacyWorkingDirName, p.items[i].rel, err)
		}
	}
	p.report.Applied = true
	if err := p.removeLegacyDirs(); err != nil {
		return err
	}
	p.report.LegacyRemoved = true
	return nil
}

func (p *legacyPlan) applyItem(ctx context.Context, item legacyItem) error {
	switch item.action {
	case legacyMove:
		return p.moveFile(ctx, item)
	case legacyDuplicate:
		return p.dropDuplicate(ctx, item)
	case legacyMerge:
		return p.mergeFile(ctx, item)
	case legacyWorktree:
		return p.moveWorktree(ctx, item)
	}
	return fmt.Errorf("unknown migration step %d", item.action)
}

// moveFile links the legacy file at its new name, which fails rather than replaces when that
// name exists, checks the SHA-256 of the file now there and only then removes the old name.
func (p *legacyPlan) moveFile(ctx context.Context, item legacyItem) error {
	src, dst := p.legacyPath(item.rel), p.workingPath(item.rel)
	if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
		return err
	}
	if err := os.Link(src, dst); err != nil {
		return fmt.Errorf("link into %s (an existing file is never replaced): %w", WorkingDirName, err)
	}
	if err := p.verifyDigest(ctx, dst, item.digest); err != nil {
		return errors.Join(err, os.Remove(dst))
	}
	return os.Remove(src)
}

// dropDuplicate removes a legacy copy only while both copies still hold the planned bytes.
func (p *legacyPlan) dropDuplicate(ctx context.Context, item legacyItem) error {
	if err := p.verifyDigest(ctx, p.workingPath(item.rel), item.digest); err != nil {
		return err
	}
	if err := p.verifyDigest(ctx, p.legacyPath(item.rel), item.digest); err != nil {
		return err
	}
	return os.Remove(p.legacyPath(item.rel))
}

// mergeFile replaces the .workingdir ledger only while it still holds the bytes the merge
// was computed from, then removes the legacy ledger the merge took in.
func (p *legacyPlan) mergeFile(ctx context.Context, item legacyItem) error {
	src := p.legacyPath(item.rel)
	if err := p.verifyDigest(ctx, src, item.digest); err != nil {
		return err
	}
	options := contextopt.ReplaceOptions{Expected: item.expected, Exists: true, Mode: 0o600}
	if err := contextopt.ReplaceSnapshot(ctx, p.workingPath(item.rel), item.merged, options); err != nil {
		return fmt.Errorf("write merged ledger: %w", err)
	}
	return os.Remove(src)
}

func (p *legacyPlan) moveWorktree(ctx context.Context, item legacyItem) error {
	dst := p.workingPath(item.rel)
	if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
		return err
	}
	return worktree.NewManager(p.root).Move(ctx, p.legacyPath(item.rel), dst)
}

func (p *legacyPlan) verifyDigest(ctx context.Context, path, want string) error {
	got, _, err := contextopt.DigestBinarySnapshot(ctx, path, maxLegacyFileBytes)
	if err != nil {
		return err
	}
	if got != want {
		return fmt.Errorf("%s changed since the migration was planned (sha256 %s, planned %s)", path, got, want)
	}
	return nil
}

// removeLegacyDirs removes the emptied legacy directories, deepest first, then the folder.
// os.Remove refuses a directory that still holds anything, so no content is ever deleted.
func (p *legacyPlan) removeLegacyDirs() error {
	for i := len(p.dirs) - 1; i >= 0; i-- {
		if err := os.Remove(p.legacyPath(p.dirs[i])); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("%s still holds content after the migration: %w", LegacyWorkingDirName, err)
		}
	}
	if err := os.Remove(p.legacy); err != nil {
		return fmt.Errorf("%s still holds content after the migration: %w", LegacyWorkingDirName, err)
	}
	return nil
}
