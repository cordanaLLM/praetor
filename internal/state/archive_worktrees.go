// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package state

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/cordanaLLM/praetor/internal/util"
	"github.com/cordanaLLM/praetor/internal/worktree"
)

// archiveTaskRows extends the checkbox parser's completed set with the table
// and bracket markers used by worktree records. Fenced examples never qualify.
func archiveTaskRows(lines []string, archive map[int]bool) []string {
	rows := make([]string, len(lines))
	var fence util.MarkdownFence
	for i := 0; i < len(lines) && i < maxScannedLines; i++ {
		line := strings.TrimSpace(lines[i])
		if fence.Inside(line) {
			continue
		}
		_, _, checkbox := taskCheckbox(line)
		done := strings.Contains(line, "| DONE") || strings.Contains(line, "[DONE")
		if checkbox || done || strings.HasPrefix(line, "|") {
			rows[i] = line
		}
		if done {
			archive[i] = true
		}
	}
	return rows
}

// retainWorktreeRows removes protected row indices from the archive plan. No
// ledger writes occur until every bounded Git and filesystem read succeeds.
func retainWorktreeRows(ctx context.Context, root string, rows []string, archive map[int]bool) error {
	present, err := util.GitWorktreePresent(ctx, root)
	if err != nil || !present {
		return err
	}
	entries, err := worktree.NewManager(root).List(ctx)
	if err != nil {
		return fmt.Errorf("archive worktree listing: %w", err)
	}
	for i := 0; i < len(entries) && i < worktree.MaxPorcelainLines; i++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		path, err := existingArchiveWorktree(entries[i])
		if err != nil {
			return err
		}
		if path != "" {
			delete(archive, retainedWorktreeRow(rows, archive, path))
		}
	}
	return nil
}

// existingArchiveWorktree returns the decoded path of a listed worktree that exists on disk, or "" for a bare or missing one.
func existingArchiveWorktree(entry worktree.WorktreeInfo) (string, error) {
	if entry.Bare || entry.Path == "" {
		return "", nil
	}
	path := entry.Path
	if strings.HasPrefix(path, `"`) {
		decoded, err := strconv.Unquote(path)
		if err != nil {
			return "", fmt.Errorf("decode worktree path: %w", err)
		}
		path = decoded
	}
	info, err := os.Stat(filepath.FromSlash(path))
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("inspect archive worktree %s: %w", path, err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("archive worktree %s is not a directory", path)
	}
	return path, nil
}

// retainedWorktreeRow prefers any existing unarchived row. Otherwise the last
// matching row in document order survives verbatim, carrying its id and steps.
func retainedWorktreeRow(rows []string, archive map[int]bool, path string) int {
	newest := -1
	for index := 0; index < len(rows) && index < maxScannedLines; index++ {
		if !namesWorktreePath(rows[index], path) {
			continue
		}
		if !archive[index] {
			return -1
		}
		newest = max(newest, index)
	}
	return newest
}

// namesWorktreePath requires a whole path token: a sibling or child path does
// not keep the row of its prefix. Both Git and native path separators qualify.
func namesWorktreePath(row, path string) bool {
	for _, spelling := range []string{path, filepath.FromSlash(path)} {
		if containsWorktreePath(row, spelling) {
			return true
		}
	}
	return false
}

// containsWorktreePath reports whether row names path as a whole token, not as a prefix of a longer path.
func containsWorktreePath(row, path string) bool {
	for attempt := 0; attempt < maxTaskLineBytes; attempt++ {
		before, after, found := strings.Cut(row, path)
		if !found {
			return false
		}
		left := before == "" || strings.ContainsAny(before[len(before)-1:], " \t`\"'(|:=")
		right := after == "" || strings.ContainsAny(after[:1], " \t`\"')|,;")
		if left && right {
			return true
		}
		row = after
	}
	return false
}
