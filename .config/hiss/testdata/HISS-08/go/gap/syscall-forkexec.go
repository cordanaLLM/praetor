package p

import "syscall"

// Fork forks and execs outside the audited exec boundary.
func Fork(name string) error {
	_, err := syscall.ForkExec(name, nil, nil)
	return err
}
