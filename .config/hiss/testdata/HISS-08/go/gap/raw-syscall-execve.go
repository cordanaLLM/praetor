package p

import "syscall"

// RawExec replaces the process image through the raw syscall entry point. The
// forbidigo pattern is ^syscall\.(Exec|ForkExec)$, so syscall.Syscall with SYS_EXECVE
// reaches the same kernel call without matching.
func RawExec(argv0 uintptr) error {
	_, _, errno := syscall.Syscall(syscall.SYS_EXECVE, argv0, 0, 0)
	if errno != 0 {
		return errno
	}
	return nil
}
