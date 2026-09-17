package agenthook

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/cordanaLLM/praetor/internal/util"
)

// pythonGuard is the implementation the Go policy replaces. The parity replay lives
// exactly as long as that file: the change that deletes the adapters deletes this test.
var pythonGuard = filepath.Join("..", "..", ".config", "agent", "hooks", "block_evasion.py")

// pythonInterpreter returns a working interpreter or skips with the reason (HISS-21).
func pythonInterpreter(t *testing.T) string {
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
	t.Skip("no working python3 or python on PATH; the Python guard cannot be replayed on this leg")
	return ""
}

// guardEnvironment is the child environment of the Python guard: enough to start an
// interpreter on every OS, and none of the Lefthook variables the guard itself judges.
func guardEnvironment(extra ...string) []string {
	environment := []string{"PATH=" + os.Getenv("PATH")}
	for _, key := range []string{"SystemRoot", "SYSTEMROOT", "TEMP", "TMP"} {
		if value := os.Getenv(key); value != "" {
			environment = append(environment, key+"="+value)
		}
	}
	return append(environment, extra...)
}

// runPythonGuard feeds one payload to the Python guard and reports its exit code and
// whether it printed the policy marker.
func runPythonGuard(t *testing.T, interpreter string, payload []byte, arguments []string, environment []string) (int, bool) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	ctx, err := util.WithCommandEnvironment(ctx, environment)
	if err != nil {
		t.Fatal(err)
	}
	if ctx, err = util.WithCommandStdin(ctx, payload); err != nil {
		t.Fatal(err)
	}
	argv := append([]string{"-B", pythonGuard}, arguments...)
	result, err := util.RunCommandBytes(ctx, "", interpreter, 1<<16, argv...)
	marker := bytes.Contains(result.Stdout, []byte(CommandPolicyMarker+"\n"))
	if err == nil {
		return 0, marker
	}
	var exit *exec.ExitError
	if !errors.As(err, &exit) {
		t.Fatalf("python guard did not run: %v", err)
	}
	return exit.ExitCode(), marker
}

func TestParityWithThePythonGuardOnTheSuitePayloads(t *testing.T) {
	interpreter := pythonInterpreter(t)
	root := repository(t, true)
	withOperator := policy(t, organisationContainerPattern)
	payloads := rawCases()
	for _, fixture := range loadCases(t) {
		payloads = append(payloads, rawCase{fixture.Name, fixture.Payload, fixture.Allow})
	}
	for _, tc := range payloads {
		pythonExit, pythonMarker := runPythonGuard(t, interpreter, tc.payload, nil, guardEnvironment())
		response := Run(context.Background(), Invocation{
			Client: "lefthook", Event: "pre-tool", Stdin: bytes.NewReader(tc.payload),
			Getenv: noEnvironment, WorkDir: root, Policy: withOperator,
		})
		goMarker := bytes.Equal(response.Stdout, []byte(CommandPolicyMarker+"\n"))
		if pythonExit != response.ExitCode || pythonMarker != goMarker || (pythonExit == 0) != tc.allow {
			t.Errorf("%s: python exit %d marker %v, go exit %d marker %v, fixture allow %v",
				tc.name, pythonExit, pythonMarker, response.ExitCode, goMarker, tc.allow)
		}
	}
}

func TestParityWithThePythonGuardOnTheEnvironment(t *testing.T) {
	interpreter := pythonInterpreter(t)
	root := repository(t, true)
	for _, variable := range []string{"", "LEFTHOOK=0", "LEFTHOOK=1", "LEFTHOOK_EXCLUDE=lint", "LEFTHOOK_SKIP=pre-push"} {
		var extra []string
		values := map[string]string{}
		if variable != "" {
			extra = []string{variable}
			key, value, _ := bytes.Cut([]byte(variable), []byte("="))
			values[string(key)] = string(value)
		}
		pythonExit, _ := runPythonGuard(t, interpreter, nil, []string{"--environment"}, guardEnvironment(extra...))
		response := Run(context.Background(), Invocation{
			Client: "lefthook", Event: "environment", Getenv: func(key string) string { return values[key] },
			WorkDir: root, Policy: policy(t),
		})
		if pythonExit != response.ExitCode {
			t.Errorf("%q: python exit %d, go exit %d (%s)", variable, pythonExit, response.ExitCode, response.Stderr)
		}
	}
}
