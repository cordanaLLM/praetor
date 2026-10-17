// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

//go:build windows

package util

import (
	"os"
	"path/filepath"
	"testing"
)

// sharedDirectory returns a fresh directory whose DACL lets Everyone read and write what is
// created in it, standing in for a shared volume or a widened profile folder. A user's own
// temporary directory is already private, so a file written there proves nothing.
func sharedDirectory(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	sid, err := currentUserSID()
	if err != nil {
		t.Fatal(err)
	}
	if err := setNamedDACL(dir, "D:P(A;OICI;FA;;;"+sid+")(A;OICI;FA;;;WD)"); err != nil {
		t.Fatal(err)
	}
	return dir
}

func requireOwnerOnly(t *testing.T, path string, want bool) {
	t.Helper()
	private, other, err := OwnerOnlyAccess(path)
	if err != nil {
		t.Fatal(err)
	}
	if private != want {
		t.Fatalf("%s: owner-only=%v (other trustee %q), want %v", filepath.Base(path), private, other, want)
	}
}

// Negative control: the check sees the inherited Everyone entry, so a pass below is the
// writers' doing, not a blind check.
func TestOwnerOnlyAccess_Negative_InheritedEveryoneIsSeen(t *testing.T) {
	dir := sharedDirectory(t)
	plain := filepath.Join(dir, "plain")
	if err := os.WriteFile(plain, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	requireOwnerOnly(t, plain, false)
}

// Positive (#101): every private writer leaves a file or directory only this account,
// SYSTEM and Administrators can open, even inside a directory that grants Everyone access.
func TestPrivateWriters_Positive_OwnerOnlyDACLOnWindows(t *testing.T) {
	dir := sharedDirectory(t)
	writers := map[string]func(string) error{
		"WriteFileSecure":    func(p string) error { return WriteFileSecure(p, []byte("x"), SecureFilePerm) },
		"WriteFileAtomic":    func(p string) error { return WriteFileAtomic(p, []byte("x"), SecureFilePerm) },
		"WriteFileExclusive": func(p string) error { return WriteFileExclusive(p, []byte("x"), SecureFilePerm) },
		"WriteFileNoFollow":  func(p string) error { return WriteFileNoFollow(p, []byte("x"), SecureFilePerm) },
		"MkdirSecure":        func(p string) error { return MkdirSecure(p, SecureDirPerm) },
		"MkdirConfined":      func(p string) error { return MkdirConfined(dir, filepath.Base(p), SecureDirPerm) },
	}
	for name, write := range writers {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(dir, name)
			if err := write(path); err != nil {
				t.Fatal(err)
			}
			requireOwnerOnly(t, path, true)
		})
	}
}

// Boundary: a tracked file (0644) keeps the DACL it inherits; only a perm without group or
// other access is made private.
func TestPrivateWriters_Boundary_TrackedPermKeepsInheritedDACL(t *testing.T) {
	dir := sharedDirectory(t)
	path := filepath.Join(dir, "tracked")
	if err := WriteFileAtomic(path, []byte("x"), TrackedFilePerm); err != nil {
		t.Fatal(err)
	}
	requireOwnerOnly(t, path, false)
}

func TestOwnerOnlyEntries_3D(t *testing.T) {
	const sid = "S-1-5-21-1-2-3-1001"
	for sddl, want := range map[string]bool{
		"D:P(A;;FA;;;" + sid + ")(A;;FA;;;SY)(A;;FA;;;BA)": true,
		"D:(A;ID;FA;;;" + sid + ")(D;;FA;;;WD)":            true,
		"D:(A;ID;FA;;;" + sid + ")(A;ID;0x1200a9;;;BU)":    false,
		"D:(A;;FR;;;WD)":      false,
		"D:NO_ACCESS_CONTROL": false,
	} {
		got, _, err := ownerOnlyEntries(sddl, sid)
		if err != nil || got != want {
			t.Errorf("%s: owner-only=%v err=%v, want %v", sddl, got, err, want)
		}
	}
	if _, _, err := ownerOnlyEntries("O:BA", sid); err == nil {
		t.Error("a descriptor without a DACL was judged")
	}
	// Boundary: the system writes the built-in Administrator (RID 500), the account a CI runner
	// may run as, under its alias LA; the entry is the user's only when that alias is passed in.
	const admin = "D:P(A;;FA;;;LA)(A;;FA;;;SY)(A;;FA;;;BA)"
	if got, other, err := ownerOnlyEntries(admin, "S-1-5-21-1-2-3-500"); err != nil || got || other != "LA" {
		t.Errorf("LA without its alias: owner-only=%v other=%q err=%v, want false and LA", got, other, err)
	}
	if got, _, err := ownerOnlyEntries(admin, "S-1-5-21-1-2-3-500", "LA"); err != nil || !got {
		t.Errorf("LA with its alias: owner-only=%v err=%v, want true", got, err)
	}
}

// userTrustee reads the name back the way the system writes it: SYSTEM is always SY, and the
// current account gets a name that ownerOnlyEntries accepts for a DACL naming only it.
func TestUserTrustee_Windows(t *testing.T) {
	if got, err := userTrustee("S-1-5-18"); err != nil || got != "SY" {
		t.Fatalf("userTrustee(SYSTEM) = %q, %v, want SY", got, err)
	}
	sid, err := currentUserSID()
	if err != nil {
		t.Fatal(err)
	}
	alias, err := userTrustee(sid)
	if err != nil || alias == "" {
		t.Fatalf("userTrustee(current) = %q, %v", alias, err)
	}
	if got, other, err := ownerOnlyEntries("D:P(A;;FA;;;"+alias+")", sid, alias); err != nil || !got {
		t.Errorf("the current account under %q: owner-only=%v other=%q err=%v", alias, got, other, err)
	}
}
