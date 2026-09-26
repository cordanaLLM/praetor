package contextopt

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"time"
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
func ReadBinarySnapshot(ctx context.Context, path string) ([]byte, error) {
	var buffer bytes.Buffer
	if _, err := copyBinarySnapshot(ctx, path, &buffer, MaxSourceBytes, MaxDuration); err != nil {
		return nil, err
	}
	return buffer.Bytes(), nil
}

// MaxDigestDuration bounds one DigestBinarySnapshot call. A streamed digest holds no
// bytes in memory, so its byte bound is the caller's and far above MaxSourceBytes; the
// longer window keeps a large artifact on a slow disk from timing out.
const MaxDigestDuration = 2 * time.Minute

// DigestBinarySnapshot streams one regular file of at most limit bytes through
// SHA-256 and returns the lowercase hex digest and the byte count. It uses the same
// confined, symlink-resistant, change-detecting traversal as ReadBinarySnapshot
// without holding the content in memory, so it can digest files far larger than
// MaxSourceBytes. A file that grows past limit or changes during the read is refused.
func DigestBinarySnapshot(ctx context.Context, path string, limit int64) (sha256Hex string, size int64, err error) {
	if limit < 1 {
		return "", 0, fmt.Errorf("digest byte limit must be positive, got %d", limit)
	}
	hasher := sha256.New()
	size, err = copyBinarySnapshot(ctx, path, hasher, limit, MaxDigestDuration)
	if err != nil {
		return "", 0, err
	}
	return hex.EncodeToString(hasher.Sum(nil)), size, nil
}

// copyBinarySnapshot streams one confined regular file of at most limit bytes into dst
// within timeout; dst may hold a partial copy when an error is returned.
func copyBinarySnapshot(ctx context.Context, path string, dst io.Writer, limit int64, timeout time.Duration) (size int64, err error) {
	if ctx == nil {
		return 0, fmt.Errorf("context is required")
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	abs, err := filepath.Abs(path)
	if err != nil {
		return 0, err
	}
	if err := validatePath(abs); err != nil {
		return 0, err
	}
	root, err := openDirectory(ctx, filepath.Dir(abs))
	if err != nil {
		return 0, err
	}
	defer func() { err = errors.Join(err, root.Close()) }()
	return snapshotRootCopy(ctx, root, filepath.Base(abs), dst, limit)
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
