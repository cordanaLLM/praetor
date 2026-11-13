package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"path/filepath"

	"github.com/cordanaLLM/praetor/internal/strictjson"
	"github.com/cordanaLLM/praetor/internal/util"
)

// readMergeJSON checks context and both byte bounds before decoding or retaining a review.
func readMergeJSON(ctx context.Context, path string, target any, remaining *int) error {
	limit := min(mergeFileLimit, *remaining)
	if limit <= 0 {
		return fmt.Errorf("merge input byte budget exceeded")
	}
	data, err := readMergeFile(ctx, filepath.Clean(path), limit)
	if err != nil {
		return fmt.Errorf("read %q: %w", path, err)
	}
	*remaining -= len(data)
	if err := strictjson.Decode(data, target, strictjson.Options{MaxBytes: limit, MaxDepth: 16}); err != nil {
		return fmt.Errorf("decode %q: %w", path, err)
	}
	return ctx.Err()
}

// readMergeFile opens without FIFO blocking, verifies the descriptor, and bounds every read.
func readMergeFile(ctx context.Context, path string, limit int) (data []byte, resultErr error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	file, err := openMergeInput(path)
	if err != nil {
		return nil, fmt.Errorf("open review: %w", err)
	}
	defer func() { resultErr = errors.Join(resultErr, file.Close()) }()
	info, err := file.Stat()
	if err != nil {
		return nil, fmt.Errorf("stat review: %w", err)
	}
	if !info.Mode().IsRegular() {
		return nil, util.ErrNotRegularFile
	}
	if info.Size() > int64(limit) {
		return nil, util.ErrFileTooLarge
	}
	reader := mergeContextReader{ctx: ctx, reader: file}
	data, err = io.ReadAll(io.LimitReader(reader, int64(limit)+1))
	if err != nil {
		return nil, fmt.Errorf("read review: %w", err)
	}
	if len(data) > limit {
		return nil, util.ErrFileTooLarge
	}
	return data, ctx.Err()
}

// mergeContextReader checks invocation cancellation around each bounded file read.
type mergeContextReader struct {
	ctx    context.Context
	reader io.Reader
}

// Read propagates cancellation before and after the underlying descriptor read.
func (r mergeContextReader) Read(data []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	n, err := r.reader.Read(data)
	if contextErr := r.ctx.Err(); contextErr != nil {
		return n, errors.Join(err, contextErr)
	}
	return n, err
}
