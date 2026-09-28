// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package bump

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/cordanaLLM/praetor/internal/testsupport"
)

// pnpmOnPath puts the stand-in pnpm in bin on PATH. On Windows pnpm is instead the batch file
// pnpm.cmd, like the npm shim, in a directory whose name holds cmd.exe metacharacters and no
// space (#538); it forwards its arguments with %* to the stand-in, as the npm shim hands %* to
// pnpm's entry point, so cmd.exe reads them twice on the way. Elsewhere the argv is the one
// pnpm always got.
func pnpmOnPath(t *testing.T, bin string) {
	t.Helper()
	if runtime.GOOS != "windows" {
		t.Setenv("PATH", bin)
		return
	}
	shims := filepath.Join(t.TempDir(), "Tom&Jerry(1)^!,=")
	if err := os.Mkdir(shims, 0o700); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(bin, testsupport.ExecutableName("pnpm"))
	if err := os.WriteFile(filepath.Join(shims, "pnpm.cmd"), []byte("@\""+target+"\" %*\r\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", shims)
}

// A caret range reaches pnpm with its caret. Without the cmd.exe quoting in the shared runner,
// cmd.exe dropped the caret and pnpm was asked for lib@5.7.3 (#508), and a shim directory
// holding '&' split the command before pnpm ran (#538).
func TestApplyNodeUpdate_Positive_CaretReachesPnpmShim(t *testing.T) {
	const call = "pnpm update lib@^5.7.3"
	bin, log := standInToolchain(t, map[string]standInReply{call: {}}, "pnpm")
	pnpmOnPath(t, bin)
	dir := lockedPackageJSON(t, `{"dependencies":{"lib":"^5.0.0"}}`)
	if err := ApplyUpdate(testDeadline(t), dir, nodeCandidate("lib", "5.7.3")); err != nil {
		t.Fatal(err)
	}
	if got := callNames(standInCalls(t, log)); len(got) != 1 || got[0] != call {
		t.Fatalf("calls = %q, want only %q", got, call)
	}
}

// A pnpm that fails behind the shim is reported, after it received the exact argv, and
// package.json is left as declared: the failure survives the cmd.exe and shim layers.
func TestApplyNodeUpdate_Negative_PnpmShimFailureIsReported(t *testing.T) {
	const call = "pnpm update lib@^5.7.3"
	bin, log := standInToolchain(t, map[string]standInReply{call: {Code: 1}}, "pnpm")
	pnpmOnPath(t, bin)
	body := `{"dependencies":{"lib":"^5.0.0"}}`
	dir := lockedPackageJSON(t, body)
	err := ApplyUpdate(testDeadline(t), dir, nodeCandidate("lib", "5.7.3"))
	if got := callNames(standInCalls(t, log)); err == nil || len(got) != 1 || got[0] != call {
		t.Fatalf("err = %v, calls = %q; want a reported failure after only %q", err, got, call)
	}
	if got := readPackageJSON(t, dir); got != body {
		t.Fatalf("package.json = %s", got)
	}
}

// A package name holding a percent reference, which #508 refused for a batch-file pnpm,
// reaches pnpm as declared on every platform: the runner guards the percent sign instead.
func TestApplyNodeUpdate_Boundary_PercentNameReachesPnpmShim(t *testing.T) {
	const call = "pnpm update li%PATH%b@^2.0.0"
	bin, log := standInToolchain(t, map[string]standInReply{call: {}}, "pnpm")
	pnpmOnPath(t, bin)
	dir := lockedPackageJSON(t, `{"dependencies":{"li%PATH%b":"^1.0.0"}}`)
	if err := ApplyUpdate(testDeadline(t), dir, nodeCandidate("li%PATH%b", "2.0.0")); err != nil {
		t.Fatal(err)
	}
	if got := callNames(standInCalls(t, log)); len(got) != 1 || got[0] != call {
		t.Fatalf("calls = %q, want only %q", got, call)
	}
}
