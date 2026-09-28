//go:build !windows

package util

import "os/exec"

// prepareBatchFile changes nothing here: only Windows starts a batch file through cmd.exe, so
// only command_batch_windows.go builds a cmd.exe command line for one.
func prepareBatchFile(*exec.Cmd) error { return nil }
