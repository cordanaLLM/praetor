package agenthook

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/testsupport"
	"github.com/cordanaLLM/praetor/internal/util"
)

// samePath compares two resolved executable paths, case-insensitively on Windows.
//
// windowsExecutable (python.go) finds a candidate by appending each PATHEXT entry and
// os.Stat-ing the result; a hit means the file exists, not that the returned string repeats
// the casing on disk. The default PATHEXT is ".COM;.EXE;.BAT;.CMD" (uppercase), so a
// candidate built as "python3.exe" (testsupport.ExecutableName's lowercase, matching real
// Python installers) resolves to "...python3.EXE" -- a different file only by a byte-exact
// string comparison, not on the NTFS volume that has to actually run it. This test file's own
// case-sensitive equality checks failed the first real Windows CI run for exactly that reason
// (#135), while the resolution itself worked: the process still launches, since Windows path
// lookup does not distinguish the two spellings.
func samePath(a, b string) bool {
	if runtime.GOOS == "windows" {
		return strings.EqualFold(a, b)
	}
	return a == b
}

// stubInterpreterSource is a Go stand-in for a Python interpreter (testsupport.BuildExecutable,
// used the same way as every other host-controlled test double in this repository): it ignores
// its own argv (runInterpreter always passes "-B <script> ..."; a fake interpreter has no
// script to run) and instead answers from three environment variables, so one binary serves
// every scenario a test needs.
const stubInterpreterSource = `package main

import (
	"fmt"
	"os"
	"strconv"
	"time"
)

func main() {
	if ready := os.Getenv("PRAETOR_STUB_READY_PATH"); ready != "" {
		if err := os.WriteFile(ready, nil, 0o600); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(2)
		}
	}
	if ms, err := strconv.Atoi(os.Getenv("PRAETOR_STUB_SLEEP_MS")); err == nil && ms > 0 {
		time.Sleep(time.Duration(ms) * time.Millisecond)
	}
	fmt.Print(os.Getenv("PRAETOR_STUB_STDOUT"))
	fmt.Fprint(os.Stderr, os.Getenv("PRAETOR_STUB_STDERR"))
	code, _ := strconv.Atoi(os.Getenv("PRAETOR_STUB_EXIT"))
	os.Exit(code)
}
`

// buildStub compiles the stub interpreter into dir under name (testsupport.ExecutableName
// applies the platform's own extension) and returns a Getenv that resolves PATH to dir alone,
// so ResolveInterpreter finds nothing else on the host.
func buildStub(t *testing.T, name string) (string, func(string) string) {
	t.Helper()
	dir := t.TempDir()
	path := testsupport.BuildExecutable(t, dir, name, stubInterpreterSource)
	return path, pathOnly(dir)
}

func pathOnly(dir string) func(string) string {
	return func(key string) string {
		if key == "PATH" {
			return dir
		}
		return ""
	}
}

func TestResolveInterpreterFindsTheFirstCandidateOnPath(t *testing.T) {
	path, getenv := buildStub(t, "python3")
	argv, err := ResolveInterpreter(getenv, [][]string{{"python3"}, {"python"}})
	if err != nil || len(argv) != 1 || !samePath(argv[0], path) {
		t.Fatalf("argv=%v err=%v want=%s", argv, err, path)
	}
}

func TestResolveInterpreterNoCandidateResolves(t *testing.T) {
	_, err := ResolveInterpreter(pathOnly(t.TempDir()), [][]string{{"python3"}, {"python"}})
	if err == nil {
		t.Fatal("an empty PATH resolved an interpreter")
	}
	if _, err := ResolveInterpreter(nil, [][]string{{"python3"}}); err == nil {
		t.Error("a nil environment reader resolved an interpreter")
	}
}

// TestResolveInterpreterTwoTokenCandidate is the "py -3" boundary the rollout spec names
// (3.3, H2 row of section 9.3): a two-token candidate resolves its first token and keeps the
// rest verbatim as leading arguments.
func TestResolveInterpreterTwoTokenCandidate(t *testing.T) {
	path, getenv := buildStub(t, "py")
	argv, err := ResolveInterpreter(getenv, [][]string{{"missing-interpreter"}, {"py", "-3"}})
	if err != nil || len(argv) != 2 || !samePath(argv[0], path) || argv[1] != "-3" {
		t.Fatalf("argv=%v err=%v want=[%s -3]", argv, err, path)
	}
}

func TestResolveInterpreterSkipsAnEmptyCandidate(t *testing.T) {
	path, getenv := buildStub(t, "python3")
	argv, err := ResolveInterpreter(getenv, [][]string{{}, {"python3"}})
	if err != nil || len(argv) != 1 || !samePath(argv[0], path) {
		t.Fatalf("argv=%v err=%v", argv, err)
	}
}

func TestLookPathUsesAnAbsoluteNameAsIs(t *testing.T) {
	path, _ := buildStub(t, "python3")
	argv, err := ResolveInterpreter(pathOnly(t.TempDir()), [][]string{{path}})
	if err != nil || len(argv) != 1 || !samePath(argv[0], path) {
		t.Fatalf("argv=%v err=%v want=%s", argv, err, path)
	}
}

func TestExtractMarker(t *testing.T) {
	if got, err := extractMarker([]byte("noise\nPRAETOR_CHECKPOINT_RESULT={\"a\":1}\nmore\n"), checkpointResultMarker); err != nil || got != `{"a":1}` {
		t.Fatalf("one match: %q %v", got, err)
	}
	if got, err := extractMarker([]byte("noise\r\nPRAETOR_CHECKPOINT_SESSION=0123456789abcdef\r\n"), "PRAETOR_CHECKPOINT_SESSION="); err != nil || got != "0123456789abcdef" {
		t.Fatalf("a CRLF marker line (Windows text-mode stdout) must give its value without the CR, got %q, %v", got, err)
	}
	if _, err := extractMarker([]byte("noise\n"), checkpointResultMarker); err == nil {
		t.Error("a missing marker was accepted")
	}
	duplicate := checkpointResultMarker + "{}\n" + checkpointResultMarker + "{}\n"
	if _, err := extractMarker([]byte(duplicate), checkpointResultMarker); err == nil {
		t.Error("a duplicate marker was accepted")
	}
	if got, err := extractMarker([]byte(checkpointScopeMarker+"\n"), checkpointScopeMarker); err != nil || got != "" {
		t.Fatalf("bare marker: %q %v", got, err)
	}
}

func TestDecodeCheckpointReport(t *testing.T) {
	valid := `{"schema_version":1,"enabled":true,"due":true,"actions":["a"]}`
	report, err := decodeCheckpointReport(valid)
	if err != nil || !report.Due || len(report.Actions) != 1 {
		t.Fatalf("valid due report: %+v %v", report, err)
	}
	for name, bad := range map[string]string{
		"wrong schema":        `{"schema_version":2,"enabled":true,"due":false,"actions":[]}`,
		"due without actions": `{"schema_version":1,"enabled":true,"due":true,"actions":[]}`,
		"carries an error":    `{"schema_version":1,"enabled":true,"due":false,"actions":[],"error":"boom"}`,
		"not json":            `not json`,
	} {
		if _, err := decodeCheckpointReport(bad); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	notDue, err := decodeCheckpointReport(`{"schema_version":1,"enabled":false,"due":false,"actions":[]}`)
	if err != nil || notDue.Due {
		t.Fatalf("boundary not-due report: %+v %v", notDue, err)
	}
}

func TestCheckpointScriptPath(t *testing.T) {
	if got, want := checkpointScript("/repo", "checkpoint.py"), filepath.Join("/repo", ".config", "lefthook", "scripts", "checkpoint.py"); got != want {
		t.Errorf("checkpointScript: %q want %q", got, want)
	}
}

// TestCheckpointFailureReason pins the reason text for a failed run's stderr shapes, including the
// named empty parts when neither stderr nor a marker carries text.
func TestCheckpointFailureReason(t *testing.T) {
	runErr := errors.New("exit status 1")
	markerErr := errors.New("missing checkpoint marker")
	exact := strings.Repeat("e", util.MaxErrorBodyBytes)
	for _, tc := range []struct {
		name   string
		runErr error
		stderr string
		want   string
	}{
		{"failed with stderr", runErr, " \nscript failed\n\t", "exit status 1: script failed; no checkpoint result marker"},
		// engine-checkpoint-reason-1983-20261116: name both empty diagnostic channels.
		{"failed without stderr", runErr, "", "exit status 1; no checkpoint result marker; stderr empty"},
		{"failed with blank stderr", runErr, " \n\t", "exit status 1; no checkpoint result marker; stderr empty"},
		{"failed at cap", runErr, exact, "exit status 1: " + exact + "; no checkpoint result marker"},
		{"failed above cap", runErr, " \n" + exact + "x\n", "exit status 1: " + exact + "... [truncated]; no checkpoint result marker"},
		{"success with stderr", nil, " \nscript warning\n\t", "script warning"},
		{"success above cap unchanged", nil, exact + "x", exact + "x"},
		{"success without stderr", nil, "", markerErr.Error()},
		{"success with blank stderr", nil, " \n\t", markerErr.Error()},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := checkpointFailureReason(tc.runErr, markerErr, util.CommandBytes{Stderr: []byte(tc.stderr)})
			if got != tc.want {
				t.Errorf("reason length=%d want length=%d; prefix=%q", len(got), len(tc.want), got[:min(len(got), 100)])
			}
		})
	}
}

// TestCheckpointFailureReasonMarker checks that the marker JSON error of a failed run is added to the
// reason, bounded, and that empty, invalid or duplicate markers are named (#1983).
func TestCheckpointFailureReasonMarker(t *testing.T) {
	const reported = `{"error":"state lock busy"}`
	exact := strings.Repeat("e", util.MaxErrorBodyBytes)
	for _, tc := range []struct{ name, stdout, stderr, want string }{
		{"error", checkpointResultMarker + reported, "", "checkpoint evaluator: state lock busy; stderr empty"},
		{"both", checkpointResultMarker + reported, "warn", "exit status 1: warn; checkpoint evaluator: state lock busy"},
		{"empty marker", checkpointResultMarker, "", "checkpoint result marker empty; stderr empty"},
		{"empty error", checkpointResultMarker + `{"error":""}`, "", "checkpoint evaluator error empty; stderr empty"},
		{"invalid", checkpointResultMarker + `{`, "", "checkpoint result JSON invalid; stderr empty"},
		{"duplicate", strings.Repeat(checkpointResultMarker+reported+"\n", 2), "", "no unique checkpoint result marker; stderr empty"},
		{"at cap", checkpointResultMarker + `{"error":"` + exact + `"}`, "", "checkpoint evaluator: " + exact + "; stderr empty"},
		{"above cap", checkpointResultMarker + `{"error":"` + exact + `x"}`, "warn", "checkpoint evaluator: " + exact + "... [truncated]"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := checkpointFailureReason(errors.New("exit status 1"), nil,
				util.CommandBytes{Stdout: []byte(tc.stdout), Stderr: []byte(tc.stderr)})
			if !strings.Contains(got, tc.want) || !strings.HasPrefix(got, "exit status 1") {
				t.Errorf("diagnostic length=%d want length=%d; prefix=%q", len(got), len(tc.want), got[:min(len(got), 100)])
			}
			if tc.stderr != "" && !strings.Contains(got, tc.stderr) {
				t.Error("stderr lost alongside evaluator error")
			}
			if tc.name == "above cap" && strings.Contains(got, exact+"x") {
				t.Error("evaluator error exceeds util.MaxErrorBodyBytes")
			}
		})
	}
}

// TestCheckpointCommandTimeout checks that common.py's run_bounded names the command and the limit
// when a command outlives its timeout.
func TestCheckpointCommandTimeout(t *testing.T) {
	argv, err := ResolveInterpreter(os.Getenv, [][]string{{"python3"}, {"python"}})
	if err != nil {
		t.Fatal(err)
	}
	// Plain attribute swaps, no unittest: the gate's toolchain container ships a Python without it.
	const script = `import sys
sys.path.insert(0, sys.argv[1])
import common
args = [sys.executable, "-c", "import time; time.sleep(10)", "x" * 1000]
def wait_expired(*_args, **_kwargs):
    raise common.subprocess.TimeoutExpired(args, 0.001)
original = common._bounded_output
for mode in ("native", "threaded", "wait"):
    target = original
    if mode == "threaded":
        target = common._bounded_output_threaded
    if mode == "wait":
        target = wait_expired
    common._bounded_output = target
    try:
        common.run_bounded(args, timeout=0.1)
    except common.HookError as error:
        print(mode + ": " + str(error))
    else:
        raise SystemExit("sleeping command unexpectedly succeeded")
    finally:
        common._bounded_output = original
# Binary write: print() in text mode ends lines with CRLF on Windows, and run_bounded returns raw bytes.
if common.run_bounded([sys.executable, "-c", "import sys; sys.stdout.buffer.write(b'ok\\n')"], timeout=10) != b"ok\n":
    raise SystemExit("successful command changed")
`
	output, err := util.RunCommand(t.Context(), "", argv[0], "-B", "-c", script,
		filepath.Join("..", "..", checkpointScriptDir))
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(output, "\n") {
		if !strings.Contains(line, "checkpoint command timed out after 0.1 s: "+filepath.Base(argv[0])+" -c") ||
			len(line) > 310 || strings.Contains(line, filepath.Dir(argv[0])) {
			t.Errorf("timeout must name bounded command and original limit without full path: %q", line)
		}
	}
}
