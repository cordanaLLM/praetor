// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package contextopt

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestRemoveSnapshotPositiveExactBytes(t *testing.T) {
	path := filepath.Join(t.TempDir(), "managed.txt")
	if err := os.WriteFile(path, []byte("canonical\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := RemoveSnapshot(t.Context(), path, []byte("canonical\n")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("exact snapshot remains after removal: %v", err)
	}
}

func TestRemoveSnapshotNegativePreservesDriftAndSymlinkTarget(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "managed.txt")
	if err := os.WriteFile(path, []byte("operator edit\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := RemoveSnapshot(t.Context(), path, []byte("canonical\n")); err == nil {
		t.Fatal("drifted snapshot was removed")
	}
	if data, err := os.ReadFile(path); err != nil || string(data) != "operator edit\n" {
		t.Fatalf("drifted snapshot changed: data=%q err=%v", data, err)
	}
	target := filepath.Join(dir, "target.txt")
	if err := os.WriteFile(target, []byte("preserve\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, path); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if err := RemoveSnapshot(t.Context(), path, []byte("preserve\n")); err == nil {
		t.Fatal("symlink snapshot accepted for removal")
	}
	if data, err := os.ReadFile(target); err != nil || string(data) != "preserve\n" {
		t.Fatalf("symlink target changed: data=%q err=%v", data, err)
	}
}
