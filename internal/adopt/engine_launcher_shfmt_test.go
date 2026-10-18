// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package adopt

import (
	"context"
	"errors"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/cordanaLLM/praetor/internal/util"
)

// shfmtLauncherArgs are the settings adopters format shell with (#1282): two-space indent and
// indented case branches.
var shfmtLauncherArgs = []string{"-d", "-i", "2", "-ci"}

// shfmtExit runs shfmt -d -i 2 -ci over text and returns its exit status and output, or skips
// where shfmt is not installed.
func shfmtExit(t *testing.T, text string) (int, string) {
	t.Helper()
	shfmt, err := exec.LookPath("shfmt")
	if err != nil {
		t.Skip("shfmt is not on PATH; the formatter stability of the launcher is not checked on this leg")
	}
	path := filepath.Join(t.TempDir(), "engine.sh")
	mustWrite(t, path, text)
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	args := append(append([]string{}, shfmtLauncherArgs...), path)
	res, err := util.RunCommandBytes(ctx, "", shfmt, util.MaxCommandOutputBytes, args...)
	out := append(res.Stdout, res.Stderr...)
	var exit *exec.ExitError
	switch {
	case err == nil:
		return 0, string(out)
	case errors.As(err, &exit):
		return exit.ExitCode(), string(out)
	default:
		t.Fatalf("run shfmt: %v", err)
		return 0, ""
	}
}

// Positive: shfmt -i 2 -ci leaves the launcher as it is, so an adopter's formatter hook does not
// rewrite a file adoption keeps following the engine pin (#1282).
func TestEngineLauncher_Positive_StableUnderShfmt(t *testing.T) {
	if code, out := shfmtExit(t, engineLauncherScript); code != 0 {
		t.Fatalf("shfmt %s rewrites the launcher (exit %d):\n%s", strings.Join(shfmtLauncherArgs, " "), code, out)
	}
}

// Negative: a trailing guard out of its column, and a joined one-line form shfmt splits into
// lines without the guard, both fail.
func TestEngineLauncher_Negative_ShfmtRefusesMisformattedLauncher(t *testing.T) {
	padded := regexp.MustCompile(`(?m)^(\S.*\S) {2,}#$`)
	if !padded.MatchString(engineLauncherScript) {
		t.Fatal("no aligned trailing guard to misalign")
	}
	split := regexp.MustCompile(`    if \[ -x "\$directory/\$binary" \]; then +#\n      printf .* +#\n      exit 0 +#\n    fi +#\n`)
	joined := "    if [ -x \"$directory/$binary\" ]; then printf '%s\\n' \"$directory/$binary\"; exit 0; fi #\n"
	for name, text := range map[string]string{
		"misaligned guard": padded.ReplaceAllString(engineLauncherScript, "$1 #"),
		"joined one-liner": split.ReplaceAllLiteralString(engineLauncherScript, joined),
	} {
		if text == engineLauncherScript {
			t.Fatalf("%s: the plant changed nothing", name)
		}
		if code, _ := shfmtExit(t, text); code != 1 {
			t.Errorf("%s: shfmt exit %d, want 1", name, code)
		}
	}
}

// Boundary: a CRLF checkout runs the lines the shfmt layout split, --print-path on a cached pin
// and the install watchdog, as the LF launcher does.
func TestEngineLauncher_Boundary_CRLFCheckoutRunsTheSplitLines(t *testing.T) {
	f := newEngineFixture(t)
	f.cached(enginePin)
	f.declare("gate.yml", "env:\n  PRAETOR_REF: "+enginePin+"\n")
	mustWrite(t, filepath.Join(f.work, filepath.FromSlash(engineLauncherFile)), strings.ReplaceAll(engineLauncherScript, "\n", "\r\n"))
	want := filepath.Join(f.cache, "praetor", "engine", enginePin, "standardsctl")
	if out, code := f.runLine("sh " + engineLauncherFile + " --print-path"); code != 0 || strings.TrimSpace(out) != want {
		t.Fatalf("CRLF --print-path: exit %d, output %q, want %q", code, out, want)
	}

	slow := newEngineFixture(t)
	slow.declare("gate.yml", "env:\n  PRAETOR_REF: "+enginePin+"\n")
	slow.goStub("sleep 30\n")
	mustWrite(t, filepath.Join(slow.work, filepath.FromSlash(engineLauncherFile)), strings.ReplaceAll(engineLauncherScript, "\n", "\r\n"))
	if out, code := slow.run("PRAETOR_ENGINE_INSTALL_TIMEOUT=1"); code != 1 || !strings.Contains(out, "timeout 1 seconds") {
		t.Fatalf("CRLF install watchdog: exit %d, output %q", code, out)
	}
}
