//go:build linux

package repairrun

import (
	"os"
	"syscall"

	"github.com/cordanaLLM/praetor/internal/util"
)

func openRegular(root *os.Root, name string) (*os.File, error) {
	return root.OpenFile(name, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
}

// lockState takes the execution lock and returns its release; busy means another run holds
// it. Without create, a missing lock file returns an error matching os.ErrNotExist.
func lockState(root *os.Root, create bool) (func() error, bool, error) {
	flag := os.O_RDWR
	if create {
		flag |= os.O_CREATE
	}
	return util.LockPrivateFile(root, "execution.lock", flag, "repair execution")
}

// ExecutionSupported reports whether repair execution can run on this platform. It needs
// Linux file isolation and locking, which this build provides.
func ExecutionSupported() error { return nil }
