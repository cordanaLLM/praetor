//go:build unix

package contextopt

import (
	"errors"
	"fmt"
	"os"
	"syscall"
)

// Lock the existing directory inode; no disposable lock path can split writers.
func lockSnapshotDirectory(root *os.Root) (func() error, error) {
	file, err := root.Open(".")
	if err != nil {
		return nil, err
	}
	conn, err := file.SyscallConn()
	if err != nil {
		return nil, errors.Join(err, file.Close())
	}
	var lockErr error
	ctlErr := conn.Control(func(fd uintptr) {
		lockErr = syscall.Flock(int(fd), syscall.LOCK_EX|syscall.LOCK_NB)
	})
	if err := errors.Join(ctlErr, lockErr); err != nil {
		return nil, errors.Join(fmt.Errorf("snapshot directory busy or un-lockable: %w", err), file.Close())
	}
	return func() error {
		var unlockErr error
		conn, ctlErr := file.SyscallConn()
		if ctlErr == nil {
			ctlErr = conn.Control(func(fd uintptr) {
				unlockErr = syscall.Flock(int(fd), syscall.LOCK_UN)
			})
		}
		if err := errors.Join(ctlErr, unlockErr, file.Close()); err != nil {
			return fmt.Errorf("release snapshot directory lock: %w", err)
		}
		return nil
	}, nil
}
