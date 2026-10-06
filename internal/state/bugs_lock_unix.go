//go:build unix

package state

import (
	"errors"
	"os"

	"github.com/cordanaLLM/praetor/internal/util"
)

// The lock inode is persistent: unlinking it would split concurrent writers
// across different locks. The release unlocks before it closes, so a
// subprocess holding a copy of the descriptor cannot keep the lock alive.
func lockBugLedger(root *os.Root) (func() error, error) {
	release, busy, err := util.LockPrivateFile(root, bugLockName, os.O_RDWR|os.O_CREATE, "bug ledger")
	if busy {
		return nil, errors.Join(errors.New("bug ledger is busy: another writer holds its lock"), err)
	}
	return release, err
}
