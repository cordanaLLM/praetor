// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package testsupport

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPinnedGosecDir(t *testing.T) {
	dir := PinnedGosecDir(t)
	bin := filepath.Join(dir, ExecutableName("gosec"))
	if _, err := os.Stat(bin); err != nil {
		t.Fatalf("pinned gosec not found at %s: %v", bin, err)
	}
}
