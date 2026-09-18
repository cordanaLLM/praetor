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

// ObserveRootSnapshot is ObserveSnapshot for a flat name inside an already
// pinned directory. Initial absence reads as (nil, false, nil); once observed,
// a file that disappears or is replaced during the read is an error.
func ObserveRootSnapshot(ctx context.Context, root *os.Root, name string) ([]byte, bool, error) {
	if ctx == nil || root == nil {
		return nil, false, errors.New("snapshot observation requires a context and pinned directory")
	}
	if _, err := root.Lstat(name); errors.Is(err, os.ErrNotExist) {
		return nil, false, nil
	} else if err != nil {
		return nil, false, err
	}
	data, err := ReadRootSnapshot(ctx, root, name)
	return data, true, err
}
