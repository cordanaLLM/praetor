// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

//go:build !unix

package testsupport

import "testing"

// MakeFIFO skips the test: this platform has no mkfifo, so no named pipe can be planted at a
// filesystem path for a reader to open (HISS-21: skipped with the reason stated).
func MakeFIFO(t testing.TB, path string) {
	t.Helper()
	t.Skipf("named pipes cannot be created at %s on this platform (no mkfifo)", path)
}
