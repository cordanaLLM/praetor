//go:build unix

package contextopt

import (
	"os"

	"github.com/cordanaLLM/praetor/internal/util"
)

// tryLockSnapshotDirectory makes one attempt at the flock lock on the directory root pins and
// reports busy while another open file description holds it, which is another process for
// every writer that goes through LockDirectory. Locking the existing directory inode leaves no
// disposable lock path that could split writers. The release unlocks before it closes, so a
// subprocess holding a copy of the descriptor cannot keep the lock alive.
func tryLockSnapshotDirectory(root *os.Root) (release func() error, busy bool, err error) {
	file, err := root.Open(".")
	if err != nil {
		return nil, false, err
	}
	return util.LockExclusive(file, "snapshot directory")
}
