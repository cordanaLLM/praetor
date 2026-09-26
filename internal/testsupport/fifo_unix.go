// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

//go:build unix

package testsupport

import (
	"syscall"
	"testing"
)

// MakeFIFO creates a named pipe at path with no writer attached, the file a reader must refuse
// rather than open: open(2) on it blocks until a writer appears.
func MakeFIFO(t testing.TB, path string) {
	t.Helper()
	if err := syscall.Mkfifo(path, 0o600); err != nil {
		t.Fatalf("mkfifo %s: %v", path, err)
	}
}
