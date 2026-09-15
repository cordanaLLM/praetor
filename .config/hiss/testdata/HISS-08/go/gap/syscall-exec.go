package p

import "syscall"

// Replace overwrites the process image outside the audited exec boundary.
func Replace(name string) error {
	return syscall.Exec(name, nil, nil)
}
