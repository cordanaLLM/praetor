// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

//go:build windows

package lockdown

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// keyAt writes a stand-in key file and returns its path and stat.
func keyAt(t *testing.T, dir, name string) (string, os.FileInfo) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte("00"), 0o600); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	return path, info
}

// Positive: a key in the per-user configuration tree is accepted. On the previous
// implementation this was impossible -- Go reports 0666 for any writable file on
// Windows, so `Perm()&0o077 == 0` could never hold and the gate could never sign.
func TestVerifyKeyPermissionsAcceptsKeyInsideUserConfigDir(t *testing.T) {
	configDir, err := os.UserConfigDir()
	if err != nil {
		t.Skipf("per-user configuration directory unavailable: %v", err)
	}
	dir := filepath.Join(configDir, "praetor-keyperm-test")
	t.Cleanup(func() {
		if err := os.RemoveAll(dir); err != nil {
			t.Errorf("stand-in key directory not removed: %v", err)
		}
	})
	path, info := keyAt(t, dir, "receipt.key")
	if err := verifyKeyPermissions(path, info); err != nil {
		t.Fatalf("key inside the per-user configuration directory rejected: %v", err)
	}
}

// Negative: containment is a real refusal, not a rubber stamp. A key on a shared
// location outside this user's configuration tree is rejected.
func TestVerifyKeyPermissionsRefusesKeyOutsideUserConfigDir(t *testing.T) {
	path, info := keyAt(t, t.TempDir(), "receipt.key")
	err := verifyKeyPermissions(path, info)
	if err == nil {
		t.Fatal("key outside the per-user configuration directory accepted")
	}
	if !errors.Is(err, ErrInsecureKeyPerm) {
		t.Fatalf("refusal does not carry ErrInsecureKeyPerm: %v", err)
	}
}

// Boundary: a directory is never a key, whatever its containment.
func TestVerifyKeyPermissionsRefusesADirectory(t *testing.T) {
	configDir, err := os.UserConfigDir()
	if err != nil {
		t.Skipf("per-user configuration directory unavailable: %v", err)
	}
	dir := filepath.Join(configDir, "praetor-keyperm-dir-test")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.RemoveAll(dir); err != nil {
			t.Errorf("stand-in key directory not removed: %v", err)
		}
	})
	info, err := os.Stat(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := verifyKeyPermissions(dir, info); !errors.Is(err, ErrInsecureKeyPerm) {
		t.Fatalf("directory accepted as a signing key: %v", err)
	}
}
