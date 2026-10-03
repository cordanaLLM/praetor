// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package testsupport

import (
	"context"
	"fmt"
	"os/exec"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/cordanaLLM/praetor/internal/util"
)

// GNUMakeCandidates are the names GNU Make is installed under, tried in order: make, gmake where
// make is another make (the BSDs) and mingw32-make on a MinGW host, such as the Windows runner
// image. TestGNUMakeCandidatesMatchHookToolchain holds the list equal to MAKE_CANDIDATES in
// .config/lefthook/scripts/toolchain.py, so the tests and the hooks resolve the same program.
var GNUMakeCandidates = []string{"make", "gmake", "mingw32-make"}

// gnuMakeProbeTimeout bounds one "--version" probe (HISS-02).
const gnuMakeProbeTimeout = 20 * time.Second

// GNUMake returns the first of GNUMakeCandidates on PATH whose --version reports GNU Make 4.x, or
// skips the test naming every candidate and why it was passed over (HISS-21). Replay rows are
// measured on GNU Make 4.4.1; macOS ships GNU Make 3.81, which has no "::=", ":::=" or "!=", so
// another version skips rather than replaying unmeasured answers.
func GNUMake(t testing.TB) string {
	t.Helper()
	tried := make([]string, 0, len(GNUMakeCandidates))
	for _, name := range GNUMakeCandidates {
		path, err := exec.LookPath(name)
		if err != nil {
			tried = append(tried, name+" (not on PATH)")
			continue
		}
		ctx, cancel := context.WithTimeout(context.Background(), gnuMakeProbeTimeout)
		version, err := util.RunCommand(ctx, "", path, "--version")
		cancel()
		if err == nil && strings.HasPrefix(version, "GNU Make 4.") {
			return path
		}
		first, _, _ := strings.Cut(version, "\n")
		tried = append(tried, fmt.Sprintf("%s (%q, %v)", path, first, err))
	}
	t.Skipf("GNU Make 4.x is required for the replay; tried %s", strings.Join(tried, "; "))
	return ""
}

// RequireGNUMakeShell skips the test on Windows unless GNU Make would run a $(shell ...) call or a
// "!=" assignment through sh with each of programs on PATH. GNU Make for Windows prefers an sh.exe
// on PATH and falls back to cmd.exe without one (README.W32, "GNU Make and sh.exe"), and cmd.exe
// keeps POSIX quotes and has no cat. Elsewhere Make runs /bin/sh, so nothing is skipped (HISS-21).
func RequireGNUMakeShell(t testing.TB, programs ...string) {
	t.Helper()
	if runtime.GOOS != "windows" {
		return
	}
	for _, program := range append([]string{"sh"}, programs...) {
		if _, err := exec.LookPath(program); err != nil {
			t.Skipf("GNU Make for Windows would run this row's shell text through cmd.exe or without %s: %v", program, err)
		}
	}
}
