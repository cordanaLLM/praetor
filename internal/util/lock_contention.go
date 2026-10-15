// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package util

import (
	"errors"
	"os"
)

// WindowsPendingDelete reports whether err is Windows refusing access to a lock file while
// its deletion is still pending, which is another caller's Remove a moment earlier: Windows
// answers a create, a stat or a remove of such a file with ERROR_ACCESS_DENIED, reported as
// os.ErrPermission, where other platforms report the file as existing or already gone.
// TestCorrelationStoreConcurrentReservations failed on windows-latest with "openat .lock:
// Access is denied" and "statat .lock: Access is denied" before this; the snapshot directory
// claim failed the same way (#827).
func WindowsPendingDelete(err error, goos string) bool {
	return goos == "windows" && errors.Is(err, os.ErrPermission)
}

// LockFileContended reports whether an exclusive create of a lock file failed because another
// caller holds the lock: the file exists, or on Windows its deletion is still pending, which
// is the same contention one release later. Either is retried.
func LockFileContended(err error, goos string) bool {
	return errors.Is(err, os.ErrExist) || WindowsPendingDelete(err, goos)
}
