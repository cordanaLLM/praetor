package contextopt

import (
	"context"
	"errors"
	"os"
)

// ObserveSnapshot distinguishes initial absence from failure after observation.
// It applies the same bounded no-symlink read contract as ReadSnapshot.
func ObserveSnapshot(ctx context.Context, path string) ([]byte, bool, error) {
	if ctx == nil {
		return nil, false, errors.New("snapshot observation requires a context")
	}
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
	if _, err := os.Lstat(path); errors.Is(err, os.ErrNotExist) {
		return nil, false, nil
	} else if err != nil {
		return nil, false, err
	}
	data, err := ReadSnapshot(ctx, path)
	return data, true, err
}
