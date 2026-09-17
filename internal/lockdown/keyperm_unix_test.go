// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

//go:build !windows

package lockdown

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func unixKeyAt(t *testing.T, mode os.FileMode) (string, os.FileInfo) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "receipt.key")
	if err := os.WriteFile(path, []byte("00"), mode); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, mode); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	return path, info
}

// Positive: the POSIX mode is the file's protection here, so 0600 is accepted.
func TestVerifyKeyPermissionsAcceptsOwnerOnlyMode(t *testing.T) {
	path, info := unixKeyAt(t, 0o600)
	if err := verifyKeyPermissions(path, info); err != nil {
		t.Fatalf("owner-only key rejected: %v", err)
	}
}

// Negative: any group or other access is refused. Splitting the check per platform
// must not weaken it where the mode is meaningful.
func TestVerifyKeyPermissionsRefusesGroupOrOtherAccess(t *testing.T) {
	for _, mode := range []os.FileMode{0o640, 0o604, 0o660, 0o666, 0o644} {
		path, info := unixKeyAt(t, mode)
		if err := verifyKeyPermissions(path, info); !errors.Is(err, ErrInsecureKeyPerm) {
			t.Fatalf("mode %#o accepted: %v", mode, err)
		}
	}
}

// Boundary: 0400 excludes group and other too, so it is accepted.
func TestVerifyKeyPermissionsAcceptsReadOnlyOwnerMode(t *testing.T) {
	path, info := unixKeyAt(t, 0o400)
	if err := verifyKeyPermissions(path, info); err != nil {
		t.Fatalf("owner-read-only key rejected: %v", err)
	}
}
