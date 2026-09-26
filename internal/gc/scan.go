package gc

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"time"
)

type dirStats struct {
	TotalSize int64
	NewestMod time.Time
	Entries   int
}

type treeEntry struct {
	path string
	info os.FileInfo
}

func readEntries(ctx context.Context, root *os.Root, path string, limit int) ([]os.DirEntry, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	dir, err := root.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", path, err)
	}
	entries, readErr := dir.ReadDir(limit + 1)
	if errors.Is(readErr, io.EOF) {
		readErr = nil
	}
	if err := errors.Join(readErr, dir.Close(), ctx.Err()); err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	if len(entries) > limit {
		return nil, fmt.Errorf("scan entry limit exceeded at %s", path)
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	return entries, nil
}

// walkRules selects what a bounded tree walk does besides measuring.
type walkRules struct {
	// collect records a removal snapshot and refuses what collection must not delete:
	// symlinks, special files, bare repositories and nested Git metadata.
	collect bool
	// allowGitFile admits a .git file at the walk root, a linked worktree's gitfile.
	allowGitFile bool
	// cutoff, when set, stops the walk at the first entry modified at or after it.
	cutoff time.Time
}

// reached reports whether newest already settles the walk's cutoff question.
func (r walkRules) reached(newest time.Time) bool {
	return !r.cutoff.IsZero() && !newest.Before(r.cutoff)
}

// scanTree is iterative, context-aware and bounded. Incomplete traversal,
// symlinks and special files are errors, never successful partial inventories.
func scanTree(ctx context.Context, root *os.Root, path string, isWorktree bool) (dirStats, []treeEntry, error) {
	return walkTree(ctx, root, path, walkRules{collect: true, allowGitFile: isWorktree})
}

// NewestModification reports the newest modification time anywhere in the tree at dir, the
// age gc's retention compares against, measured by the walk collection itself uses (HISS-19:
// the workstation harvester calls this rather than judge a worktree by its top-level stat).
// It measures rather than collects: symlinks and special files count by their own timestamps
// without being followed, and nested Git metadata is walked instead of refused. A non-zero
// cutoff stops the walk at the first entry modified at or after it, because that entry alone
// settles that the tree is not older than cutoff. Exceeding a bound or failing to stat an
// entry is an error, never a partial answer.
func NewestModification(ctx context.Context, dir string, cutoff time.Time) (time.Time, error) {
	if ctx == nil {
		return time.Time{}, errors.New("gc: context cannot be nil")
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return time.Time{}, fmt.Errorf("open %s: %w", dir, err)
	}
	stats, _, err := walkTree(ctx, root, ".", walkRules{cutoff: cutoff})
	if err = errors.Join(err, root.Close()); err != nil {
		return time.Time{}, err
	}
	return stats.NewestMod, nil
}

func walkTree(ctx context.Context, root *os.Root, path string, rules walkRules) (dirStats, []treeEntry, error) {
	var stats dirStats
	var snapshot []treeEntry
	queue := []string{path}
	dirs := 0
	for i := 0; i < len(queue) && i < MaxFileScanLimit; i++ {
		current := queue[i]
		info, err := scanEntry(ctx, root, current, &stats, rules.collect)
		if err != nil {
			return stats, nil, err
		}
		if rules.collect {
			snapshot = append(snapshot, treeEntry{current, info})
		}
		if rules.reached(stats.NewestMod) {
			return stats, snapshot, nil
		}
		if !info.IsDir() {
			continue
		}
		dirs++
		if dirs > MaxDirectoryTraversal {
			return stats, nil, errors.New("directory traversal limit exceeded")
		}
		children, err := scanChildren(ctx, root, current, MaxFileScanLimit-len(queue), rules, current == path)
		if err != nil {
			return stats, nil, err
		}
		queue = append(queue, children...)
	}
	return stats, snapshot, nil
}

func scanChildren(ctx context.Context, root *os.Root, path string, limit int, rules walkRules, atRoot bool) ([]string, error) {
	entries, err := readEntries(ctx, root, path, limit)
	if err != nil {
		return nil, err
	}
	if rules.collect {
		if err := refuseRepositoryMetadata(path, entries, rules.allowGitFile && atRoot); err != nil {
			return nil, err
		}
	}
	children := make([]string, 0, len(entries))
	for _, entry := range entries {
		children = append(children, filepath.Join(path, entry.Name()))
	}
	return children, nil
}

// refuseRepositoryMetadata protects Git state from collection: a bare repository, or nested
// .git metadata other than the linked-worktree gitfile at the resource root.
func refuseRepositoryMetadata(path string, entries []os.DirEntry, gitFileAllowed bool) error {
	if looksLikeBareRepository(entries) {
		return fmt.Errorf("bare Git repository protected at %s", path)
	}
	for _, entry := range entries {
		if entry.Name() == ".git" && (!gitFileAllowed || entry.IsDir()) {
			return fmt.Errorf("nested Git metadata protected at %s", path)
		}
	}
	return nil
}

// scanEntry measures one entry. Under collect a symlink or special file is an error; a
// measuring walk counts its own timestamp and never follows it (it is not a directory).
func scanEntry(ctx context.Context, root *os.Root, path string, stats *dirStats, collect bool) (os.FileInfo, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	info, err := root.Lstat(path)
	if err != nil {
		return nil, fmt.Errorf("stat %s: %w", path, err)
	}
	if collect && !info.IsDir() && !info.Mode().IsRegular() {
		return nil, fmt.Errorf("symlink or special file protected: %s", path)
	}
	stats.Entries++
	if info.ModTime().After(stats.NewestMod) {
		stats.NewestMod = info.ModTime()
	}
	if info.Mode().IsRegular() {
		stats.TotalSize += info.Size()
	}
	return info, nil
}

func validatePlanSize(plan []candidate) error {
	entries := 0
	for _, item := range plan {
		entries += len(item.entries)
		if entries > MaxFileScanLimit {
			return errors.New("combined collection plan exceeds entry limit")
		}
	}
	return nil
}

func sameSnapshot(before, after candidate) bool {
	if before.stats != after.stats || len(before.entries) != len(after.entries) {
		return false
	}
	for i, entry := range before.entries {
		other := after.entries[i]
		if entry.path != other.path || !sameEntry(entry.info, other.info) {
			return false
		}
	}
	return true
}

func sameEntry(before, after os.FileInfo) bool {
	return os.SameFile(before, after) && before.Mode() == after.Mode() && before.Size() == after.Size() && before.ModTime().Equal(after.ModTime())
}

// removeEntries removes only inspected entries, with cancellation and identity
// checks before each operation. Remove refuses nonempty directories, preserving
// files created after the scan. Quiescent ownership is still required.
func removeEntries(ctx context.Context, root *os.Root, entries []treeEntry) error {
	for i := len(entries) - 1; i >= 0; i-- {
		if err := ctx.Err(); err != nil {
			return err
		}
		entry := entries[i]
		current, err := root.Lstat(entry.path)
		if err != nil {
			return err
		}
		if !removalIdentityMatches(entry.info, current) {
			return fmt.Errorf("resource changed before removal: %s", entry.path)
		}
		if err := root.Remove(entry.path); err != nil {
			return err
		}
	}
	return nil
}

func removalIdentityMatches(before, after os.FileInfo) bool {
	// Removing children updates their parent's mtime and size; the parent's inode
	// and kind must still match, and Remove itself requires an empty directory.
	if before.IsDir() {
		return after.IsDir() && os.SameFile(before, after)
	}
	return sameEntry(before, after)
}

func looksLikeBareRepository(entries []os.DirEntry) bool {
	head, objects := false, false
	for _, entry := range entries {
		if entry.Name() == "HEAD" && !entry.IsDir() {
			head = true
		}
		if entry.Name() == "objects" && entry.IsDir() {
			objects = true
		}
	}
	return head && objects
}
