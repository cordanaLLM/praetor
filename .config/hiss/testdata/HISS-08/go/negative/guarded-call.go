package main

import "syscall"

func assertSafeSignalPID(pid int) {
	if pid <= 1 {
		panic("unsafe")
	}
}

func stop(pid int) error {
	assertSafeSignalPID(pid)
	return syscall.Kill(-pid, syscall.SIGKILL)
}
