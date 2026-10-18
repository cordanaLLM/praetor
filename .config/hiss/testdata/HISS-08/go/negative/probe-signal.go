package main

import "syscall"

func probe(pid int) error {
	return syscall.Kill(-pid, 0)
}
