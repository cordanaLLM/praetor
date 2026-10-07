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

// Positive: the pin reads as the action at its tag and the reuse major that tag runs.
func TestReuseActionPin(t *testing.T) {
	if got := ReuseActionRef(); got != "fsfe/reuse-action@"+ReuseActionVersion {
		t.Errorf("ReuseActionRef = %q", got)
	}
	if got := ReuseMajor(); got == "" || got == ReuseActionVersion || got[0] < '0' || got[0] > '9' {
		t.Errorf("ReuseMajor = %q, want the digits of %s", got, ReuseActionVersion)
	}
}

// ReuseDeclared reads the two REUSE markers at the root. Positive: REUSE.toml or LICENSES/ alone
// declares REUSE. Negative: a root with neither does not, nor a nil context. Boundary: a
// REUSE.toml directory or a LICENSES file is no marker, and neither is a symlink to one.
func TestReuseDeclared_3D(t *testing.T) {
	write := func(root, rel string) {
		t.Helper()
		path := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("x\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	tomlRoot, licensesRoot, plain, wrongKinds := t.TempDir(), t.TempDir(), t.TempDir(), t.TempDir()
	write(tomlRoot, ReuseFile)
	write(licensesRoot, LicensesDir+"/MIT.txt")
	write(plain, "README.md")
	write(wrongKinds, ReuseFile+"/inner")
	write(wrongKinds, LicensesDir)
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
