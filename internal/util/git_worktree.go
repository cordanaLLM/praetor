package util

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// GitWorktreePresent detects standard .git directory or linked-worktree metadata
// without converting a failed Git invocation into a non-repository result. It
// validates metadata presence only; callers must propagate subsequent Git errors.
func GitWorktreePresent(ctx context.Context, path string) (bool, error) {
	if ctx == nil {
		return false, errors.New("git worktree detection requires a context")
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return false, err
	}
	info, err := os.Stat(abs)
	if err != nil {
		return false, err
	}
	if !info.IsDir() {
		return false, fmt.Errorf("git worktree root is not a directory: %s", path)
	}
	for depth := 0; depth < 1024; depth++ {
		if err := ctx.Err(); err != nil {
			return false, err
		}
		present, err := gitMetadataPresent(abs)
		if present || err != nil {
			return present, err
		}
		parent := filepath.Dir(abs)
		if parent == abs {
			return false, nil
		}
		abs = parent
	}
	return false, errors.New("git worktree ancestry exceeds 1024 directories")
}

func gitMetadataPresent(path string) (bool, error) {
	_, err := os.Lstat(filepath.Join(path, ".git"))
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	return err == nil, err
}
