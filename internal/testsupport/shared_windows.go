// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

//go:build windows

package testsupport

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/cordanaLLM/praetor/internal/util"
)

// SharedDirectory returns a fresh directory whose DACL also lets Everyone read and write
// what is created in it, as a shared volume or a widened profile folder does. A file a
// private writer creates there must still come out owner-only (#101).
func SharedDirectory(t testing.TB) string {
	t.Helper()
	dir := t.TempDir()
	if out, err := exec.CommandContext(t.Context(), "icacls", dir, "/grant", "*S-1-1-0:(OI)(CI)F").CombinedOutput(); err != nil {
		t.Fatalf("grant Everyone on %s: %v: %s", dir, err, out)
	}
	probe := filepath.Join(dir, "probe")
	// A plain create inherits the directory's DACL, so the probe shows what others may open.
	if err := os.WriteFile(probe, nil, 0o644); err != nil {
		t.Fatalf("create probe in %s: %v", dir, err)
	}
	if private, _, err := util.OwnerOnlyAccess(probe); err != nil || private {
		t.Fatalf("shared directory %s does not grant others: private=%v err=%v", dir, private, err)
	}
	return dir
}

// RequireOwnerOnly fails t unless the DACL of path grants access to no one but this account,
// SYSTEM and Administrators.
func RequireOwnerOnly(t testing.TB, path string) {
	t.Helper()
	private, other, err := util.OwnerOnlyAccess(path)
	if err != nil {
		t.Fatalf("read the DACL of %s: %v", path, err)
	}
	if !private {
		t.Fatalf("%s is open to %s, want owner only", path, other)
	}
}
