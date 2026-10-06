//go:build unix

package contextopt

import (
	"errors"
	"os"

	"github.com/cordanaLLM/praetor/internal/util"
)

// Lock the existing directory inode; no disposable lock path can split writers. The release
// unlocks before it closes, so a subprocess holding a copy of the descriptor cannot keep the
// lock alive.
func lockSnapshotDirectory(root *os.Root) (func() error, error) {
	file, err := root.Open(".")
	if err != nil {
		return nil, err
	}
	release, busy, err := util.LockExclusive(file, "snapshot directory")
	if busy {
		return nil, errors.Join(errors.New("snapshot directory busy: another writer holds its lock"), err)
	}
	return release, err
}
