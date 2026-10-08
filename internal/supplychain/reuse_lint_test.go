// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package supplychain

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// Positive: the pin reads as the action at its tag and the reuse major that tag runs, and
// the digest-pinned reference combines the full commit SHA with the version comment.
func TestReuseActionPin(t *testing.T) {
	if got := ReuseActionRef(); got != "fsfe/reuse-action@"+ReuseActionVersion {
		t.Errorf("ReuseActionRef = %q", got)
	}
	if got := ReuseActionPinnedRef(); got != "fsfe/reuse-action@"+ReuseActionCommit+"  # "+ReuseActionVersion {
		t.Errorf("ReuseActionPinnedRef = %q", got)
	}
	checkHexSHA(t, ReuseActionCommit)
	if got := ReuseMajor(); got == "" || got == ReuseActionVersion || got[0] < '0' || got[0] > '9' {
		t.Errorf("ReuseMajor = %q, want the digits of %s", got, ReuseActionVersion)
	}
}

func checkHexSHA(t *testing.T, sha string) {
	t.Helper()
	if len(sha) != 40 {
		t.Errorf("commit SHA length = %d, want 40", len(sha))
		return
	}
	for _, c := range sha {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			t.Errorf("commit SHA %q contains non-hex character %q", sha, c)
			return
		}
	}
}

// ReuseDeclared reads the two REUSE markers at the root. Positive: REUSE.toml or LICENSES/ alone
// declares REUSE. Negative: a root with neither does not, nor a nil context. Boundary: a
// REUSE.toml directory or a LICENSES file is no marker, and neither is a symlink to one.
func TestReuseDeclared_3D(t *testing.T) {
	tomlRoot := licensedRoot(t, map[string]string{ReuseFile: "x\n"})
	licensesRoot := licensedRoot(t, map[string]string{LicensesDir + "/MIT.txt": "x\n"})
	plain := licensedRoot(t, map[string]string{"README.md": "x\n"})
	wrongKinds := licensedRoot(t, map[string]string{ReuseFile + "/inner": "x\n", LicensesDir: "x\n"})
	for name, tc := range map[string]struct {
		root string
		want bool
	}{
		"REUSE.toml": {tomlRoot, true}, "LICENSES/": {licensesRoot, true},
		"neither": {plain, false}, "wrong kinds": {wrongKinds, false},
	} {
		got, err := ReuseDeclared(context.Background(), tc.root)
		if err != nil || got != tc.want {
			t.Errorf("%s: ReuseDeclared = %v, %v; want %v", name, got, err, tc.want)
		}
	}
	//nolint:staticcheck // SA1012: the nil context is the case under test.
	if _, err := ReuseDeclared(nil, tomlRoot); err == nil {
		t.Error("a nil context was accepted")
	}
	if runtime.GOOS == "windows" {
		t.Log("the symlink case needs symlink privileges Windows test hosts do not grant by default")
		return
	}
	linked := t.TempDir()
	if err := os.Symlink(filepath.Join(licensesRoot, LicensesDir), filepath.Join(linked, LicensesDir)); err != nil {
		t.Fatal(err)
	}
	if got, err := ReuseDeclared(context.Background(), linked); err != nil || got {
		t.Errorf("a symlinked LICENSES/: ReuseDeclared = %v, %v; want false", got, err)
	}
}
