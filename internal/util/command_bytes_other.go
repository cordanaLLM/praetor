//go:build !unix

package util

import "os/exec"

// Other systems retain exec.CommandContext's direct-child cancellation and WaitDelay. Windows
// has no SIGTERM to ask a console program to stop, so cancellation terminates the direct child
// at once, without the grace Unix gives. The command stays in this process's console group,
// so the console's Ctrl-C reaches it directly and git runs its own cleanup there.
func commandBytesCleanup(cmd *exec.Cmd) (start func() error, cleanup func() error) {
	return cmd.Start, func() error { return nil }
}

// TerminateCommandsOnSignal is a no-op here and never calls the exit it is given: commands are
// not moved to a group of their own, so a console interrupt already reaches them without help.
func TerminateCommandsOnSignal(func(code int)) {}
