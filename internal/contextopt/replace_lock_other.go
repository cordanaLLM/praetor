//go:build !unix

package contextopt

import (
	"errors"
	"fmt"
	"os"
)

// Portable exclusive claim; an interrupted writer retains the claim for review.
func lockSnapshotDirectory(root *os.Root) (func() error, error) {
	const name = ".praetor-write.lock"
	file, err := root.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return nil, fmt.Errorf("snapshot directory busy or interrupted claim retained: %w", err)
	}
	return func() error { return errors.Join(file.Close(), root.Remove(name)) }, nil
}
