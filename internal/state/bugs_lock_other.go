//go:build !unix

package state

import (
	"errors"
	"fmt"
	"os"
)

// Portable exclusive claim. Unlike Unix process locks, an interrupted writer
// leaves a claim that must be inspected and removed before another mutation.
func lockBugLedger(root *os.Root) (func() error, error) {
	file, err := root.OpenFile(bugLockName, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return nil, fmt.Errorf("bug ledger busy or interrupted writer claim retained: %w", err)
	}
	return func() error { return errors.Join(file.Close(), root.Remove(bugLockName)) }, nil
}
