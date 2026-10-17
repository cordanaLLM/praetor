package main

import (
	"os/exec"
	"syscall"
)

func stopCmd(cmd *exec.Cmd) error {
	return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
}
