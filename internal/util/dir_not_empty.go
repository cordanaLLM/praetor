// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package util

import (
	"errors"
	"runtime"
	"syscall"
)

// errorDirNotEmpty is ERROR_DIR_NOT_EMPTY, Windows' answer to removing a directory that holds
// entries. syscall.ENOTEMPTY there is an invented constant the system never returns.
const errorDirNotEmpty syscall.Errno = 145

// DirectoryNotEmpty reports whether err is a failed removal of a directory that still holds
// entries, on every platform.
func DirectoryNotEmpty(err error) bool {
	if errors.Is(err, syscall.ENOTEMPTY) {
		return true
	}
	var errno syscall.Errno
	return runtime.GOOS == "windows" && errors.As(err, &errno) && errno == errorDirNotEmpty
}
