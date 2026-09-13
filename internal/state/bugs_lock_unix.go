//go:build unix

package state

import (
	"errors"
	"fmt"
	"os"
	"syscall"
)

// The lock inode is persistent: unlinking it would split concurrent writers
// across different locks. Closing the descriptor releases the process lock.
func lockBugLedger(root *os.Root) (func() error, error) {
	file, err := root.OpenFile(bugLockName, os.O_RDWR|os.O_CREATE|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0o600)
	if err != nil {
		return nil, err
	}
	info, err := file.Stat()
	if err == nil && (!info.Mode().IsRegular() || info.Mode().Perm()&0o077 != 0) {
		err = fmt.Errorf("bug lock must be a private regular file")
	}
	if err != nil {
		return nil, errors.Join(err, file.Close())
	}
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		return nil, errors.Join(fmt.Errorf("bug ledger is busy or cannot be locked: %w", err), file.Close())
	}
	return file.Close, nil
}
