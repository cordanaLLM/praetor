package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/agenthook"
	"github.com/cordanaLLM/praetor/internal/util"
)

const hookProcessRun = "-test.run=^TestHookProcessHelper$"

// TestHookProcessHelper is the child of the process tests: it dispatches `hook` exactly
// as main does, with real stdin, streams and exit code.
func TestHookProcessHelper(t *testing.T) {
	arguments := os.Getenv("PRAETOR_HOOK_PROCESS_TEST")
	if arguments == "" {
		return
	}
	if err := dispatchCommand("hook", strings.Fields(arguments)); err != nil {
		os.Exit(commandExitCode(os.Stderr, err))
	}
	os.Exit(0)
}

// hookProcess runs `hook <arguments>` as a child process in dir and returns its streams
// and exit code.
func hookProcess(t *testing.T, dir, arguments string, stdin []byte, extraEnvironment ...string) (util.CommandBytes, int) {
	t.Helper()
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	environment := append([]string{
		"PATH=" + os.Getenv("PATH"), "SystemRoot=" + os.Getenv("SystemRoot"),
		"PRAETOR_HOOK_PROCESS_TEST=" + arguments, "GOCOVERDIR=" + t.TempDir(),
	}, extraEnvironment...)
	ctx, err := util.WithCommandEnvironment(context.Background(), environment)
	if err != nil {
		t.Fatal(err)
	}
	if ctx, err = util.WithCommandStdin(ctx, stdin); err != nil {
		t.Fatal(err)
	}
	result, err := util.RunCommandBytes(ctx, dir, binary, 1<<16, hookProcessRun)
	var exit *exec.ExitError
	switch {
	case err == nil:
		return result, 0
	case errors.As(err, &exit):
		return result, exit.ExitCode()
	}
	t.Fatalf("hook process did not run: %v", err)
	return result, -1
}

func hookRepository(t *testing.T, governed bool) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not on PATH; workspace resolution cannot run on this leg")
	}
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := util.RunGitProbe(context.Background(), dir, 4096, "init", "-q"); err != nil {
		t.Fatal(err)
	}
	if governed {
		if err := os.WriteFile(filepath.Join(dir, ".standards.yaml"), []byte("version: 1\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func TestHookProcessAllowsAndDeniesInTheClientDialect(t *testing.T) {
	root := hookRepository(t, true)
	nested := filepath.Join(root, "nested directory")
	if err := os.Mkdir(nested, 0o700); err != nil {
		t.Fatal(err)
	}
	allowed := []byte(`{"hook_event_name":"PreToolUse","tool_name":"Bash","tool_input":{"command":"git status"}}`)
	denied := []byte(`{"tool_input":{"command":"git commit --no-verify"}}`)
	for _, tc := range []struct {
		arguments string
		stdin     []byte
		exit      int
		stdout    string
	}{
		{"claude pre-tool", allowed, 0, ""}, {"claude pre-tool", denied, 2, ""},
		{"codex pre-tool", allowed, 0, ""}, {"codex pre-tool", denied, 2, ""},
		{"gemini pre-tool", denied, 2, ""},
		{"lefthook pre-tool", allowed, 0, "PRAETOR_COMMAND_POLICY_OK\n"}, {"lefthook pre-tool", denied, 1, ""},
		{"lefthook environment", nil, 0, ""},
	} {
		result, exit := hookProcess(t, nested, tc.arguments, tc.stdin)
		if exit != tc.exit || string(result.Stdout) != tc.stdout || (exit != 0) != bytes.Contains(result.Stderr, []byte("[BLOCKED BY HISS]")) {
			t.Errorf("%s: exit %d stdout %q stderr %q", tc.arguments, exit, result.Stdout, result.Stderr)
		}
		if bytes.Contains(result.Stderr, []byte("Error:")) {
			t.Errorf("%s: a verdict was reported as a command failure: %q", tc.arguments, result.Stderr)
		}
	}
}

func TestHookProcessFailsClosed(t *testing.T) {
	root := hookRepository(t, true)
	allowed := []byte(`{"tool_input":{"command":"git status"}}`)
	for _, tc := range []struct {
		name, arguments string
		stdin           []byte
		environment     string
		exit            int
	}{
		{"no arguments", " ", allowed, "", 2}, {"one argument", "claude", allowed, "", 2},
		{"three arguments", "claude pre-tool extra", allowed, "", 2}, {"unknown client", "cursor pre-tool", allowed, "", 2},
		{"client without this event's row", "codex pre-edit", allowed, "", 2}, {"empty stdin", "claude pre-tool", nil, "", 2},
		{"malformed stdin", "codex pre-tool", []byte("{"), "", 2},
		{"disabled lefthook in the environment", "lefthook environment", nil, "LEFTHOOK=0", 1},
		{"disabled lefthook reaches pre-tool", "claude pre-tool", allowed, "LEFTHOOK=0", 2},
	} {
		var extra []string
		if tc.environment != "" {
			extra = []string{tc.environment}
		}
		if result, exit := hookProcess(t, root, tc.arguments, tc.stdin, extra...); exit != tc.exit || len(result.Stdout) != 0 {
			t.Errorf("%s: exit %d stdout %q stderr %q", tc.name, exit, result.Stdout, result.Stderr)
		}
	}
}

func TestHookProcessSkipsAnUngovernedWorkspace(t *testing.T) {
	plain := hookRepository(t, false)
	governed := hookRepository(t, true)
	denied := []byte(`{"tool_input":{"command":"rm -rf .git/hooks"}}`)
	result, exit := hookProcess(t, plain, "claude pre-tool", denied)
	if exit != 0 || len(result.Stdout) != 0 || string(result.Stderr) != "praetor hook: workspace not governed, skipped\n" {
		t.Errorf("ungoverned: exit %d stdout %q stderr %q", exit, result.Stdout, result.Stderr)
	}
	// The payload names the governed workspace while the process runs in the plain one.
	payload := []byte(`{"cwd":` + quoteJSON(t, governed) + `,"tool_input":{"command":"rm -rf .git/hooks"}}`)
	if _, exit := hookProcess(t, plain, "claude pre-tool", payload); exit != 2 {
		t.Errorf("payload workspace ignored: exit %d", exit)
	}
}

func quoteJSON(t *testing.T, value string) string {
	t.Helper()
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(value) + `"`
}

func TestWriteHookResponse(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if err := writeHookResponse(&stdout, &stderr, agenthook.Response{Stdout: []byte("out"), Stderr: []byte("note")}); err != nil ||
		stdout.String() != "out" || stderr.String() != "note" {
		t.Fatalf("allow: %v %q %q", err, stdout.String(), stderr.String())
	}
	err := writeHookResponse(&stdout, &stderr, agenthook.Response{ExitCode: 2})
	var status exitStatusError
	if !errors.As(err, &status) || status.code != 2 || err.Error() != "exit status 2" {
		t.Fatalf("deny: %v", err)
	}
	if err := writeHookResponse(failingWriter{}, &stderr, agenthook.Response{Stdout: []byte("x")}); err == nil || errors.As(err, &status) {
		t.Fatalf("an undelivered response passed: %v", err)
	}
	if err := writeHookResponse(&stdout, failingWriter{}, agenthook.Response{}); err == nil {
		t.Fatal("an undelivered diagnostic passed")
	}
	err = writeHookResponse(failingWriter{}, failingWriter{}, agenthook.Response{Stderr: []byte("why"), ExitCode: 2})
	if !errors.As(err, &status) || status.code != 2 {
		t.Fatalf("a deny lost its exit code to a closed stream: %v", err)
	}
}

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) { return 0, errors.New("closed") }

func TestCommandExitCode(t *testing.T) {
	var stderr bytes.Buffer
	if code := commandExitCode(&stderr, exitStatusError{code: 2}); code != 2 || stderr.Len() != 0 {
		t.Errorf("exit status: %d %q", code, stderr.String())
	}
	if code := commandExitCode(&stderr, fmt.Errorf("wrapped: %w", exitStatusError{code: 3})); code != 3 || stderr.Len() != 0 {
		t.Errorf("wrapped status: %d %q", code, stderr.String())
	}
	if code := commandExitCode(&stderr, exitStatusError{code: 0}); code != 1 || stderr.String() != "Error: exit status 0\n" {
		t.Errorf("zero status inside an error: %d %q", code, stderr.String())
	}
	stderr.Reset()
	if code := commandExitCode(&stderr, errors.New("plain failure")); code != 1 || stderr.String() != "Error: plain failure\n" {
		t.Errorf("plain failure: %d %q", code, stderr.String())
	}
}
