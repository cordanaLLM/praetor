// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
// SPDX-License-Identifier: EUPL-1.2

//go:build unix

package util

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

// OwnsFile reports whether info belongs to the calling user.
func OwnsFile(info os.FileInfo) bool {
	if info == nil {
		return false
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	return ok && int64(stat.Uid) == int64(os.Getuid())
}

// PrivateDirectory creates or opens an owner-only directory without accepting a final symlink.
// owns defaults to OwnsFile; tests may plant ownership without privileged chown calls.
// Local regular-file operations do not support kernel deadlines; ctx bounds each operation boundary.
func PrivateDirectory(ctx context.Context, path string, owns func(os.FileInfo) bool) (*os.Root, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if owns == nil {
		owns = OwnsFile
	}
	// nosemgrep: go.lang.correctness.permissions.file_permission.incorrect-default-permission -- owner search bit required for private directory
	if err := os.Mkdir(path, 0o700); err != nil && !errors.Is(err, os.ErrExist) {
		return nil, fmt.Errorf("private directory %q cannot be created: %w", path, err)
	}
	info, err := privateDirectoryInfo(path, owns)
	if err != nil {
		return nil, err
	}
	return pinPrivateDirectory(ctx, path, info)
}

func privateDirectoryInfo(path string, owns func(os.FileInfo) bool) (os.FileInfo, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("private directory %q is a symlink or not a directory", path)
	}
	if !owns(info) {
		return nil, fmt.Errorf("private directory %q is not owned by caller", path)
	}
	if info.Mode().Perm() != 0o700 {
		return nil, fmt.Errorf("private directory %q must have mode 0700 (group or other access refused)", path)
	}
	return info, nil
}

func pinPrivateDirectory(ctx context.Context, path string, info os.FileInfo) (*os.Root, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	root, err := os.OpenRoot(path)
	if err != nil {
		return nil, err
	}
	opened, err := root.Stat(".")
	if err == nil && !os.SameFile(info, opened) {
		err = errors.New("private directory changed during open")
	}
	if err = errors.Join(err, ctx.Err()); err != nil {
		return nil, errors.Join(err, root.Close())
	}
	return root, nil
}

// UserRuntimeDirectory resolves XDG_RUNTIME_DIR, or a private named folder in system temp.
// The fallback name is local to system temp; callers share this resolver across sandboxes.
func UserRuntimeDirectory(ctx context.Context, fallback string) (string, error) {
	if path := os.Getenv("XDG_RUNTIME_DIR"); path != "" {
		return path, nil
	}
	if !filepath.IsLocal(fallback) {
		return "", errors.New("runtime fallback must be a local name")
	}
	path := filepath.Join(os.TempDir(), fallback)
	root, err := PrivateDirectory(ctx, path, nil)
	if err != nil {
		return "", err
	}
	return path, root.Close()
}
