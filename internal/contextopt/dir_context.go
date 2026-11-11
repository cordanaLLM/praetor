// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package contextopt

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
)

// StatContext checks cancellation on both sides of a filesystem stat.
func StatContext(ctx context.Context, path string) (os.FileInfo, error) {
	if ctx == nil {
		return nil, errors.New("context required")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	info, err := os.Stat(path)
	if ctxErr := ctx.Err(); ctxErr != nil {
		return nil, ctxErr
	}
	return info, err
}

// ReadDirContext reads at most limit entries from a pinned directory.
func ReadDirContext(ctx context.Context, path string, limit int) (entries []os.DirEntry, err error) {
	if ctx == nil || limit < 1 {
		return nil, errors.New("context and positive entry limit required")
	}
	root, err := OpenDirectory(ctx, path)
	if err != nil {
		return nil, err
	}
	defer func() { err = errors.Join(err, root.Close()) }()
	file, err := root.Open(".")
	if err != nil {
		return nil, err
	}
	defer func() { err = errors.Join(err, file.Close()) }()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	entries, err = file.ReadDir(limit + 1)
	if ctxErr := ctx.Err(); ctxErr != nil {
		return nil, ctxErr
	}
	if err != nil && !errors.Is(err, io.EOF) {
		return nil, err
	}
	if len(entries) > limit {
		return nil, fmt.Errorf("directory exceeds %d entries", limit)
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	return entries, nil
}
