//go:build unix

package dogfood

import (
	"errors"
	"os"
	"syscall"
)

func lockSchedule(root *os.Root, create bool) (*os.File, bool, error) {
	flags := os.O_RDONLY | syscall.O_NONBLOCK | syscall.O_NOFOLLOW
	if create {
		flags = os.O_RDWR | os.O_CREATE | syscall.O_NONBLOCK | syscall.O_NOFOLLOW
	}
	file, err := root.OpenFile("schedule.lock", flags, 0o600)
	if err != nil {
		return nil, false, err
	}
	info, err := file.Stat()
	if err == nil && (!info.Mode().IsRegular() || info.Mode().Perm()&0o077 != 0) {
		err = errors.New("schedule lock must be a private regular file")
	}
	if err != nil {
		return nil, false, errors.Join(err, file.Close())
	}
	err = syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
	if errors.Is(err, syscall.EWOULDBLOCK) || errors.Is(err, syscall.EAGAIN) {
		return nil, true, file.Close()
	}
	if err != nil {
		return nil, false, errors.Join(err, file.Close())
	}
	return file, false, nil
}
