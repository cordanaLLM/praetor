package contextopt

import (
	"bytes"
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
)

// ReplaceOptions binds a write to observed contents or explicit absence. Mode is
// a permission ceiling: existing restrictive permissions are never widened.
type ReplaceOptions struct {
	Expected []byte
	Exists   bool
	Mode     os.FileMode
}

// ReplaceSnapshot publishes bounded UTF-8 text through a pinned parent directory.
// Cooperative writers serialize on that directory; replacement checks the prior
// snapshot again before rename. External writers can still race the final check.
// Creation uses an exclusive hard link and never replaces an existing name.
// A failed staged write is retained under its reported private .pending name.
func ReplaceSnapshot(ctx context.Context, path string, data []byte, options ReplaceOptions) (err error) {
	if err := validateReplacement(ctx, data, options); err != nil {
		return err
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
	mode, err := verifyReplacement(ctx, root, name, options)
	if err != nil {
		return err
	}
	stage := ".praetor-" + rand.Text() + ".pending"
	file, err := stageSnapshot(ctx, root, stage, data)
	if err != nil {
		return fmt.Errorf("stage snapshot (inspect %s): %w", filepath.Join(filepath.Dir(abs), stage), err)
	}
	defer func() { err = errors.Join(err, file.Close()) }()
	if err := publishSnapshot(ctx, root, name, stage, file, mode, options); err != nil {
		err = errors.Join(err, file.Chmod(0o600))
		return fmt.Errorf("publish snapshot (inspect %s): %w", filepath.Join(filepath.Dir(abs), stage), err)
	}
	return SyncDirectory(ctx, root)
}

// LockDirectory serializes cooperating operations on an already pinned
// directory. The returned release function must be called; contention fails fast.
func LockDirectory(ctx context.Context, root *os.Root) (func() error, error) {
	if ctx == nil || root == nil {
		return nil, errors.New("directory lock requires a context and pinned root")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return lockSnapshotDirectory(root)
}

func validateReplacement(ctx context.Context, data []byte, options ReplaceOptions) error {
	if ctx == nil {
		return errors.New("snapshot replacement requires a context")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if len(data) > MaxSourceBytes || len(options.Expected) > MaxSourceBytes {
		return fmt.Errorf("snapshot replacement exceeds %d bytes", MaxSourceBytes)
	}
	if options.Mode == 0 || options.Mode&^0o644 != 0 {
		return errors.New("snapshot mode must be nonzero owner read/write and optional group/other read bits")
	}
	if !options.Exists && len(options.Expected) != 0 {
		return errors.New("absent snapshot cannot have expected content")
	}
	return errors.Join(validateText(data), validateText(options.Expected))
}

func verifyReplacement(ctx context.Context, root *os.Root, name string, options ReplaceOptions) (os.FileMode, error) {
	info, err := root.Lstat(name)
	if errors.Is(err, os.ErrNotExist) && !options.Exists {
		return options.Mode, nil
	}
	if err != nil {
		return 0, err
	}
	if !options.Exists {
		return 0, fmt.Errorf("snapshot already exists: %s", name)
	}
	current, err := ReadRootSnapshot(ctx, root, name)
	if err != nil {
		return 0, err
	}
	if !bytes.Equal(current, options.Expected) {
		return 0, fmt.Errorf("snapshot changed before replacement: %s", name)
	}
	return options.Mode & info.Mode().Perm(), nil
}

func stageSnapshot(ctx context.Context, root *os.Root, name string, data []byte) (*os.File, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	file, err := root.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return nil, err
	}
	_, writeErr := file.Write(data)
	if err := errors.Join(writeErr, file.Sync()); err != nil {
		return nil, errors.Join(err, file.Close())
	}
	return file, nil
}

func publishSnapshot(ctx context.Context, root *os.Root, name, stage string, file *os.File, mode os.FileMode, options ReplaceOptions) error {
	if _, err := verifyReplacement(ctx, root, name, options); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := errors.Join(file.Chmod(mode), file.Sync()); err != nil {
		return err
	}
	if options.Exists {
		return root.Rename(stage, name)
	}
	if err := root.Link(stage, name); err != nil {
		return err
	}
	return root.Remove(stage)
}

// SyncDirectory persists directory entry changes through an already pinned root.
func SyncDirectory(ctx context.Context, root *os.Root) error {
	if ctx == nil || root == nil {
		return errors.New("directory sync requires a context and pinned root")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	dir, err := root.Open(".")
	if err != nil {
		return err
	}
	// Directory fsync is POSIX-only. Windows refuses it on a directory handle with
	// "Zugriff verweigert"/access denied, which failed the commit-msg live-state gate on every
	// Windows commit. Closing is the whole contract there: NTFS metadata durability does not
	// depend on a caller-issued directory flush the way a POSIX filesystem's does.
	if runtime.GOOS == "windows" {
		return dir.Close()
	}
	return errors.Join(dir.Sync(), dir.Close())
}
