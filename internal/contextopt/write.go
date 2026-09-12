package contextopt

import (
	"context"
	"os"
	"path/filepath"
)

// WriteSnapshot observes and replaces a current generated artifact, creating its
// parents without following symlinks. Use ReplaceSnapshot when the caller must
// bind publication to an earlier reviewed snapshot instead of the current file.
func WriteSnapshot(ctx context.Context, path string, data []byte, mode os.FileMode) error {
	if err := validateReplacement(ctx, data, ReplaceOptions{Mode: mode}); err != nil {
		return err
	}
	if err := EnsureDirectory(ctx, filepath.Dir(path), 0o755); err != nil {
		return err
	}
	before, exists, err := ObserveSnapshot(ctx, path)
	if err != nil {
		return err
	}
	return ReplaceSnapshot(ctx, path, data, ReplaceOptions{Expected: before, Exists: exists, Mode: mode})
}
