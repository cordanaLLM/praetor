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

// scanTree is iterative, context-aware and bounded. Incomplete traversal,
// symlinks and special files are errors, never successful partial inventories.
func scanTree(ctx context.Context, root *os.Root, path string, isWorktree bool) (dirStats, []treeEntry, error) {
	var stats dirStats
	var snapshot []treeEntry
	queue := []string{path}
	dirs := 0
	for i := 0; i < len(queue) && i < MaxFileScanLimit; i++ {
		current := queue[i]
		info, err := scanEntry(ctx, root, current, &stats)
		if err != nil {
			return stats, nil, err
		}
		snapshot = append(snapshot, treeEntry{current, info})
		if !info.IsDir() {
			continue
		}
		dirs++
		if dirs > MaxDirectoryTraversal {
			return stats, nil, errors.New("directory traversal limit exceeded")
		}
		children, err := scanChildren(ctx, root, current, MaxFileScanLimit-len(queue), isWorktree && current == path)
		if err != nil {
			return stats, nil, err
		}
		queue = append(queue, children...)
	}
	return stats, snapshot, nil
}

func scanChildren(ctx context.Context, root *os.Root, path string, limit int, allowGitFile bool) ([]string, error) {
	entries, err := readEntries(ctx, root, path, limit)
	if err != nil {
		return nil, err
	}
	if looksLikeBareRepository(entries) {
		return nil, fmt.Errorf("bare Git repository protected at %s", path)
	}
	children := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.Name() == ".git" && (!allowGitFile || entry.IsDir()) {
			return nil, fmt.Errorf("nested Git metadata protected at %s", path)
		}
		children = append(children, filepath.Join(path, entry.Name()))
	}
	return children, nil
}

func scanEntry(ctx context.Context, root *os.Root, path string, stats *dirStats) (os.FileInfo, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	info, err := root.Lstat(path)
	if err != nil {
		return nil, fmt.Errorf("stat %s: %w", path, err)
	}
	if !info.IsDir() && !info.Mode().IsRegular() {
		return nil, fmt.Errorf("symlink or special file protected: %s", path)
	}
	stats.Entries++
	if info.ModTime().After(stats.NewestMod) {
		stats.NewestMod = info.ModTime()
	}
	if !info.IsDir() {
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
