// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
// SPDX-License-Identifier: EUPL-1.2

package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/cordanaLLM/praetor/internal/config"
	"github.com/cordanaLLM/praetor/internal/util"
)

// errExceptionMigrationManifestChanged refuses replacement of a manifest edited after planning.
var errExceptionMigrationManifestChanged = errors.New("manifest changed since it was planned; rerun the migration")

// runExceptionMigration plans exception migrations for a manifest, prints proposals, and optionally writes them.
func runExceptionMigration(path, todayStr string, write bool) error {
	today, err := exceptionToday(todayStr)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 35*time.Second)
	defer cancel()
	plan, err := config.PlanExceptionMigrationSnapshot(ctx, path, today)
	if err != nil {
		return fmt.Errorf("plan exception migration: %w", err)
	}
	unresolved := printExceptionMigrations(path, plan.Proposals)
	if write && len(plan.Proposals) > unresolved {
		if err := writeExceptionMigration(ctx, path, plan.Data, plan.Source); err != nil {
			return err
		}
	}
	if unresolved > 0 {
		return fmt.Errorf("%d exception migrations unresolved; existing entries retained", unresolved)
	}
	return nil
}

// writeExceptionMigration refuses changed source, writes migrated data atomically, and verifies it via readback.
func writeExceptionMigration(ctx context.Context, path string, data, source []byte) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("apply exception migration: %w", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("stat migration manifest: %w", err)
	}
	// Windows reports writable files as 0666; remove shared write bits without
	// widening the manifest's existing permissions.
	mode := info.Mode().Perm() &^ 0o022
	if err := checkExceptionMigrationSource(path, source); err != nil {
		return err
	}
	if err := util.WriteFileConfined(filepath.Dir(path), filepath.Base(path), data, mode); err != nil {
		return fmt.Errorf("write exception migration: %w", err)
	}
	readback, err := util.ReadConfinedLimited(filepath.Dir(path), filepath.Base(path), int64(len(data))+1)
	if err != nil {
		return fmt.Errorf("read back exception migration: %w", err)
	}
	if !bytes.Equal(data, readback) {
		return fmt.Errorf("exception migration readback differs from proposal")
	}
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("verify exception migration: %w", err)
	}
	return nil
}

// checkExceptionMigrationSource compares the current confined manifest with the exact bytes used for planning.
func checkExceptionMigrationSource(path string, source []byte) error {
	current, err := util.ReadConfinedLimited(filepath.Dir(path), filepath.Base(path), int64(len(source))+1)
	if errors.Is(err, util.ErrFileTooLarge) || err == nil && !bytes.Equal(source, current) {
		return errExceptionMigrationManifestChanged
	}
	if err != nil {
		return fmt.Errorf("reread migration manifest: %w", err)
	}
	return nil
}

// printExceptionMigrations outputs proposed migrations to stdout and returns the count of unresolved proposals.
func printExceptionMigrations(path string, proposals []config.ExceptionMigration) int {
	unresolved := 0
	for i := 0; i < len(proposals) && i < config.MaxExceptions; i++ {
		proposal := proposals[i]
		entry := proposal.Entry
		status := "proposed"
		if proposal.Unresolved != "" {
			unresolved++
			status = "unresolved: " + proposal.Unresolved
		}
		line := fmt.Sprintf("path=%s rule=%s target=%s expires=%s category=%s entered=%s source=%s status=%s",
			path, entry.Rule, entry.Target(), entry.Expires, entry.Category, entry.Entered, proposal.Source, status)
		quoted := strconv.QuoteToGraphic(line)
		fmt.Println(quoted[1 : len(quoted)-1])
	}
	return unresolved
}
