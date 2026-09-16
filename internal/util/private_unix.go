// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

//go:build !windows

package util

import "os"

// ArtefactPrivacy reports whether an artefact is readable only by its owner, and
// names what prevents the answer being verified where it cannot be.
//
// Here the POSIX mode is the file's protection, so the mode is the whole answer and
// the second return is always empty. See private_windows.go for the platform where
// the same comparison decides nothing (HISS-21).
func ArtefactPrivacy(info os.FileInfo) (private bool, unverifiable string) {
	if info == nil {
		return false, ""
	}
	return info.Mode().Perm()&0o077 == 0, ""
}
