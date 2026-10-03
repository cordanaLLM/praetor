// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

//go:build unix

package util

import (
	"errors"
	"fmt"
	"os"
	"sync/atomic"
	"syscall"
)

// LockExclusive takes a non-blocking exclusive advisory lock, flock(LOCK_EX|LOCK_NB), on
// file and hands the file to the returned release. name says which lock this is; every
// error the helper returns names it.
//
// The release runs flock(LOCK_UN) before it closes the file. Closing alone is not enough:
// a flock lock belongs to the open file description, and a subprocess that another
// goroutine forks keeps a copy of every descriptor until its exec closes them. While that
// copy exists, a close-only release leaves the lock held and the next caller is told the
// lock is busy (#732, #740). Unlocking drops the lock for every copy at once.
//
// Both calls reach the descriptor through SyscallConn().Control, which holds it open for
// the call. They never use File.Fd, which the os package documents as stopping the file's
// deadlines, nor a descriptor number saved at acquire time, which after the close may
// belong to another file. A second release touches nothing and reports os.ErrClosed.
//
// On failure the file is closed. busy reports that another holder has the lock; err then
// carries only a failure to close the file, so a caller can treat busy as an outcome and
// word it in its own message.
func LockExclusive(file *os.File, name string) (release func() error, busy bool, err error) {
	if file == nil {
		return nil, false, fmt.Errorf("lock %s: %w", name, os.ErrInvalid)
	}
	lockErr := controlFlock(file, syscall.LOCK_EX|syscall.LOCK_NB)
	if lockErr == nil {
		held := &heldLock{file: file, name: name}
		return held.release, false, nil
	}
	closeErr := file.Close()
	if errors.Is(lockErr, syscall.EWOULDBLOCK) || errors.Is(lockErr, syscall.EAGAIN) {
		if closeErr != nil {
			return nil, true, fmt.Errorf("close busy %s lock: %w", name, closeErr)
		}
		return nil, true, nil
	}
	return nil, false, errors.Join(fmt.Errorf("lock %s: %w", name, lockErr), closeErr)
}

// LockPrivateFile opens path inside root as a lock file and takes LockExclusive on it. flag
// carries the access mode, plus os.O_CREATE when the caller may create the file. The open
// adds O_NONBLOCK, so a FIFO planted at path is refused rather than waited on. The file must
// be a regular file that only its owner can reach, and path must name it directly: os.Root
// follows a link that stays inside root even under O_NOFOLLOW, so a link at path is refused
// after the open by comparing it with the opened file. The lock inode persists: removing it
// would let two writers lock two different files.
//
// A missing file returns an error matching os.ErrNotExist, so a caller that opens without
// os.O_CREATE can tell an absent lock from a broken one. busy and release are those of
// LockExclusive.
func LockPrivateFile(root *os.Root, path string, flag int, name string) (release func() error, busy bool, err error) {
	if root == nil {
		return nil, false, fmt.Errorf("open %s lock: %w", name, os.ErrInvalid)
	}
	file, err := root.OpenFile(path, flag|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0o600)
	if err != nil {
		return nil, false, fmt.Errorf("open %s lock: %w", name, err)
	}
	if err := checkPrivateLock(root, path, file); err != nil {
		return nil, false, errors.Join(fmt.Errorf("%s lock %q: %w", name, path, err), file.Close())
	}
	return LockExclusive(file, name)
}

// checkPrivateLock accepts file when it is a regular file only its owner can reach and path
// names it without a link.
func checkPrivateLock(root *os.Root, path string, file *os.File) error {
	opened, err := file.Stat()
	if err != nil {
		return err
	}
	if !opened.Mode().IsRegular() || opened.Mode().Perm()&0o077 != 0 {
		return errors.New("must be a private regular file")
	}
	named, err := root.Lstat(path)
	if err != nil {
		return err
	}
	if !os.SameFile(opened, named) {
		return errors.New("must be named directly, not through a link")
	}
	return nil
}

// heldLock is one acquired lock; released makes the release run at most once.
type heldLock struct {
	file     *os.File
	name     string
	released atomic.Bool
}

func (l *heldLock) release() error {
	if !l.released.CompareAndSwap(false, true) {
		return fmt.Errorf("release %s lock: already released: %w", l.name, os.ErrClosed)
	}
	if err := errors.Join(controlFlock(l.file, syscall.LOCK_UN), l.file.Close()); err != nil {
		return fmt.Errorf("release %s lock: %w", l.name, err)
	}
	return nil
}

// controlFlock runs flock(how) on file's descriptor while the runtime holds it open.
func controlFlock(file *os.File, how int) error {
	conn, err := file.SyscallConn()
	if err != nil {
		return err
	}
	var flockErr error
	ctlErr := conn.Control(func(fd uintptr) {
		flockErr = syscall.Flock(int(fd), how)
	})
	return errors.Join(ctlErr, flockErr)
}
