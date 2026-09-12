//go:build !unix

package util

import "os/exec"

// Other systems retain exec.CommandContext's direct-child cancellation and WaitDelay.
func commandBytesCleanup(_ *exec.Cmd) func() error { return func() error { return nil } }
