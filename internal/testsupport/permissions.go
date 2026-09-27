// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package testsupport

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/cordanaLLM/praetor/internal/util"
)

// SkipIfFileModeUnenforced skips a case that provokes a permission failure by giving a file
// mode 0, on a host where that does not stop the file being read, and says why.
//
// It replaces checks on os.Geteuid() == 0. Those inferred the answer from one cause, root, and
// missed another: Geteuid returns -1 on Windows, where file protection is an ACL and a mode of 0
// only sets the read-only attribute. The cases built on them then reported a missing failure the
// platform could not produce. The answer is measured instead, which covers both causes.
func SkipIfFileModeUnenforced(t testing.TB) {
	t.Helper()
	probe := filepath.Join(t.TempDir(), "unreadable")
	if err := os.WriteFile(probe, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	restore := denyAll(t, probe, 0o600)
	defer restore()
	// #nosec G304 -- probe is a file this helper just created in its own temporary directory.
	if _, err := os.ReadFile(probe); err == nil {
		t.Skip("file modes are not enforced on this host (root, or a platform whose files are " +
			"protected by ACL), so a permission failure cannot be provoked here")
	}
}

// SkipIfDirectoryModeUnenforced is SkipIfFileModeUnenforced for a directory: it skips when a
// directory with mode 0 can still be listed.
func SkipIfDirectoryModeUnenforced(t testing.TB) {
	t.Helper()
	probe := filepath.Join(t.TempDir(), "unlistable")
	if err := os.Mkdir(probe, 0o700); err != nil {
		t.Fatal(err)
	}
	restore := denyAll(t, probe, 0o700)
	defer restore()
	if _, err := os.ReadDir(probe); err == nil {
		t.Skip("directory modes are not enforced on this host (root, or a platform whose " +
			"directories are protected by ACL), so an unreadable directory cannot be provoked here")
	}
}

// denyAll sets path to mode 0 and returns the function that restores mode, so the temporary
// directory can still be removed however the probe ends.
func denyAll(t testing.TB, path string, mode os.FileMode) func() {
	t.Helper()
	if err := os.Chmod(path, 0); err != nil {
		t.Fatal(err)
	}
	return func() {
		if err := os.Chmod(path, mode); err != nil {
			t.Errorf("restore probe mode on %s: %v", path, err)
		}
	}
}

// RequireCreatedMode fails t unless path carries the mode a writer that honours the process
// umask gives an entry it creates with perm: perm filtered by the umask. The umask is measured
// by creating a probe, never assumed, so a host with a restrictive umask asserts what it can
// actually produce. It skips where permission bits are not the file protection
// (util.ModeIsProtection), since no mode is asserted there.
func RequireCreatedMode(t testing.TB, path string, perm os.FileMode) {
	t.Helper()
	if !util.ModeIsProtection() {
		t.Skip("permission bits are not the file protection on this platform, so no mode is asserted")
	}
	want := perm & creationMask(t)
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat %s: %v", path, err)
	}
	if got := info.Mode().Perm(); got != want {
		t.Errorf("%s mode = %#o, want %#o (%#o under this host's umask)", path, got, want, perm)
	}
}

// creationMask is the set of permission bits the process umask lets a new entry keep, read off
// a probe created with every bit requested.
func creationMask(t testing.TB) os.FileMode {
	t.Helper()
	probe := filepath.Join(t.TempDir(), "umask-probe")
	// #nosec G302 G304 -- a probe in this test's own temporary directory; requesting every bit is what measures the umask.
	file, err := os.OpenFile(probe, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o777)
	if err != nil {
		t.Fatalf("create umask probe: %v", err)
	}
	info, err := file.Stat()
	if cerr := file.Close(); cerr != nil && err == nil {
		err = cerr
	}
	if err != nil {
		t.Fatalf("read umask probe: %v", err)
	}
	return info.Mode().Perm()
}
