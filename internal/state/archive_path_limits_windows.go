// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package state

import "syscall"

// hostArchivePathArgumentLimit returns the Windows extended-path ceiling in UTF-16 units.
// https://learn.microsoft.com/en-us/windows/win32/fileio/maximum-file-path-limitation
// Prefix expansion can lower the effective limit; stat decides every argument below this ceiling.
func hostArchivePathArgumentLimit() int { return 32767 }

// archivePathWithinBounds counts native WTF-16, including unpaired surrogates, excluding NUL.
// Invalid strings proceed to stat so its error preserves all rows.
func archivePathWithinBounds(path string) bool {
	encoded, err := syscall.UTF16FromString(path)
	return err != nil || len(encoded)-1 <= archivePathArgumentLimit()
}
