//go:build !linux

package repairrun

import (
	"errors"
	"os"
)

func openRegular(_ *os.Root, _ string) (*os.File, error) {
	return nil, errors.New("repair execution requires Linux file isolation")
}
func lockState(_ *os.Root, _ bool) (*os.File, bool, error) {
	return nil, false, errors.New("repair execution requires Linux locking")
}

// ExecutionSupported reports whether repair execution can run on this platform, and why not.
// A caller can ask before attempting a run and state the reason, rather than meeting it
// midway as an isolation failure. The error is the one execution itself returns.
func ExecutionSupported() error {
	_, err := openRegular(nil, "")
	return err
}
