//go:build !unix

package util

import "os/exec"

// Other systems retain exec.CommandContext's direct-child cancellation and WaitDelay. The
// command stays in this process's console group, so it receives the console's Ctrl-C itself.
func commandBytesCleanup(cmd *exec.Cmd) (start func() error, cleanup func() error) {
	return cmd.Start, func() error { return nil }
}

// TerminateCommandsOnSignal is a no-op here: commands are not moved to a group of their own,
// so a console interrupt already reaches them without help.
func TerminateCommandsOnSignal() {}
