// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
// SPDX-License-Identifier: EUPL-1.2

package main

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/cordanaLLM/praetor/internal/config"
	"github.com/cordanaLLM/praetor/internal/util"
)

func runExceptionMigration(path, todayStr string, write bool) error {
	today, err := exceptionToday(todayStr)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 35*time.Second)
	defer cancel()
	data, proposals, err := config.PlanExceptionMigration(ctx, path, today)
	if err != nil {
		return fmt.Errorf("plan exception migration: %w", err)
	}
	unresolved := printExceptionMigrations(path, proposals)
	if write && len(proposals) > unresolved {
		if err := writeExceptionMigration(ctx, path, data); err != nil {
			return err
		}
	}
	if unresolved > 0 {
		return fmt.Errorf("%d exception migrations unresolved; existing entries retained", unresolved)
	}
	return nil
}

func writeExceptionMigration(ctx context.Context, path string, data []byte) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("apply exception migration: %w", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("stat migration manifest: %w", err)
	}
	if err := util.WriteFileAtomic(path, data, info.Mode().Perm()); err != nil {
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
