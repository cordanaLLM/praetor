// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

//go:build !windows

package util

import (
	"fmt"
	"os"
)

// ArtefactPrivacy reports whether an artefact is readable only by its owner, and
// names what prevents the answer being verified where it cannot be.
//
// Here the POSIX mode is the file's protection, so the mode is the whole answer and
// the second return is always empty. See private_windows.go for the platform where
// the same comparison decides nothing (HISS-21).
func ArtefactPrivacy(_ string, info os.FileInfo) (private bool, unverifiable string) {
	if info == nil {
		return false, ""
	}
	return info.Mode().Perm()&0o077 == 0, ""
}

// TightenCommand is the command that makes the artefact at path private: chmod 700 for a
// directory, chmod 600 for a file.
func TightenCommand(path string, dir bool) string {
	if dir {
		return fmt.Sprintf("chmod 700 '%s'", path)
	}
	return fmt.Sprintf("chmod 600 '%s'", path)
}

// ModeIsProtection reports whether a file's mode is what protects it on this platform.
// On POSIX it is, so an assertion about the mode is an assertion about access.
func ModeIsProtection() bool { return true }
