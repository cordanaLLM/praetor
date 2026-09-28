// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package bump

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"testing"

	"github.com/cordanaLLM/praetor/internal/testsupport"
)

// resolvesTo is a lookPath that finds pnpm at path.
func resolvesTo(path string) func(string) (string, error) {
	return func(string) (string, error) { return path, nil }
}

// Under a batch-file pnpm on Windows the resolved shim is run and each caret is written four
// times, so the two cmd.exe parses between praetor and pnpm leave exactly one. The batch
// extension is matched in any case.
func TestPnpmUpdateCommand_Positive_BatchShimKeepsCaret(t *testing.T) {
	for _, shim := range []string{filepath.Join("shims", "pnpm.cmd"), filepath.Join("shims", "PNPM.BAT")} {
		name, args, err := pnpmUpdateCommand("windows", resolvesTo(shim), "lib@^5.7.3")
		if err != nil || name != shim || !slices.Equal(args, []string{"update", "lib@^^^^5.7.3"}) {
			t.Errorf("%s: pnpmUpdateCommand = %q %q, %v; want %q update lib@^^^^5.7.3", shim, name, args, err, shim)
		}
	}
}

// Outside Windows the argv is exactly the one pnpm always got, and pnpm is not even resolved.
func TestPnpmUpdateCommand_Positive_ArgvUnchangedOffWindows(t *testing.T) {
	unresolved := func(string) (string, error) {
		t.Fatal("pnpm was resolved outside Windows")
		return "", nil
	}
	for _, goos := range []string{"linux", "darwin", "freebsd"} {
		name, args, err := pnpmUpdateCommand(goos, unresolved, "lib@^5.7.3")
		if err != nil || name != "pnpm" || !slices.Equal(args, []string{"update", "lib@^5.7.3"}) {
			t.Errorf("%s: pnpmUpdateCommand = %q %q, %v; want pnpm update lib@^5.7.3", goos, name, args, err)
		}
	}
}

// A value cmd.exe would read as something other than itself is refused for a batch-file pnpm
// instead of being escaped: variable expansion, argument delimiters, quotes, redirection and
// grouping, a character outside ASCII, and an empty argument.
func TestPnpmUpdateCommand_Negative_BatchShimRefusesWhatCmdReinterprets(t *testing.T) {
	shim := filepath.Join("shims", "pnpm.cmd")
	for _, spec := range []string{"", "li%PATH%b@^1.0.0", "lib!x@^1.0.0", "lib@^1.0.0 x", "lib,x@1.0.0", "lib=x@1.0.0",
		`lib"@1.0.0`, "lib&x@1.0.0", "lib|x@1.0.0", "lib>x@1.0.0", "lib(x)@1.0.0", "lib\t@1.0.0", "libé@1.0.0"} {
		name, args, err := pnpmUpdateCommand("windows", resolvesTo(shim), spec)
		if !errors.Is(err, errBatchShimArgument) || name != "" || args != nil {
			t.Errorf("%q: pnpmUpdateCommand = %q %q, %v; want a refusal", spec, name, args, err)
		}
	}
}

// On Windows an executable pnpm, and a pnpm that does not resolve, get the plain argv; a spec
// without a caret passes a batch-file pnpm unchanged, and every character of a scoped name
// and a tilde prerelease range with build metadata is one cmd.exe leaves alone.
func TestPnpmUpdateCommand_Boundary_WhatReachesWindowsPnpm(t *testing.T) {
	missing := func(string) (string, error) { return "", os.ErrNotExist }
	cases := []struct {
		name     string
		lookPath func(string) (string, error)
		spec     string
		wantName string
		wantArg  string
	}{
		{"executable pnpm", resolvesTo(filepath.Join("bin", "pnpm.exe")), "lib@^5.7.3", "pnpm", "lib@^5.7.3"},
		{"unresolved pnpm", missing, "lib@^5.7.3", "pnpm", "lib@^5.7.3"},
		{"extensionless pnpm", resolvesTo(filepath.Join("bin", "pnpm")), "lib@^5.7.3", "pnpm", "lib@^5.7.3"},
		{"bare version", resolvesTo("pnpm.cmd"), "lib@5.7.3", "pnpm.cmd", "lib@5.7.3"},
		{"scoped tilde prerelease", resolvesTo("pnpm.cmd"), "@Scope/lib_x.y@~1.0.0-rc.1+build.5", "pnpm.cmd", "@Scope/lib_x.y@~1.0.0-rc.1+build.5"},
		{"caret at both ends", resolvesTo("pnpm.cmd"), "^lib^", "pnpm.cmd", "^^^^lib^^^^"},
	}
	for _, tc := range cases {
		name, args, err := pnpmUpdateCommand("windows", tc.lookPath, tc.spec)
		if err != nil || name != tc.wantName || !slices.Equal(args, []string{"update", tc.wantArg}) {
			t.Errorf("%s: pnpmUpdateCommand = %q %q, %v; want %q update %s", tc.name, name, args, err, tc.wantName, tc.wantArg)
		}
	}
}

// pnpmBatchShim writes pnpm.cmd into a new directory and returns that directory. Like the
// npm shim pnpm.cmd, whose last line hands %* to pnpm's entry point, it forwards its arguments
// with %* to the stand-in pnpm executable in bin, so cmd.exe parses them twice on the way.
func pnpmBatchShim(t *testing.T, bin string) string {
	t.Helper()
	shims := t.TempDir()
	target := filepath.Join(bin, testsupport.ExecutableName("pnpm"))
	if err := os.WriteFile(filepath.Join(shims, "pnpm.cmd"), []byte("@\""+target+"\" %*\r\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return shims
}

// A caret range reaches pnpm with its caret. On Windows pnpm is the batch file pnpm.cmd, run
// through cmd.exe, as the npm shim is; elsewhere it is the stand-in executable and the argv is
// the one pnpm always got. Without the escape, cmd.exe drops the caret and pnpm is asked for
// lib@5.7.3 (#508).
func TestApplyNodeUpdate_Positive_CaretReachesPnpmShim(t *testing.T) {
	const call = "pnpm update lib@^5.7.3"
	bin, log := standInToolchain(t, map[string]standInReply{call: {}}, "pnpm")
	if runtime.GOOS == "windows" {
		bin = pnpmBatchShim(t, bin)
	}
	t.Setenv("PATH", bin)
	dir := lockedPackageJSON(t, `{"dependencies":{"lib":"^5.0.0"}}`)
	if err := ApplyUpdate(testDeadline(t), dir, nodeCandidate("lib", "5.7.3")); err != nil {
		t.Fatal(err)
	}
	if got := callNames(standInCalls(t, log)); len(got) != 1 || got[0] != call {
		t.Fatalf("calls = %q, want only %q", got, call)
	}
}

// A package name cmd.exe would expand is refused before a batch-file pnpm runs, with the
// manifest unchanged; where pnpm is not a batch file the name reaches it as declared.
func TestApplyNodeUpdate_Negative_BatchShimNeverRunsForAReinterpretedName(t *testing.T) {
	const call = "pnpm update li%PATH%b@^2.0.0"
	bin, log := standInToolchain(t, map[string]standInReply{call: {}}, "pnpm")
	if runtime.GOOS == "windows" {
		bin = pnpmBatchShim(t, bin)
	}
	t.Setenv("PATH", bin)
	body := `{"dependencies":{"li%PATH%b":"^1.0.0"}}`
	dir := lockedPackageJSON(t, body)
	err := ApplyUpdate(testDeadline(t), dir, nodeCandidate("li%PATH%b", "2.0.0"))
	calls := callNames(standInCalls(t, log))
	if runtime.GOOS == "windows" {
		if !errors.Is(err, errBatchShimArgument) || len(calls) != 0 {
			t.Fatalf("batch-file pnpm: err = %v, calls = %q; want a refusal before pnpm runs", err, calls)
		}
	} else if err != nil || len(calls) != 1 || calls[0] != call {
		t.Fatalf("err = %v, calls = %q; want only %q", err, calls, call)
	}
	if got := readPackageJSON(t, dir); got != body {
		t.Fatalf("package.json = %s", got)
	}
}
