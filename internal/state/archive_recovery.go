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
		if len(header)+len(tasks[i])+1 > maxLedgerBytes {
			return nil, fmt.Errorf("archive record exceeds the %d-byte history limit", maxLedgerBytes)
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

// archiveCompletedChunks rolls BACKLOG.md and completed rows into bounded history files.
// The replacement index's size is checked before any history chunk is published.
// A failed later write can duplicate records, but OPEN.md still holds every original row.
func archiveCompletedChunks(ctx context.Context, rootPath, header string, backlog []byte, tasks []string) (index string, err error) {
	chunks, err := completedTaskChunks(ctx, header, tasks)
	if err != nil {
		return "", err
	}
	chunks = append([]string{string(backlog)}, chunks...)
	names, index, err := archiveChunkIndex(ctx, header, len(chunks))
	if err != nil {
		return "", err
	}
	root, err := contextopt.OpenDirectoryIn(ctx, ledgerRoot(ctx, rootPath), WorkingDirName)
	if err != nil {
		return "", err
	}
	defer func() { err = errors.Join(err, root.Close()) }()
	for i := 0; i < len(chunks) && i < maxScannedLines; i++ {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		name := names[i]
		created, err := contextopt.CreateRootSnapshot(ctx, root, name, []byte(chunks[i]), 0o600)
		if err != nil {
			return "", fmt.Errorf("archive completed records to %s: %w", name, err)
		}
		if !created {
			return "", fmt.Errorf("completed record archive %s already exists", name)
		}
	}
	return index, nil
}

// archiveChunkIndex plans every history name and refuses an index beyond the ledger limit.
func archiveChunkIndex(ctx context.Context, header string, count int) ([]string, string, error) {
	if count > maxScannedLines {
		return nil, "", fmt.Errorf("archive index exceeds the %d chunk bound", maxScannedLines)
	}
	stamp := time.Now().UTC().Format(stateHistoryStamp)
	names := make([]string, count)
	var links strings.Builder
	links.WriteString(defaultBacklogMD())
	links.WriteString(header)
	for i := 0; i < count && i < maxScannedLines; i++ {
		if err := ctx.Err(); err != nil {
			return nil, "", err
		}
		name := fmt.Sprintf("BACKLOG.history-%s-%d.md", stamp, i)
		names[i] = name
		fmt.Fprintf(&links, "- Discharged records: [%s](%s)\n", name, filepath.ToSlash(name))
	}
	if links.Len() > maxLedgerBytes {
		return nil, "", fmt.Errorf("archive index exceeds the %d-byte ledger limit", maxLedgerBytes)
	}
	return names, links.String(), nil
}
