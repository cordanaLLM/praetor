// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

//go:build unix

package admission

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"

	"github.com/cordanaLLM/praetor/internal/util"
)

// AdmitExecutorLease creates and locks a per-step lease file under the user's runtime directory
// (<dir>/praetor-admit-leases/<executor>/<pid>-<random>.lock) and returns the inclusive count of
// live steps holding a flock lease on that executor across processes, along with a release
// closure that unlocks and removes the lease file when the step ends.
func AdmitExecutorLease(name string) (concurrent int, release func() error, resultErr error) {
	dir, err := executorLeaseDir(name)
	if err != nil {
		return 0, nil, err
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return 0, nil, fmt.Errorf("executor %s: lease directory: %w", name, err)
	}
	defer func() {
		resultErr = errors.Join(resultErr, root.Close())
	}()
	ownName, releaseLease, err := createExecutorLease(name, dir, root)
	if err != nil {
		return 0, nil, err
	}
	count, err := countExecutorLeases(name, ownName, root)
	if err != nil {
		return 0, nil, errors.Join(err, releaseLease())
	}
	return count, releaseLease, nil
}

// executorLeaseDir returns the private local runtime directory path used for holding executor lease files.
func executorLeaseDir(name string) (string, error) {
	if !validExecutorName(name) {
		return "", fmt.Errorf("executor %s: invalid or reserved executor name", name)
	}
	base := os.Getenv("XDG_RUNTIME_DIR")
	if base == "" {
		uid := os.Getuid()
		base = filepath.Join(os.TempDir(), fmt.Sprintf("praetor-admit-%d", uid))
		if err := ensurePrivateRuntimeDir(base, uid); err != nil {
			return "", fmt.Errorf("executor %s: lease directory: %w", name, err)
		}
	}
	dir := filepath.Join(base, executorLeasesDir, name)
	// #nosec G703 -- name matches validExecutorName (alphanumeric, no path separators) under the user's runtime directory.
	// nosemgrep: go.lang.correctness.permissions.file_permission.incorrect-default-permission -- a lease directory needs the owner search bit; 0o700 is least privilege
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("executor %s: lease directory: %w", name, err)
	}
	return dir, nil
}

// ensurePrivateRuntimeDir ensures that the directory exists with 0700 permissions and is owned by the specified UID.
func ensurePrivateRuntimeDir(dir string, uid int) error {
	// nosemgrep: go.lang.correctness.permissions.file_permission.incorrect-default-permission -- a private runtime directory needs the owner search bit; 0o700 is least privilege
	if err := os.Mkdir(dir, 0o700); err != nil && !errors.Is(err, os.ErrExist) {
		return err
	}
	info, err := os.Lstat(dir)
	if err != nil {
		return err
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || !info.IsDir() || info.Mode().Perm() != 0o700 || int(stat.Uid) != uid {
		return fmt.Errorf("%q must be a 0700 directory owned by uid %d", dir, uid)
	}
	return nil
}

// createExecutorLease generates and locks a unique lease file, returning its name and a release function.
func createExecutorLease(name, dir string, root *os.Root) (string, func() error, error) {
	var token [8]byte
	if _, err := rand.Read(token[:]); err != nil {
		return "", nil, fmt.Errorf("executor %s: lease token: %w", name, err)
	}
	ownName := fmt.Sprintf("%d-%s.lock", os.Getpid(), hex.EncodeToString(token[:]))
	tmpName := "." + ownName + ".tmp"
	unlock, busy, err := util.LockPrivateFile(root, tmpName, os.O_RDWR|os.O_CREATE|os.O_EXCL, "executor "+name+" lease")
	if err != nil {
		return "", nil, fmt.Errorf("executor %s: lease directory: %w", name, err)
	}
	if busy {
		return "", nil, fmt.Errorf("executor %s: lease %s already held", name, ownName)
	}
	ownPath := filepath.Join(dir, ownName)
	if err := os.Rename(filepath.Join(dir, tmpName), ownPath); err != nil {
		return "", nil, errors.Join(fmt.Errorf("executor %s: lease directory: %w", name, err), root.Remove(tmpName), unlock())
	}
	var once sync.Once
	var releaseErr error
	release := func() error {
		once.Do(func() {
			removeErr := os.Remove(ownPath)
			if errors.Is(removeErr, os.ErrNotExist) {
				removeErr = nil
			}
			releaseErr = errors.Join(removeErr, unlock())
		})
		return releaseErr
	}
	return ownName, release, nil
}

const maxExecutorLeaseDirEntries = 1024

// countExecutorLeases tallies the active advisory file locks currently held for the specified
// executor, refusing admission when lock files exceed maxExecutorLeaseFiles.
func countExecutorLeases(name, ownName string, root *os.Root) (int, error) {
	peers, err := peerExecutorLeaseNames(name, ownName, root)
	if err != nil {
		return 0, err
	}
	concurrent := 1
	for i := 0; i < len(peers) && i < maxExecutorLeaseFiles; i++ {
		held, err := probeExecutorLease(name, root, peers[i])
		if err != nil {
			return 0, err
		}
		if held {
			concurrent++
		}
	}
	return concurrent, nil
}

// peerExecutorLeaseNames lists peer .lock filenames in root and refuses when total .lock files exceed maxExecutorLeaseFiles.
func peerExecutorLeaseNames(name, ownName string, root *os.Root) ([]string, error) {
	entries, err := readExecutorLeaseEntries(name, root)
	if err != nil {
		return nil, err
	}
	peers := make([]string, 0, min(len(entries), maxExecutorLeaseFiles))
	lockFiles := 0
	for i := 0; i < len(entries) && i < maxExecutorLeaseDirEntries; i++ {
		entryName := entries[i].Name()
		if !strings.HasSuffix(entryName, ".lock") {
			continue
		}
		lockFiles++
		if lockFiles > maxExecutorLeaseFiles {
			return nil, fmt.Errorf("executor %s: %w (%d)", name, ErrExecutorLeaseCap, maxExecutorLeaseFiles)
		}
		if entryName != ownName {
			peers = append(peers, entryName)
		}
	}
	return peers, nil
}

// readExecutorLeaseEntries reads up to maxExecutorLeaseDirEntries plus one directory entries from the lease root.
func readExecutorLeaseEntries(name string, root *os.Root) ([]os.DirEntry, error) {
	f, err := root.Open(".")
	if err != nil {
		return nil, fmt.Errorf("executor %s: lease directory: %w", name, err)
	}
	entries, readErr := f.ReadDir(maxExecutorLeaseDirEntries + 1)
	if errors.Is(readErr, io.EOF) {
		readErr = nil
	}
	if err := errors.Join(readErr, f.Close()); err != nil {
		return nil, fmt.Errorf("executor %s: lease directory: %w", name, err)
	}
	if len(entries) > maxExecutorLeaseDirEntries {
		return nil, fmt.Errorf("executor %s: %w (%d)", name, ErrExecutorLeaseCap, maxExecutorLeaseFiles)
	}
	return entries, nil
}

// probeExecutorLease tests whether a lease file is actively locked, removing stale unheld lock files.
func probeExecutorLease(name string, root *os.Root, entryName string) (bool, error) {
	unlock, busy, err := util.LockPrivateFile(root, entryName, os.O_RDONLY, "executor "+name+" lease")
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("executor %s: %w", name, err)
	}
	if busy {
		return true, nil
	}
	removeErr := root.Remove(entryName)
	if errors.Is(removeErr, os.ErrNotExist) {
		removeErr = nil
	}
	if err := errors.Join(removeErr, unlock()); err != nil {
		return false, fmt.Errorf("executor %s: remove stale lease %s: %w", name, entryName, err)
	}
	return false, nil
}
