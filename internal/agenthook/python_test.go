package agenthook

import (
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/testsupport"
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
