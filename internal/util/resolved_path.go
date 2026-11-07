package util

import (
	"context"
	"errors"
	"os"
	"path/filepath"
)

// MkdirAllContext creates path and missing parents under the caller's context and permission ceiling.
// It refuses a nil or cancelled context before creation and checks cancellation afterward.
// MkdirSecure supplies permission validation, the zero-permission default, and tightening of existing directories.
func MkdirAllContext(ctx context.Context, path string, perm os.FileMode) error {
	if ctx == nil {
		return errors.New("directory creation requires a context")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := MkdirSecure(path, perm); err != nil {
		return err
	}
	return ctx.Err()
}

// ResolveExistingPath resolves symlinks through the deepest existing ancestor,
// retaining missing trailing components. It is a bounded discovery operation,
// not a security capability: callers must confine the result and pin subsequent I/O.
func ResolveExistingPath(ctx context.Context, path string) (string, error) {
	if ctx == nil {
		return "", errors.New("path resolution requires a context")
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	resolved, err := resolveExistingAncestor(absolute)
	if err != nil {
		return "", err
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	return resolved, nil
}
