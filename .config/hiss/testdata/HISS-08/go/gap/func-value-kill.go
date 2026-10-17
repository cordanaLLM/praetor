package main

import "syscall"

type signaller struct {
	kill func(pid int, sig syscall.Signal) error
}

// Gap: signal through a function value is not checked by go_ast.go
func (s signaller) stop(pid int) error {
	return s.kill(-pid, syscall.SIGKILL)
}
