// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package util

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// Positive and negative: removing a directory that still holds a file is reported as not empty
// on every platform; Windows answers ERROR_DIR_NOT_EMPTY, which is not syscall.ENOTEMPTY there.
// Any other failure is not.
func TestDirectoryNotEmpty_3D(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "kept")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "player.js"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	err := os.Remove(dir)
	if err == nil || !DirectoryNotEmpty(err) {
		t.Fatalf("remove a non-empty directory: %v, DirectoryNotEmpty = false", err)
	}
	if missing := os.Remove(filepath.Join(dir, "absent")); DirectoryNotEmpty(missing) {
		t.Fatalf("a missing entry was reported as a non-empty directory: %v", missing)
	}
	if DirectoryNotEmpty(nil) || DirectoryNotEmpty(errors.New("other")) {
		t.Fatal("nil or an unrelated error was reported as a non-empty directory")
	}
}
