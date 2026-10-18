package main

import (
	"os/exec"
	"syscall"
)

func stopHelper(helper *exec.Cmd, group bool) error {
	target := helper.Process.Pid
	if group {
		target = -target
	}
	return syscall.Kill(target, syscall.SIGKILL)
}
