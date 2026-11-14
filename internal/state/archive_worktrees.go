// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package state

import (
	"context"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/cordanaLLM/praetor/internal/util"
	"github.com/cordanaLLM/praetor/internal/worktree"
)

// archiveWorktreeOps supplies the listing and context-bounded filesystem inspection.
type archiveWorktreeOps struct {
	list    func(context.Context, string) ([]worktree.WorktreeInfo, error)
	inspect func(context.Context, string) (archivePath, error)
}

// defaultArchiveWorktreeOps wires the real Git and bounded filesystem operations.
func defaultArchiveWorktreeOps() archiveWorktreeOps {
	return archiveWorktreeOps{list: listArchiveWorktrees, inspect: inspectArchivePath}
}

// listArchiveWorktrees propagates the porcelain parser's explicit line-limit error.
func listArchiveWorktrees(ctx context.Context, root string) ([]worktree.WorktreeInfo, error) {
	present, err := util.GitWorktreePresent(ctx, root)
	if err != nil || !present {
		return nil, err
	}
	return worktree.NewManager(root).List(ctx)
}

// archiveTaskRows recognizes checkbox, status-cell and leading bracket records only.
func archiveTaskRows(ctx context.Context, lines []string, archive map[int]bool) ([]string, error) {
	rows := make([]string, len(lines))
	var fence util.MarkdownFence
	statusColumn := 1
	for i := 0; i < len(lines) && i < maxScannedLines; i++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		line := strings.TrimSpace(lines[i])
		if fence.Inside(line) {
			continue
		}
		statusColumn = archiveTableStatusColumn(ctx, lines, i, statusColumn)
		row, done, err := archiveTaskRow(ctx, line, statusColumn)
		if err != nil {
			return nil, err
		}
		if row {
			rows[i] = line
		}

		if done {
			archive[i] = true
		}
	}
	return rows, ctx.Err()
}

// archiveTaskRow gives the checkbox grammar precedence over completion words in descriptions.
func archiveTaskRow(ctx context.Context, line string, statusColumn int) (row, done bool, err error) {
	_, completed, checkbox := taskCheckbox(line)
	if checkbox {
		return true, completed, nil
	}
	done, err = archiveRecordDone(ctx, line, statusColumn)
	return done || strings.HasPrefix(line, "|"), done, err
}

// archiveTableStatusColumn uses the ledger table cells and separator grammar to find Status.
func archiveTableStatusColumn(ctx context.Context, lines []string, index, previous int) int {
	if !strings.HasPrefix(strings.TrimSpace(lines[index]), "|") {
		return 1
	}
	if index+1 >= len(lines) || !ledgerTableSeparator(ledgerTableCells(strings.TrimSpace(lines[index+1])), func() bool { return ctx.Err() != nil }) {
		return previous
	}
	cells := ledgerTableCells(lines[index])
	for i := 1; i < len(cells)-1 && i < maxTaskLineBytes; i++ {
		if ctx.Err() != nil {
			return previous
		}
		if strings.TrimSpace(cells[i]) == "Status" {
			return i
		}
	}
	return -1
}

// archiveRecordDone accepts a status cell or the first bracket token, never description text.
func archiveRecordDone(ctx context.Context, line string, statusColumn int) (bool, error) {
	if strings.HasPrefix(line, "|") {
		return archiveTableDone(ctx, line, statusColumn)
	}
	first := strings.TrimPrefix(line, "- ")
	marker, _, closed := strings.Cut(first, "]")
	return closed && (marker == "[DONE" || strings.HasPrefix(marker, "[DONE ")), nil
}

// archiveTableDone ignores status-looking pipe cells inside the shared Markdown code spans.
func archiveTableDone(ctx context.Context, line string, statusColumn int) (bool, error) {
	cells := ledgerTableCells(line)
	if statusColumn < 1 || statusColumn >= len(cells)-1 {
		return false, nil
	}
	status := strings.TrimSpace(cells[statusColumn])
	if status != "DONE" && !strings.HasPrefix(status, "DONE —") {
		return false, nil
	}
	offset := len(strings.Join(cells[:statusColumn], "|")) + 1
	spans := util.MarkdownCodeSpans(line, maxTaskLineBytes)
	for i := 0; i < len(spans) && i < maxTaskLineBytes; i++ {
		if err := ctx.Err(); err != nil {
			return false, err
		}
		if spans[i].Start <= offset && offset < spans[i].End {
			return false, nil
		}
	}
	return true, ctx.Err()
}

// archiveRowPaths extracts complete pipe-delimited path fields, preserving embedded spaces.
func archiveRowPaths(ctx context.Context, row string) ([]string, error) {
	fields := ledgerTableCells(row)
	paths := make([]string, 0, len(fields))
	for i := 0; i < len(fields) && i < maxTaskLineBytes; i++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if path := archivePathField(fields[i]); path != "" {
			paths = append(paths, path)
		}
	}
	return paths, nil
}

// archivePathField reads a complete native, slash or UNC field without splitting embedded spaces.
func archivePathField(field string) string {
	field = strings.TrimSpace(field)
	start := archivePathStart(field)
	if start < 0 {
		return ""
	}
	path := strings.Trim(strings.TrimSpace(field[start:]), "`\"'()[]")
	return filepath.Clean(filepath.FromSlash(path))
}

// archivePathStart accepts a field boundary before native roots, drive letters and slash paths.
func archivePathStart(field string) int {
	start := strings.IndexAny(field, `/\`)
	if start < 0 {
		return -1
	}
	if start >= 2 && field[start-1] == ':' && archiveFieldBoundary(field, start-2) {
		start -= 2
	}
	if !archiveFieldBoundary(field, start) {
		return -1
	}
	if start > 0 && field[start-1] == ':' && start+1 < len(field) && field[start+1] == '/' {
		return -1
	}
	return start
}

// archiveFieldBoundary distinguishes a path field from a slash inside another token.
func archiveFieldBoundary(field string, start int) bool {
	return start == 0 || strings.ContainsAny(field[start-1:start], " \t`\"'(:=")
}

// archiveRowIndex caches normalized fields and their row positions for one archive pass.
type archiveRowIndex struct {
	rows       map[string][]int
	normalized map[string]string
}

// add resolves each unique path once and indexes a row at most once per normalized path.
func (index archiveRowIndex) add(ctx context.Context, row int, paths []string, ops archiveWorktreeOps) error {
	for i := 0; i < len(paths) && i < maxTaskLineBytes; i++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		path, cached := index.normalized[paths[i]]
		if !cached {
			result, err := ops.inspect(ctx, paths[i])
			if err != nil {
				return err
			}
			path = result.path
			index.normalized[paths[i]] = path
		}
		positions := index.rows[path]
		if len(positions) == 0 || positions[len(positions)-1] != row {
			index.rows[path] = append(positions, row)
		}
	}
	return nil
}

// indexWorktreeRows normalizes each unique field once instead of scanning rows per worktree.
func indexWorktreeRows(ctx context.Context, rows []string, ops archiveWorktreeOps) (map[string][]int, error) {
	index := archiveRowIndex{rows: make(map[string][]int), normalized: make(map[string]string)}
	for i := 0; i < len(rows) && i < maxScannedLines; i++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		paths, err := archiveRowPaths(ctx, rows[i])
		if err != nil {
			return nil, err
		}
		if err := index.add(ctx, i, paths, ops); err != nil {
			return nil, err
		}
	}
	return index.rows, nil
}

// retainWorktreeRows rechecks a fresh complete listing before any ledger write.
func retainWorktreeRows(ctx context.Context, root string, index map[string][]int, archive map[int]bool, ops archiveWorktreeOps) error {
	entries, err := ops.list(ctx, root)
	if err != nil {
		return fmt.Errorf("archive worktree listing: %w", err)
	}
	if len(entries) > worktree.MaxPorcelainLines {
		return fmt.Errorf("archive worktree listing: %w", worktree.ErrLimitExceeded)
	}
	for i := 0; i < len(entries) && i < worktree.MaxPorcelainLines; i++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		path, err := existingArchiveWorktree(ctx, entries[i], ops)
		if err != nil {
			return err
		}
		if path != "" {
			row, err := retainedWorktreeRow(ctx, index[path], archive)
			if err != nil {
				return err
			}
			delete(archive, row)
		}
	}
	return ctx.Err()
}

// existingArchiveWorktree decodes Git's spelling and checks existence under the archive deadline.
func existingArchiveWorktree(ctx context.Context, entry worktree.WorktreeInfo, ops archiveWorktreeOps) (string, error) {
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
	result, err := ops.inspect(ctx, filepath.Clean(filepath.FromSlash(path)))
	if err != nil {
		return "", err
	}
	if result.notDirectory {
		return "", fmt.Errorf("archive worktree %s is not a directory", path)
	}
	if !result.exists {
		return "", nil
	}
	return result.path, nil
}

// retainedWorktreeRow prefers an unarchived exact field, otherwise the newest completed row.
func retainedWorktreeRow(ctx context.Context, rows []int, archive map[int]bool) (int, error) {
	newest := -1
	for i := 0; i < len(rows) && i < maxScannedLines; i++ {
		if err := ctx.Err(); err != nil {
			return -1, err
		}
		if !archive[rows[i]] {
			return -1, nil
		}
		newest = max(newest, rows[i])
	}
	return newest, nil
}

// namesWorktreePath compares complete cleaned fields; spaces never terminate a path.
func namesWorktreePath(row, path string) bool {
	paths, err := archiveRowPaths(context.Background(), row)
	if err != nil {
		return false
	}
	for i := 0; i < len(paths) && i < maxTaskLineBytes; i++ {
		if paths[i] == filepath.Clean(filepath.FromSlash(path)) {
			return true
		}
	}
	return false
}
