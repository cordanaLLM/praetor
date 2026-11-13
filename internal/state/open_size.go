// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package state

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"

	"github.com/cordanaLLM/praetor/internal/contextopt"
	"github.com/cordanaLLM/praetor/internal/util"
)

const archiveTaskCommand = "praetorctl state task archive"

// syncStateBinding reports the advisory once per sync, before the bound write.
func syncStateBinding(ctx context.Context, root string, snap *StateSnapshot) (string, error) {
	warning, err := OpenLedgerSizeWarning(ctx, root)
	if err != nil {
		return "", err
	}
	if warning != "" {
		slog.Warn(warning)
	}
	return stateBinding(ctx, root, snap)
}

// OpenLedgerSizeWarning checks the bounded OPEN.md snapshot without writing it.
// The warning is advisory through the read limit; the next byte is an error.
func OpenLedgerSizeWarning(ctx context.Context, rootPath string) (string, error) {
	content, err := readTaskLedger(ctx, filepath.Join(ledgerRoot(ctx, rootPath), WorkingDirName, openLedgerName))
	if err != nil {
		return "", err
	}
	if len(content) <= openLedgerWarnBytes {
		return "", nil
	}
	return fmt.Sprintf("OPEN.md size warning: %d bytes exceeds %d-byte warning threshold; read limit %d bytes; run `%s`",
		len(content), openLedgerWarnBytes, maxLedgerBytes, archiveTaskCommand), nil
}

// readTaskLedger retains the snapshot reader's typed errors and adds the same
// recovery command wherever an oversized OPEN.md is encountered.
func readTaskLedger(ctx context.Context, path string) ([]byte, error) {
	content, err := contextopt.ReadSnapshot(ctx, path)
	if filepath.Base(path) == openLedgerName && errors.Is(err, util.ErrFileTooLarge) {
		return nil, fmt.Errorf("OPEN.md read limit %d bytes; run `%s`: %w", maxLedgerBytes, archiveTaskCommand, err)
	}
	return content, err
}
