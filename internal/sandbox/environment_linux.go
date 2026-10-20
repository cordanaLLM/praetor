//go:build linux

package sandbox

import (
	"os"
	"strconv"
)

// ManagerEnvironment is the control-plane environment, separate from the clean
// environment inside bubblewrap. It preserves the repair verifier's runtime path.
func ManagerEnvironment() ([]string, error) {
	return []string{"PATH=/usr/bin:/bin", "XDG_RUNTIME_DIR=/run/user/" + strconv.Itoa(os.Getuid())}, nil
}
