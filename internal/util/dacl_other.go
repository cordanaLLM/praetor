// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

//go:build !windows

package util

import (
	"fmt"
	"os"
)

// restrictFileAccess is a no-op here: the mode the caller already applied is the file's
// protection. dacl_windows.go sets the DACL the mode stands for there (#101).
func restrictFileAccess(*os.File, os.FileMode) error { return nil }

// OwnerOnlyAccess reports whether the mode of path grants no group or other access; otherwise
// it names the mode.
func OwnerOnlyAccess(path string) (bool, string, error) {
	info, err := os.Stat(path)
	if err != nil {
		return false, "", err
	}
	if perm := info.Mode().Perm(); perm&0o077 != 0 {
		return false, fmt.Sprintf("mode %#o", perm), nil
	}
	return true, "", nil
}
