package main

import "syscall"

func stop(pid int) error {
	if pid > 0 {
		return syscall.Kill(-pid, syscall.SIGKILL)
	}
	return nil
}
