// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package state

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/cordanaLLM/praetor/internal/contextopt"
)

// maxArchiveRecoveryBytes permits recovery above the ordinary snapshot limit, still bounded.
const maxArchiveRecoveryBytes = contextopt.MaxTotalBytes

// readArchiveOpen reuses the confined snapshot reader with the recovery pass's separate bound.
func readArchiveOpen(ctx context.Context, path string) ([]byte, error) {
	content, err := contextopt.ReadSnapshotLimit(ctx, path, maxArchiveRecoveryBytes)
	if err != nil {
		return nil, fmt.Errorf("archive OPEN.md recovery limit %d bytes: %w", maxArchiveRecoveryBytes, err)
	}
	return content, nil
}

// completedTaskText renders one archive block with a deadline check for each bounded row.
func completedTaskText(ctx context.Context, header string, tasks []string) (string, error) {
	var text strings.Builder
	text.WriteString(header)
	for i := 0; i < len(tasks) && i < maxScannedLines; i++ {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		text.WriteString(tasks[i])
		text.WriteByte('\n')
	}
	return text.String(), nil
}

// completedTaskChunks splits only at record boundaries, keeping each history file readable.
func completedTaskChunks(ctx context.Context, header string, tasks []string) ([]string, error) {
	chunks := []string{}
	var text strings.Builder
	text.WriteString(header)
	for i := 0; i < len(tasks) && i < maxScannedLines; i++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if text.Len()+len(tasks[i])+1 > maxLedgerBytes {
			chunks = append(chunks, text.String())
			text.Reset()
			text.WriteString(header)
		}
		text.WriteString(tasks[i])
		text.WriteByte('\n')
	}
	return append(chunks, text.String()), nil
}

// archiveCompletedChunks publishes bounded history files before their index enters BACKLOG.md.
// A failed later write can duplicate records, but OPEN.md still holds every original row.
func archiveCompletedChunks(ctx context.Context, rootPath, header string, tasks []string) (index string, err error) {
	chunks, err := completedTaskChunks(ctx, header, tasks)
	if err != nil {
		return "", err
	}
	root, err := contextopt.OpenDirectoryIn(ctx, ledgerRoot(ctx, rootPath), WorkingDirName)
	if err != nil {
		return "", err
	}
	defer func() { err = errors.Join(err, root.Close()) }()
	stamp := time.Now().UTC().Format(stateHistoryStamp)
	var links strings.Builder
	links.WriteString(header)
	for i := 0; i < len(chunks) && i < maxScannedLines; i++ {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		name := fmt.Sprintf("BACKLOG.history-%s-%d.md", stamp, i)
		created, err := contextopt.CreateRootSnapshot(ctx, root, name, []byte(chunks[i]), 0o600)
		if err != nil {
			return "", fmt.Errorf("archive completed records to %s: %w", name, err)
		}
		if !created {
			return "", fmt.Errorf("completed record archive %s already exists", name)
		}
		fmt.Fprintf(&links, "- Discharged records: [%s](%s)\n", name, filepath.ToSlash(name))
	}
	return links.String(), nil
}
