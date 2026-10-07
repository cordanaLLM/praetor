// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

//go:build unix && !linux

package contextopt

import "os"

// directoryLockHolder says that this platform does not name the holder of a flock lock: macOS
// and the BSDs keep no list of flock locks and their owners that a process can read, as Linux
// does in /proc/locks.
func directoryLockHolder(*os.Root) string {
	return "another process (this platform does not report who holds a flock lock)"
}
