// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package contextopt

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// RemoveSnapshot deletes one regular text file only when its current bytes equal expected.
// The parent directory is pinned and locked, and a second identity check rejects symlinks,
// drift, or replacement observed before unlink. Like ReplaceSnapshot, an external process can
// still replace the same directory entry after the final check; that is the filesystem boundary.
func RemoveSnapshot(ctx context.Context, path string, expected []byte) (err error) {
	bounded, cancel, abs, err := prepareSnapshotRemoval(ctx, path, expected)
	if err != nil {
		return err
	}
	defer cancel()
	return removePinnedSnapshot(bounded, abs, expected)
}

func prepareSnapshotRemoval(
	ctx context.Context, path string, expected []byte,
) (context.Context, context.CancelFunc, string, error) {
	if ctx == nil {
		return nil, nil, "", errors.New("snapshot removal requires a context")
	}
	if len(expected) > MaxSourceBytes {
		return nil, nil, "", fmt.Errorf("snapshot removal exceeds %d bytes", MaxSourceBytes)
	}
	if err := errors.Join(ctx.Err(), validateText(expected)); err != nil {
		return nil, nil, "", err
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, nil, "", err
	}
	if err := validatePath(abs); err != nil {
		return nil, nil, "", err
	}
	bounded, cancel := context.WithTimeout(ctx, MaxDuration)
	return bounded, cancel, abs, nil
}

func removePinnedSnapshot(ctx context.Context, abs string, expected []byte) (err error) {
	root, err := OpenDirectory(ctx, filepath.Dir(abs))
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, root.Close()) }()
	unlock, err := LockDirectory(ctx, root)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, unlock()) }()
	name := filepath.Base(abs)
	before, err := root.Lstat(name)
	if err != nil {
		return err
	}
	current, err := ReadRootSnapshot(ctx, root, name)
	if err != nil {
		return err
	}
	if !bytes.Equal(current, expected) {
		return fmt.Errorf("snapshot differs before removal: %s", name)
	}
	confirmed, err := root.Lstat(name)
	if err != nil {
		return err
	}
	if !os.SameFile(before, confirmed) {
		return fmt.Errorf("snapshot changed before removal: %s", name)
	}
	if err := root.Remove(name); err != nil {
		return err
	}
	return SyncDirectory(ctx, root)
}
