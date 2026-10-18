package main

import "golang.org/x/sys/unix"

func stop(pid int) error {
	return unix.Kill(-pid, unix.SIGKILL)
}
