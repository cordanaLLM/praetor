// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package testsupport

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/cordanaLLM/praetor/internal/util"
)

// PythonInterpreter returns a python3 or python on PATH that starts and imports json and re,
// or skips the test with the reason (HISS-21).
func PythonInterpreter(t testing.TB) string {
	t.Helper()
	for _, name := range []string{"python3", "python"} {
		path, err := exec.LookPath(name)
		if err != nil {
			continue
		}
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		_, err = util.RunCommandBytes(ctx, "", path, 4096, "-B", "-c", "import json, re")
		cancel()
		if err == nil {
			return path
		}
	}
	t.Skip("no working python3 or python on PATH; Python programs cannot run on this leg")
	return ""
}

// PythonText returns what a Python program wrote to sys.stdout or sys.stderr with LF line
// endings. CPython opens those text streams with newline=None on Windows
// (Python/pylifecycle.c, create_stdio), so each "\n" the program writes arrives as "\r\n"
// there and as "\n" everywhere else. A test comparing Python output with Go output byte for
// byte would compare the host's line ending, not the text. One consistent style is accepted;
// mixed endings or a lone carriage return are an error, never repaired.
func PythonText(stream []byte) ([]byte, error) {
	text, _, err := util.NormalizeLineEndingsStrict(string(stream))
	if err != nil {
		return nil, err
	}
	return []byte(text), nil
}

// windowsNewlineLauncher runs the script named by its first placeholder as __main__ with
// sys.stdout and sys.stderr translating "\n" to "\r\n", as CPython's standard streams do on
// Windows. Bytes written through the streams' .buffer stay untranslated, as they do there.
const windowsNewlineLauncher = `import runpy
import sys

script = %s
sys.stdout.reconfigure(newline="\r\n")
sys.stderr.reconfigure(newline="\r\n")
sys.argv = [script] + sys.argv[1:]
runpy.run_path(script, run_name="__main__")
`

// WindowsNewlinePython writes a launcher that runs script with the Windows newline
// translation of CPython's standard streams and returns the launcher's path. A test passes
// the launcher where it passed the script, with the same arguments, to prove on any host that
// its comparison of the script's output holds on a Windows leg.
func WindowsNewlinePython(t testing.TB, script string) string {
	t.Helper()
	absolute, err := filepath.Abs(script)
	if err != nil {
		t.Fatalf("resolve %s: %v", script, err)
	}
	literal, err := json.Marshal(absolute)
	if err != nil {
		t.Fatalf("quote %s: %v", absolute, err)
	}
	launcher := filepath.Join(t.TempDir(), "windows_newline_launcher.py")
	source := []byte(fmt.Sprintf(windowsNewlineLauncher, literal))
	if err := os.WriteFile(launcher, source, 0o600); err != nil {
		t.Fatalf("write launcher: %v", err)
	}
	return launcher
}
