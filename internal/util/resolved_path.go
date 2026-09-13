package util

import (
	"context"
	"errors"
	"path/filepath"
)

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
