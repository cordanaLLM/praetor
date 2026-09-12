package contextopt

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// EnsureDirectory creates absent directories through pinned parents, rejecting
// symlinks in every component. Existing directory permissions are left unchanged.
func EnsureDirectory(ctx context.Context, path string, mode os.FileMode) (err error) {
	if ctx == nil || mode == 0 || mode&^0o755 != 0 {
		return errors.New("directory creation requires a context and nonzero permissions at most 0755")
	}
	ctx, cancel := context.WithTimeout(ctx, MaxDuration)
	defer cancel()
	abs, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	if err := validatePath(abs); err != nil {
		return err
	}
	return ensureDirectoryPath(ctx, abs, mode)
}

func ensureDirectoryPath(ctx context.Context, abs string, mode os.FileMode) (err error) {
	volume := filepath.VolumeName(abs) + string(filepath.Separator)
	root, err := os.OpenRoot(volume)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, root.Close()) }()
	parts := strings.Split(strings.TrimPrefix(abs, volume), string(filepath.Separator))
	for _, name := range parts {
		if name == "" {
			continue
		}
		if err := ensureChildDirectory(ctx, root, name, mode); err != nil {
			return err
		}
		next, err := childDirectory(ctx, root, name)
		if err != nil {
			return err
		}
		if err := root.Close(); err != nil {
			return errors.Join(err, next.Close())
		}
		root = next
	}
	return ctx.Err()
}

func ensureChildDirectory(ctx context.Context, root *os.Root, name string, mode os.FileMode) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	info, err := root.Lstat(name)
	if errors.Is(err, os.ErrNotExist) {
		if err := root.Mkdir(name, mode); err != nil && !errors.Is(err, os.ErrExist) {
			return err
		}
		return nil
	}
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return fmt.Errorf("directory component must not be a symlink or file: %s", name)
	}
	return nil
}
