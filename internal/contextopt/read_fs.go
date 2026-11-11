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

type fsReadResult struct {
	data []byte
	err  error
}

// ReadFS reads at most limit bytes from fsys, refusing a longer file. Cancellation
// returns promptly even if Open, Read or Close blocks. An uncooperative filesystem
// can retain the worker until its operation returns; the worker closes an opened file.
func ReadFS(ctx context.Context, fsys fs.FS, name string, limit int) ([]byte, error) {
	if ctx == nil {
		return nil, fmt.Errorf("read %s: context is required", name)
	}
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("read %s: %w", name, err)
	}
	if limit < 0 || limit > int(^uint(0)>>1)-101 {
		return nil, fmt.Errorf("read %s: invalid byte limit", name)
	}
	result := make(chan fsReadResult, 1)
	go func() { data, err := readFS(ctx, fsys, name, limit); result <- fsReadResult{data, err} }()
	select {
	case <-ctx.Done():
		return nil, fmt.Errorf("read %s: %w", name, ctx.Err())
	case r := <-result:
		if err := ctx.Err(); err != nil {
			return nil, fmt.Errorf("read %s: %w", name, err)
		}
		if r.err != nil {
			return nil, fmt.Errorf("read %s: %w", name, r.err)
		}
		return r.data, nil
	}
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
