//go:build unix

package dogfood

import (
	"os"

	"github.com/cordanaLLM/praetor/internal/util"
)

// lockSchedule takes the schedule lock and returns its release; busy means another tick
// holds it. Without create, a missing lock file returns an error matching os.ErrNotExist.
func lockSchedule(root *os.Root, create bool) (func() error, bool, error) {
	flag := os.O_RDONLY
	if create {
		flag = os.O_RDWR | os.O_CREATE
	}
	return util.LockPrivateFile(root, "schedule.lock", flag, "dogfood schedule")
}
