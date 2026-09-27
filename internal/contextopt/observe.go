package contextopt

import (
	"context"
	"errors"
	"os"
	"path/filepath"
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

// ObserveSnapshotIn is ObserveRootSnapshot for rel below root, walked as WriteSnapshotIn and
// ReplaceSnapshotIn walk it but creating nothing: a component of rel that is a symlink or not a
// directory, and a leaf that is a symlink, not a regular UTF-8 text file or above
// MaxSourceBytes, are refused, which are the files those writers refuse to replace. An absent
// directory or file reads as (nil, false, nil). A caller that checks a target here before its
// first write fails before writing anything instead of at the write.
func ObserveSnapshotIn(ctx context.Context, root, rel string) (data []byte, exists bool, err error) {
	if ctx == nil {
		return nil, false, errors.New("snapshot observation requires a context")
	}
	ctx, cancel := context.WithTimeout(ctx, MaxDuration)
	defer cancel()
	dir, err := OpenDirectoryIn(ctx, root, filepath.Dir(rel))
	if errors.Is(err, os.ErrNotExist) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	defer func() { err = errors.Join(err, dir.Close()) }()
	return ObserveRootSnapshot(ctx, dir, filepath.Base(rel))
}
