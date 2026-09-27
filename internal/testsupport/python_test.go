// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package testsupport

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/cordanaLLM/praetor/internal/util"
)

func TestPythonTextFoldsOneLineEndingStyle(t *testing.T) {
	for name, stream := range map[string]string{
		"LF":   "refused\nsecond\n",
		"CRLF": "refused\r\nsecond\r\n",
	} {
		got, err := PythonText([]byte(stream))
		if err != nil || string(got) != "refused\nsecond\n" {
			t.Errorf("%s: got %q, %v", name, got, err)
		}
	}
}

func TestPythonTextRejectsMixedEndingsAndLoneCarriageReturns(t *testing.T) {
	for name, stream := range map[string]string{
		"mixed":     "refused\r\nsecond\n",
		"lone CR":   "refused\rsecond\n",
		"CR at end": "refused\r",
	} {
		if got, err := PythonText([]byte(stream)); err == nil {
			t.Errorf("%s: %q passed as %q", name, stream, got)
		}
	}
}

func TestPythonTextBoundaryEmptyAndUnterminated(t *testing.T) {
	for _, stream := range [][]byte{nil, {}, []byte("no newline")} {
		got, err := PythonText(stream)
		if err != nil || !bytes.Equal(got, stream) {
			t.Errorf("%q: got %q, %v", stream, got, err)
		}
	}
}

// runPython runs script under the interpreter with argv and returns stdout and stderr.
func runPython(t *testing.T, interpreter, script string, argv ...string) (stdout, stderr []byte) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	result, err := util.RunCommandBytes(ctx, "", interpreter, 4096, append([]string{"-B", script}, argv...)...)
	if err != nil {
		t.Fatalf("run %s: %v (stderr %q)", script, err, result.Stderr)
	}
	return result.Stdout, result.Stderr
}

// TestWindowsNewlinePythonTranslatesTextStreamsOnly runs one script directly and through the
// launcher, from a directory whose name needs quoting in the launcher's source where the
// filesystem allows one: through the launcher the text streams end lines in CRLF on every
// host, the .buffer bytes and the arguments arrive unchanged, and __name__ is __main__. Read as
// text, the direct and the launched stderr are the same.
func TestWindowsNewlinePythonTranslatesTextStreamsOnly(t *testing.T) {
	interpreter := PythonInterpreter(t)
	dir := filepath.Join(t.TempDir(), `quote " and \ back`)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		dir = t.TempDir() // Windows refuses a quote in a name; its paths carry backslashes anyway.
	}
	script := filepath.Join(dir, "probe.py")
	source := "import sys\nif __name__ == \"__main__\":\n    sys.stderr.write(sys.argv[1] + \"\\n\")\n    sys.stdout.buffer.write(b\"marker\\n\")\n"
	if err := os.WriteFile(script, []byte(source), 0o600); err != nil {
		t.Fatal(err)
	}
	stdout, stderr := runPython(t, interpreter, WindowsNewlinePython(t, script), "refused")
	if string(stderr) != "refused\r\n" || string(stdout) != "marker\n" {
		t.Errorf("through the launcher: stderr %q, stdout %q", stderr, stdout)
	}
	if text, err := PythonText(stderr); err != nil || string(text) != "refused\n" {
		t.Errorf("launcher stderr read as %q, %v", text, err)
	}
	_, direct := runPython(t, interpreter, script, "refused")
	if text, err := PythonText(direct); err != nil || string(text) != "refused\n" {
		t.Errorf("direct stderr %q read as %q, %v", direct, text, err)
	}
}

func TestWindowsNewlinePythonFailsForAMissingScript(t *testing.T) {
	interpreter := PythonInterpreter(t)
	launcher := WindowsNewlinePython(t, filepath.Join(t.TempDir(), "absent.py"))
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if result, err := util.RunCommandBytes(ctx, "", interpreter, 4096, "-B", launcher); err == nil {
		t.Errorf("a missing script ran through the launcher: stdout %q", result.Stdout)
	}
}

func TestPythonInterpreterSkipsWithoutAnInterpreterOnPath(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	skipped := false
	t.Run("lookup", func(t *testing.T) {
		defer func() { skipped = t.Skipped() }()
		t.Errorf("found %q on an empty PATH", PythonInterpreter(t))
	})
	if !skipped {
		t.Error("an empty PATH did not skip the test")
	}
}
