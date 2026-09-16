//go:build linux

package repairrun

import (
	"errors"
	"os"
	"syscall"
)

func openRegular(root *os.Root, name string) (*os.File, error) {
	return root.OpenFile(name, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
}

func lockState(root *os.Root, create bool) (*os.File, bool, error) {
	flags := os.O_RDWR | syscall.O_NOFOLLOW | syscall.O_NONBLOCK
	if create {
		flags |= os.O_CREATE
	}
	file, err := root.OpenFile("execution.lock", flags, 0o600)
	if err != nil {
		return nil, false, err
	}
	info, err := file.Stat()
	if err == nil && (!info.Mode().IsRegular() || info.Mode().Perm()&0o077 != 0) {
		err = errors.New("execution lock must be a private regular file")
	}
	if err != nil {
		return nil, false, errors.Join(err, file.Close())
	}
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, true, file.Close()
		}
		return nil, false, errors.Join(err, file.Close())
	}
	return file, false, nil
}

// ExecutionSupported reports whether repair execution can run on this platform. It needs
// Linux file isolation and locking, which this build provides.
func ExecutionSupported() error { return nil }
