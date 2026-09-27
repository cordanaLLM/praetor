package contextopt

import (
	"context"
	"errors"
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

// WriteSnapshotIn is WriteSnapshot for rel below root, with root as the confinement boundary.
// root must exist; its ancestry is resolved once. Every directory component of rel, existing
// or created, is walked without following a symlink (ensureDirectoryIn), and the file is
// observed and replaced through that pinned parent (ReplaceRootSnapshot), so a symlinked
// directory or file inside the governed tree is refused rather than written through.
// WriteSnapshot resolves the ancestry that already exists and would follow it.
func WriteSnapshotIn(ctx context.Context, root, rel string, data []byte, mode os.FileMode) error {
	return replaceSnapshotIn(ctx, root, rel, data, ReplaceOptions{Mode: mode}, true)
}

// ReplaceSnapshotIn is ReplaceSnapshot for rel below root, confined as WriteSnapshotIn is: it
// publishes only while the file still holds options.Expected, or is still absent when
// options.Exists is false. A caller that planned the content from an earlier read binds the
// write to that read, so a concurrent edit fails the publish instead of being overwritten.
func ReplaceSnapshotIn(ctx context.Context, root, rel string, data []byte, options ReplaceOptions) error {
	return replaceSnapshotIn(ctx, root, rel, data, options, false)
}

// replaceSnapshotIn pins the parent of rel below root, creating absent directories without
// following a symlink, and publishes through ReplaceRootSnapshot. observe binds the publish to
// the current file instead of options.Expected and options.Exists.
func replaceSnapshotIn(ctx context.Context, root, rel string, data []byte, options ReplaceOptions, observe bool) (err error) {
	if err := validateReplacement(ctx, data, options); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, MaxDuration)
	defer cancel()
	dir, err := ensureDirectoryIn(ctx, root, filepath.Dir(rel), 0o755)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, dir.Close()) }()
	name := filepath.Base(rel)
	if observe {
		options.Expected, options.Exists, err = ObserveRootSnapshot(ctx, dir, name)
		if err != nil {
			return err
		}
	}
	return ReplaceRootSnapshot(ctx, dir, name, data, options)
}
