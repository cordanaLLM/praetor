// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package util

import (
	"os"
	"testing"
)

// TestLockFileContended_3D: positive, an existing lock file is contention on every platform and a
// pending-delete refusal is contention on Windows; negative, the same permission error elsewhere
// and any other error are real failures; boundary, no error is not contention.
func TestLockFileContended_3D(t *testing.T) {
	exist := &os.PathError{Op: "openat", Path: ".lock", Err: os.ErrExist}
	denied := &os.PathError{Op: "openat", Path: ".lock", Err: os.ErrPermission}
	for _, tc := range []struct {
		name string
		err  error
		goos string
		want bool
	}{
		{"existing lock on linux", exist, "linux", true},
		{"existing lock on windows", exist, "windows", true},
		{"pending delete on windows", denied, "windows", true},
		{"permission denied on linux", denied, "linux", false},
		{"other error on windows", &os.PathError{Op: "openat", Path: ".lock", Err: os.ErrInvalid}, "windows", false},
		{"no error", nil, "windows", false},
	} {
		if got := LockFileContended(tc.err, tc.goos); got != tc.want {
			t.Errorf("%s: LockFileContended = %v, want %v", tc.name, got, tc.want)
		}
	}
	// The stat and remove paths use WindowsPendingDelete alone: an existing file is no
	// pending delete, and only Windows answers one with a permission error.
	if !WindowsPendingDelete(denied, "windows") || WindowsPendingDelete(denied, "darwin") || WindowsPendingDelete(exist, "windows") {
		t.Error("WindowsPendingDelete must hold for a permission error on windows only")
	}
}
