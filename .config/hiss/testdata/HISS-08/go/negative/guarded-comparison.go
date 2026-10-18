package main

import (
	"os"
	"syscall"
)

func stop(pid int) error {
	if pid <= 1 || pid == os.Getpid() || pid == syscall.Getpgrp() {
		return nil
	}
	return syscall.Kill(-pid, syscall.SIGKILL)
}
