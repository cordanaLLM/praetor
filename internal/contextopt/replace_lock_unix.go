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
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		return nil, errors.Join(fmt.Errorf("snapshot directory busy or un-lockable: %w", err), file.Close())
	}
	return file.Close, nil
}
