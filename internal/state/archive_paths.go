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
	"runtime"
	"syscall"

	"github.com/cordanaLLM/praetor/internal/util"
)

// archivePathSlot caps outstanding stat/resolution workers, including stalled syscalls, at one.
var archivePathSlot = make(chan struct{}, 1)

// archivePathArgumentLimit returns the host's pre-stat total argument ceiling, or 0 when stat decides.
var archivePathArgumentLimit = hostArchivePathArgumentLimit

// archiveStat is the filesystem stat entry point; tests override it to exercise fail-closed fallbacks.
var archiveStat = os.Stat

// archivePath carries the canonical spelling and whether its directory exists.
type archivePath struct {
	path         string
	exists       bool
	notDirectory bool
}

// archivePathResult delivers a filesystem answer without blocking an abandoned worker.
type archivePathResult struct {
	value archivePath
	err   error
}

// inspectArchivePath bounds both stat and symlink resolution with the archive context.
func inspectArchivePath(ctx context.Context, path string) (archivePath, error) {
	return boundedArchivePath(ctx, path, func(path string) (archivePath, error) { return lookupArchivePath(ctx, path) })
}

// boundedArchivePath leaves its shared slot occupied until a stalled syscall actually returns.
func boundedArchivePath(ctx context.Context, path string, lookup func(string) (archivePath, error)) (archivePath, error) {
	if err := ctx.Err(); err != nil {
		return archivePath{}, fmt.Errorf("inspect archive worktree %s: %w", path, err)
	}
	select {
	case archivePathSlot <- struct{}{}:
	case <-ctx.Done():
		return archivePath{}, fmt.Errorf("inspect archive worktree %s: %w", path, ctx.Err())
	}
	done := make(chan archivePathResult, 1)
	go func() {
		defer func() { <-archivePathSlot }()
		value, err := lookup(path)
		done <- archivePathResult{value: value, err: err}
	}()
	select {
	case <-ctx.Done():
		return archivePath{}, fmt.Errorf("inspect archive worktree %s: %w", path, ctx.Err())
	case result := <-done:
		if err := ctx.Err(); err != nil {
			return archivePath{}, fmt.Errorf("inspect archive worktree %s: %w", path, err)
		}
		return result.value, result.err
	}
}

// lookupArchivePath keeps the cleaned spelling on resolution errors; only stat can prove absence.
func lookupArchivePath(ctx context.Context, path string) (archivePath, error) {
	result := archivePath{path: filepath.Clean(filepath.FromSlash(path))}
	info, err := statArchivePath(result.path)
	if err != nil {
		return result, err
	}
	result.exists = info != nil && info.IsDir()
	result.notDirectory = info != nil && !info.IsDir()
	if resolved, err := util.ResolveExistingPath(ctx, result.path); err == nil {
		result.path = filepath.Clean(resolved)
	}
	return result, nil
}

// statArchivePath treats missing or unrepresentable names as absent and wraps other stat errors.
func statArchivePath(path string) (os.FileInfo, error) {
	info, err := archiveStat(path)
	if err != nil && !archivePathAbsent(err, runtime.GOOS) {
		return nil, fmt.Errorf("inspect archive worktree %s: %w", path, err)
	}
	return info, nil
}

// archivePathAbsent recognizes missing or unrepresentable names without hiding inspection failures.
// Windows: 2/3 missing file/path, 53 missing network path, 123 invalid name, 206 too long.
// https://learn.microsoft.com/en-us/windows/win32/debug/system-error-codes--0-499-
// Unix retains its missing and ENAMETOOLONG rules.
func archivePathAbsent(err error, platform string) bool {
	if platform == "windows" {
		var code syscall.Errno
		if errors.As(err, &code) {
			switch code {
			case 2, 3, 53, 123, 206:
				return true
			}
			return false
		}
	}
	return errors.Is(err, os.ErrNotExist) || errors.Is(err, syscall.ENAMETOOLONG)
}
