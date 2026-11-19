// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

//go:build !windows

package state

import "runtime"

// hostArchivePathArgumentLimit excludes the terminating NUL from the native syscall buffer.
// Linux: include/uapi/linux/limits.h PATH_MAX=4096; XNU: bsd/sys/syslimits.h PATH_MAX=1024.
// Other hosts defer to stat rather than assuming a syscall limit.
func hostArchivePathArgumentLimit() int {
	switch runtime.GOOS {
	case "linux":
		return 4095
	case "darwin":
		return 1023
	default:
		return 0
	}
}

// archivePathWithinBounds rejects only cleaned arguments exceeding the host's total byte limit.
func archivePathWithinBounds(path string) bool {
	limit := archivePathArgumentLimit()
	return limit == 0 || len(path) <= limit
}
