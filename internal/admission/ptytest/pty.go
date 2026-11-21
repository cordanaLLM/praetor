// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

// Package ptytest supplies the controlling-terminal fixture shared by admission tests.
package ptytest

import (
	"os"
	"testing"
)

// closeAfterTest retains a terminal descriptor until the fixture has finished.
func closeAfterTest(t *testing.T, f *os.File) {
	t.Helper()
	t.Cleanup(func() {
		if err := f.Close(); err != nil {
			t.Errorf("close %s: %v", f.Name(), err)
		}
	})
}
