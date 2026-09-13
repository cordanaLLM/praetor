package contextopt

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
)

// OpenDirectory pins a directory without following symlinks in any component.
// The caller owns the returned handle and must close it after bounded operations.
func OpenDirectory(ctx context.Context, path string) (*os.Root, error) {
	if ctx == nil {
		return nil, fmt.Errorf("context is required")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	return openDirectory(ctx, abs)
}

// ReadRootSnapshot reads a bounded regular UTF-8 file from a pinned directory.
// A flat name is required; symlinks and changes during the read are rejected.
func ReadRootSnapshot(ctx context.Context, root *os.Root, name string) ([]byte, error) {
	if ctx == nil || root == nil || !filepath.IsLocal(name) || filepath.Base(name) != name || name == "." {
		return nil, fmt.Errorf("context, directory and flat filename required")
	}
	if err := validatePath(name); err != nil {
		return nil, err
	}
	return snapshotRoot(ctx, root, name)
}

// ReadSnapshot reads one immutable regular UTF-8 file using the same bounded,
// symlink-resistant path traversal as context preparation.
func ReadSnapshot(ctx context.Context, path string) ([]byte, error) {
	if ctx == nil {
		return nil, fmt.Errorf("context is required")
	}
	ctx, cancel := context.WithTimeout(ctx, MaxDuration)
	defer cancel()
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	return snapshot(ctx, abs)
}

// ReadBinarySnapshot reads bounded stable bytes through confined directories.
// Unlike ReadSnapshot it accepts binary content; the same 1 MiB bound applies.
func ReadBinarySnapshot(ctx context.Context, path string) (data []byte, err error) {
	if ctx == nil {
		return nil, fmt.Errorf("context is required")
	}
	ctx, cancel := context.WithTimeout(ctx, MaxDuration)
	defer cancel()
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	if err := validatePath(abs); err != nil {
		return nil, err
	}
	root, err := openDirectory(ctx, filepath.Dir(abs))
	if err != nil {
		return nil, err
	}
	defer func() { err = errors.Join(err, root.Close()) }()
	return snapshotRootBytes(ctx, root, filepath.Base(abs))
}

// WriteArtifacts writes a bounded flat pack to a NEW private directory. On
// failure any partial output is retained for inspection; no existing file is changed.
func WriteArtifacts(ctx context.Context, directory string, files map[string][]byte) (err error) {
	if ctx == nil || directory == "" {
		return fmt.Errorf("context and output directory required")
	}
	ctx, cancel := context.WithTimeout(ctx, MaxDuration)
	defer cancel()
	names, err := artifactNames(files)
	if err != nil {
		return err
	}
	abs, err := filepath.Abs(directory)
	if err != nil {
		return err
	}
	parent, err := openDirectory(ctx, filepath.Dir(abs))
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, parent.Close()) }()
	name := filepath.Base(abs)
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := parent.Mkdir(name, 0o700); err != nil {
		return err
	}
	root, err := childDirectory(ctx, parent, name)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, root.Close()) }()
	if err := writeArtifactEntries(ctx, root, names, files); err != nil {
		return err
	}
	return SyncDirectory(ctx, parent)
}

// writeArtifactEntries persists the pack contents before publishing its parent
// directory entry as a durable recovery copy for a separate live replacement.
func writeArtifactEntries(ctx context.Context, root *os.Root, names []string, files map[string][]byte) error {
	for _, name := range names {
		if err := writePrivate(ctx, root, name, files[name]); err != nil {
			return err
		}
	}
	return SyncDirectory(ctx, root)
}

func artifactNames(files map[string][]byte) ([]string, error) {
	if len(files) == 0 || len(files) > MaxSources {
		return nil, fmt.Errorf("1..64 artifacts required")
	}
	names := make([]string, 0, len(files))
	total := 0
	for name, data := range files {
		if err := validatePath(name); err != nil {
			return nil, err
		}
		if !filepath.IsLocal(name) || filepath.Base(name) != name {
			return nil, fmt.Errorf("artifact name must be a flat filename")
		}
		total += len(data)
		if len(data) > MaxSourceBytes || total > MaxTotalBytes {
			return nil, fmt.Errorf("artifact byte limit exceeded")
		}
		names = append(names, name)
	}
	sort.Strings(names)
	return names, nil
}
