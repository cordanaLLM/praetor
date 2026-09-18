// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package workstation

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// lockDirName is the exclusive installation lock: a directory, so mkdir is atomic and a
// killed installer leaves visible evidence rather than a silently stale flag file. This
// mirrors scripts/dev_install.py's own `.praetor-dev-install.lock` rule (section 8.3).
const lockDirName = ".praetor-workstation-install.lock"

func lockPath(binDir string) string { return filepath.Join(binDir, lockDirName) }

// acquireLock takes the installation lock in binDir. The returned release func removes it;
// callers must defer it under every outcome, including a later failure in the same run.
func acquireLock(binDir string) (release func() error, err error) {
	path := lockPath(binDir)
	if mkErr := os.Mkdir(path, 0o700); mkErr != nil {
		if errors.Is(mkErr, os.ErrExist) {
			return nil, fmt.Errorf("%w: %s", ErrLockHeld, path)
		}
		return nil, fmt.Errorf("workstation: acquire installation lock %s: %w", path, mkErr)
	}
	return func() error {
		if rmErr := os.Remove(path); rmErr != nil {
			return fmt.Errorf("workstation: release installation lock %s: %w", path, rmErr)
		}
		return nil
	}, nil
}

// LockHeld reports whether an installation lock is currently held in binDir, for `status`.
// It never takes the lock itself.
func LockHeld(binDir string) bool {
	info, err := os.Stat(lockPath(binDir))
	return err == nil && info.IsDir()
}
