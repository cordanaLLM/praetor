// SPDX-License-Identifier: EUPL-1.2
// SPDX-FileCopyrightText: 2026 Lusoris <lusoris@proton.me>

package contextopt

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"

	"github.com/cordanaLLM/praetor/internal/util"
)

type fsOpResult[T any] struct {
	value T
	err   error
}

// runFS runs fn on a worker goroutine and returns promptly when ctx ends, even if the
// underlying filesystem operation blocks.
func runFS[T any](ctx context.Context, verb, name string, fsys fs.FS, fn func() (T, error)) (T, error) {
	var zero T
	if ctx == nil {
		return zero, fmt.Errorf("%s %s: context is required", verb, name)
	}
	if err := ctx.Err(); err != nil {
		return zero, fmt.Errorf("%s %s: %w", verb, name, err)
	}
	if fsys == nil {
		return zero, fmt.Errorf("%s %s: filesystem is required", verb, name)
	}
	result := make(chan fsOpResult[T], 1)
	go func() {
		value, err := fn()
		result <- fsOpResult[T]{value: value, err: err}
	}()
	select {
	case <-ctx.Done():
		return zero, fmt.Errorf("%s %s: %w", verb, name, ctx.Err())
	case r := <-result:
		if err := ctx.Err(); err != nil {
			return zero, fmt.Errorf("%s %s: %w", verb, name, err)
		}
		if r.err != nil {
			return zero, fmt.Errorf("%s %s: %w", verb, name, r.err)
		}
		return r.value, nil
	}
}

// ReadFS reads at most limit bytes from fsys, refusing a longer file. Cancellation
// returns promptly even if Open, Read or Close blocks. An uncooperative filesystem
// can retain the worker until its operation returns; the worker closes an opened file.
func ReadFS(ctx context.Context, fsys fs.FS, name string, limit int) ([]byte, error) {
	if limit < 0 || limit > int(^uint(0)>>1)-101 {
		return nil, fmt.Errorf("read %s: invalid byte limit", name)
	}
	return runFS(ctx, "read", name, fsys, func() ([]byte, error) {
		return readFS(ctx, fsys, name, limit)
	})
}

// ReadDirFS reads the named directory from fsys and returns promptly when ctx ends,
// even if ReadDir or Open blocks on an uncooperative filesystem.
func ReadDirFS(ctx context.Context, fsys fs.FS, name string) ([]fs.DirEntry, error) {
	return runFS(ctx, "read dir", name, fsys, func() ([]fs.DirEntry, error) {
		return fs.ReadDir(fsys, name)
	})
}

// StatFS stats the named file in fsys and returns promptly when ctx ends, even if Stat
// or Open blocks on an uncooperative filesystem.
func StatFS(ctx context.Context, fsys fs.FS, name string) (fs.FileInfo, error) {
	return runFS(ctx, "stat", name, fsys, func() (fs.FileInfo, error) {
		return fs.Stat(fsys, name)
	})
}

// readFS owns the opened file and checks cancellation between bounded reads.
func readFS(ctx context.Context, fsys fs.FS, name string, limit int) (data []byte, err error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	file, err := fsys.Open(name)
	if err != nil {
		return nil, err
	}
	defer func() { err = errors.Join(err, file.Close()) }()
	chunk := make([]byte, 32*1024)
	for i := 0; i <= limit+100; i++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		remaining := limit + 1 - len(data)
		size := min(len(chunk), remaining)
		n, readErr := file.Read(chunk[:size])
		if n < 0 || n > size {
			return nil, errors.New("invalid read count")
		}
		data = append(data, chunk[:n]...)
		if len(data) > limit {
			return nil, fmt.Errorf("%w: limit %d bytes", util.ErrFileTooLarge, limit)
		}
		if errors.Is(readErr, io.EOF) {
			return data, nil
		}
		if readErr != nil {
			return nil, readErr
		}
	}
	return nil, io.ErrNoProgress
}
