package main

import (
	"context"
	"fmt"

	"github.com/cordanaLLM/praetor/internal/contextopt"
	"github.com/cordanaLLM/praetor/internal/strictjson"
	"github.com/cordanaLLM/praetor/internal/util"
)

// readMergeJSON reuses contextopt.ReadSnapshot for bounded UTF-8 review reads,
// then checks the shared byte budget before decoding or retaining a review.
func readMergeJSON(ctx context.Context, path string, target any, remaining *int) error {
	limit := min(mergeFileLimit, *remaining)
	if limit <= 0 {
		return fmt.Errorf("merge input byte budget exceeded")
	}
	data, err := contextopt.ReadSnapshot(ctx, path)
	if err != nil {
		return fmt.Errorf("read %q: %w", path, err)
	}
	if len(data) > limit {
		return fmt.Errorf("merge input byte budget exceeded: %w", util.ErrFileTooLarge)
	}
	*remaining -= len(data)
	if err := strictjson.Decode(data, target, strictjson.Options{MaxBytes: limit, MaxDepth: 16}); err != nil {
		return fmt.Errorf("decode %q: %w", path, err)
	}
	return ctx.Err()
}
