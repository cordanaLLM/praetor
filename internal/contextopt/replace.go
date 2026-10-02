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
// Any failure once the staging entry exists retains the staged bytes under the
// private .pending name the error reports, so the partial write can be found and
// recovered; only a decided publish drops them.
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
	return replaceInRoot(ctx, root, filepath.Base(abs), data, options)
}

// ReplaceRootSnapshot is ReplaceSnapshot for a flat name inside an already pinned
// directory. The caller owns how that directory was opened, so a writer that must stay
// confined below a repository root can pin the directory through that root first and
// still publish through the one locked compare-and-swap implementation.
func ReplaceRootSnapshot(ctx context.Context, root *os.Root, name string, data []byte, options ReplaceOptions) error {
	if root == nil || !filepath.IsLocal(name) || filepath.Base(name) != name || name == "." {
		return errors.New("snapshot replacement requires a pinned directory and a flat filename")
	}
	if err := validatePath(name); err != nil {
		return err
	}
	if err := validateReplacement(ctx, data, options); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, MaxDuration)
	defer cancel()
	return replaceInRoot(ctx, root, name, data, options)
}

// replaceInRoot is the locked compare-and-swap publish shared by ReplaceSnapshot and
// ReplaceRootSnapshot: it serializes on root, re-checks the prior snapshot, stages the
// replacement and publishes it.
func replaceInRoot(ctx context.Context, root *os.Root, name string, data []byte, options ReplaceOptions) (err error) {
	unlock, err := LockDirectory(ctx, root)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, unlock()) }()
	mode, err := verifyReplacement(ctx, root, name, options)
	if err != nil {
		return err
	}
	stage := ".praetor-" + rand.Text() + ".pending"
	file, err := stageSnapshot(ctx, root, stage, data)
	if err != nil {
		return fmt.Errorf("stage snapshot (inspect %s): %w", stagedPath(root, stage), err)
	}
	defer func() { err = errors.Join(err, file.Close()) }()
	if err := publishSnapshot(ctx, root, name, stage, file, mode, options); err != nil {
		err = errors.Join(err, file.Chmod(0o600))
		return fmt.Errorf("publish snapshot (inspect %s): %w", stagedPath(root, stage), err)
	}
	return SyncDirectory(ctx, root)
}

// stagedPath names the staging entry the way the operator has to look for it. Every
// publisher reports a retained partial write through this one form (HISS-19); an error
// that names the target ledger instead leaves an orphan nothing can identify, because the
// staging name is random and the audit only knows the five ledger names.
func stagedPath(root *os.Root, stage string) string {
	return filepath.Join(root.Name(), stage)
}

// CreateRootSnapshot writes bounded UTF-8 text under name inside an already pinned
// directory, and only when that name is still free. The content is staged under a private
// random name, written and synced there, and published with an exclusive hard link, so no
// reader and no concurrent creator ever observes name holding a half-written file: it is
// either absent or complete. It reports whether this call published it; losing the race to
// another creator is not an error, because the file that won is the file this call would
// have written. An existing name is left byte-for-byte alone.
func CreateRootSnapshot(ctx context.Context, root *os.Root, name string, data []byte, mode os.FileMode) (created bool, err error) {
	if err := validateReplacement(ctx, data, ReplaceOptions{Mode: mode}); err != nil {
		return false, err
	}
	if root == nil || !filepath.IsLocal(name) || filepath.Base(name) != name || name == "." {
		return false, errors.New("snapshot creation requires a pinned directory and a flat filename")
	}
	if err := validatePath(name); err != nil {
		return false, err
	}
	ctx, cancel := context.WithTimeout(ctx, MaxDuration)
	defer cancel()
	stage := ".praetor-" + rand.Text() + ".pending"
	file, err := stageSnapshot(ctx, root, stage, data)
	if err != nil {
		return false, fmt.Errorf("stage snapshot %s (inspect %s): %w", name, stagedPath(root, stage), err)
	}
	defer func() { err = errors.Join(err, file.Close()) }()
	if err := sealStage(ctx, file, mode); err != nil {
		return false, fmt.Errorf("publish snapshot %s (inspect %s): %w", name, stagedPath(root, stage), err)
	}
	created, err = linkStagedSnapshot(root, name, stage)
	if err != nil {
		return false, fmt.Errorf("publish snapshot %s (inspect %s): %w", name, stagedPath(root, stage), err)
	}
	return created, SyncDirectory(ctx, root)
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
	if err := sealStage(ctx, file, mode); err != nil {
		return err
	}
	if options.Exists {
		return root.Rename(stage, name)
	}
	created, err := linkStagedSnapshot(root, name, stage)
	if err != nil {
		return err
	}
	if !created {
		return fmt.Errorf("snapshot already exists: %s", name)
	}
	return nil
}

// sealStage makes the staged bytes and their published mode durable while the file still
// has no name a reader can reach, so every publish links a file that is already complete.
func sealStage(ctx context.Context, file *os.File, mode os.FileMode) error {
	return errors.Join(ctx.Err(), file.Chmod(mode), file.Sync())
}

// linkStagedSnapshot publishes the staged entry under name with an exclusive hard link.
// It is the one create path ReplaceSnapshot and CreateRootSnapshot both take (HISS-19);
// two copies of it drifted apart on exactly the two questions below.
//
// It reports whether this call published name. os.ErrExist is not a failure: another
// creator won the race, and the file that won is the file this call would have written.
// A decided outcome, published or lost, drops the staging entry; a link that failed for
// any other reason leaves it, because the staged bytes are then the only complete copy
// and the caller names them in its error.
func linkStagedSnapshot(root *os.Root, name, stage string) (bool, error) {
	linkErr := root.Link(stage, name)
	if linkErr != nil && !errors.Is(linkErr, os.ErrExist) {
		return false, fmt.Errorf("link snapshot %s: %w", name, linkErr)
	}
	return linkErr == nil, root.Remove(stage)
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
