//go:build unix

package util

import (
	"errors"
	"os"
	"os/exec"
	"syscall"
)

// Keep descendants in an owned process group; cancellation and return clean it up.
func commandBytesCleanup(cmd *exec.Cmd) func() error {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return os.ErrProcessDone
		}
		err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		if errors.Is(err, syscall.ESRCH) {
			return os.ErrProcessDone
		}
		return err
	}
	return func() error {
		if cmd.Process == nil {
			return nil
		}
		err := cmd.Cancel()
		if errors.Is(err, os.ErrProcessDone) {
			return nil
		}
		return err
	}
}
