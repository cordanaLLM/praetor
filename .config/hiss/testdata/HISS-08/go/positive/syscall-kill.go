package main

import "syscall"

func stop(pid int) error {
	return syscall.Kill(-pid, syscall.SIGKILL)
}
