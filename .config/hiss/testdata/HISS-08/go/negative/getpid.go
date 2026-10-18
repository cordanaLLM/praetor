package main

import (
	"os"
	"syscall"
)

func signalSelf() error {
	return syscall.Kill(os.Getpid(), syscall.SIGTERM)
}

func signalSelfGroup() error {
	return syscall.Kill(-syscall.Getpid(), syscall.SIGTERM)
}
