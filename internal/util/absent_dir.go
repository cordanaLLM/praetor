// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package util

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
)

// DirectoryAbsent reports whether err, returned while reading the directory dir, means that
// dir genuinely does not exist -- the case a caller may treat as "nothing to read".
//
// errors.Is(err, fs.ErrNotExist) alone does not answer that on Windows. Reading a directory
// whose path names a regular file, or runs through one ("brain.txt" or "brain.txt\logs"),
// fails there with ERROR_PATH_NOT_FOUND, which Go maps to fs.ErrNotExist. POSIX reports the
// same mistake as ENOTDIR, which it does not map, so a caller that returned early on
// fs.ErrNotExist refused a misconfigured path on Linux and macOS and silently read it as
// empty on Windows. DirectoryAbsent keeps the POSIX answer on every host: it walks up to the
// nearest ancestor that exists and reports absence only when that ancestor is a directory
// other than dir itself.
//
// A walk that exhausts its bound, or meets any error other than fs.ErrNotExist, reports
// false, so the caller surfaces the original error instead of assuming there was nothing.
func DirectoryAbsent(dir string, err error) bool {
	if dir == "" || !errors.Is(err, fs.ErrNotExist) {
		return false
	}
	current := filepath.Clean(dir)
	for i := 0; i < maxPathAncestorWalk; i++ {
		info, statErr := os.Stat(current)
		if statErr == nil {
			// dir itself existing means the read failed for another reason: a file, or a
			// directory that appeared after the read. Neither is absence.
			return info.IsDir() && current != filepath.Clean(dir)
		}
		if !errors.Is(statErr, fs.ErrNotExist) {
			return false
		}
		parent := filepath.Dir(current)
		if parent == current {
			return false
		}
		current = parent
	}
	return false
}
