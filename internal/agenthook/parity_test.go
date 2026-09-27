package agenthook

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
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

// guardResult is what one Python guard run printed and returned.
type guardResult struct {
	exit   int
	marker bool
	stderr []byte
}

// runGuardScript feeds one payload to a guard script and reports its exit code, whether it
// printed the policy marker, and its stderr.
func runGuardScript(t *testing.T, interpreter, script string, payload []byte, arguments []string, environment []string) guardResult {
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
	argv := append([]string{"-B", script}, arguments...)
	result, err := util.RunCommandBytes(ctx, "", interpreter, 1<<16, argv...)
	guard := guardResult{marker: bytes.Contains(result.Stdout, []byte(CommandPolicyMarker+"\n")), stderr: result.Stderr}
	if err == nil {
		return guard
	}
	var exit *exec.ExitError
	if !errors.As(err, &exit) {
		t.Fatalf("python guard did not run: %v", err)
	}
	guard.exit = exit.ExitCode()
	return guard
}

// refusalDrift compares a Python guard's stderr with the engine's for one case and returns
// "" when they agree. Refusals of unreadable input agree on the InvalidInputRefusal prefix
// only: the reason after it is each parser's own error text. Every other refusal, and the
// empty stderr of an allowed command, must match character for character (BUG-1014).
func refusalDrift(name string, python, engine []byte) string {
	if bytes.HasPrefix(engine, []byte(InvalidInputRefusal)) {
		if bytes.HasPrefix(python, []byte(InvalidInputRefusal)) {
			return ""
		}
	} else if bytes.Equal(python, engine) {
		return ""
	}
	return name + ": refusal text drifted\npython " + strconv.Quote(string(python)) + "\nengine " + strconv.Quote(string(engine))
}

// TestParityWithThePythonGuardOnTheSuitePayloads replays the corpus against the Python guard
// and the built-in Go policy. Neither carries operator rules, so a fixture that only an
// operator deny pattern refuses (operator: true) is allowed by both.
func TestParityWithThePythonGuardOnTheSuitePayloads(t *testing.T) {
	interpreter := pythonInterpreter(t)
	root := repository(t, true)
	builtin := policy(t)
	payloads := rawCases()
	for _, fixture := range loadCases(t) {
		payloads = append(payloads, rawCase{fixture.Name, fixture.Payload, fixture.Allow || fixture.Operator})
	}
	for _, tc := range payloads {
		start := time.Now()
		python := runGuardScript(t, interpreter, pythonGuard, tc.payload, nil, guardEnvironment())
		if elapsed := time.Since(start); elapsed > 5*time.Second {
			t.Errorf("%s: python guard took %v, exceeding the 5s timing bound (pathological backtracking check)", tc.name, elapsed)
		}
		response := Run(context.Background(), Invocation{
			Client: "lefthook", Event: "pre-tool", Stdin: bytes.NewReader(tc.payload),
			Getenv: noEnvironment, WorkDir: root, Policy: builtin,
		})
		goMarker := bytes.Equal(response.Stdout, []byte(CommandPolicyMarker+"\n"))
		if python.exit != response.ExitCode || python.marker != goMarker || (python.exit == 0) != tc.allow {
			t.Errorf("%s: python exit %d marker %v, go exit %d marker %v, fixture allow %v",
				tc.name, python.exit, python.marker, response.ExitCode, goMarker, tc.allow)
		}
		if drift := refusalDrift(tc.name, python.stderr, response.Stderr); drift != "" {
			t.Error(drift)
		}
	}
}

// refusalCases are the cases whose full refusal text both engines must share: an evasion
// flag, the dev-root rule, a command over the scan bound, one exactly at it, and a benign
// command. A scan-bound refusal is the Python adapters' alone (RE2 needs no bound), so its
// engine text is ScanBoundRefusal and the Go policy's own verdict on it is an allow.
func refusalCases() []struct {
	name, command string
	want          []byte
} {
	return []struct {
		name, command string
		want          []byte
	}{
		{"evasion flag", "git push --no-verify origin main", []byte(BuiltinRules()[0].RefusalPrefix() + builtinEvasion[0] + "\n")},
		{"dev root", "praetorctl adopt /home/user/dev", []byte(BuiltinRules()[len(builtinEvasion)].RefusalPrefix() + builtinDevRoot + "\n")},
		{"scan bound exceeded", strings.Repeat("x", MaxScanLineChars+1), []byte(ScanBoundRefusal() + "\n")},
		{"scan bound exactly", strings.Repeat("x", MaxScanLineChars), nil},
		{"benign", "git status", nil},
	}
}

// TestParityRefusalTextWithThePythonGuard holds praetor's own guard to the engine's wording:
// for each refusal case its stderr is the engine's text character for character, and an
// allowed command prints nothing. The engine's text comes from the Go policy itself where the
// policy judges the command.
func TestParityRefusalTextWithThePythonGuard(t *testing.T) {
	interpreter := pythonInterpreter(t)
	builtin := policy(t)
	for _, tc := range refusalCases() {
		python := runGuardScript(t, interpreter, pythonGuard, commandPayload(t, map[string]any{"tool_input": map[string]string{"command": tc.command}}), nil, guardEnvironment())
		if drift := refusalDrift(tc.name, python.stderr, tc.want); drift != "" {
			t.Error(drift)
		}
		if tc.name == "scan bound exceeded" {
			continue
		}
		engine := []byte(nil)
		if verdict := builtin.Command(tc.command); verdict.Outcome == Deny {
			engine = []byte(verdict.Reason + "\n")
		}
		if drift := refusalDrift(tc.name+" (Go policy)", engine, tc.want); drift != "" {
			t.Error(drift)
		}
	}
}

// TestRefusalDriftFailsOnAChangedWord proves the comparison above can fail: a copy of the
// guard with one word of its evasion refusal changed is reported, as is a guard that stays
// silent where the engine refuses, while unreadable input only has to share the prefix.
func TestRefusalDriftFailsOnAChangedWord(t *testing.T) {
	interpreter := pythonInterpreter(t)
	source, err := os.ReadFile(pythonGuard)
	if err != nil {
		t.Fatal(err)
	}
	drifted := bytes.Replace(source, []byte("verification evasion prohibited"), []byte("verification evasion refused"), 1)
	if bytes.Equal(drifted, source) {
		t.Fatal("the guard no longer carries the evasion refusal this test changes")
	}
	script := filepath.Join(t.TempDir(), "block_evasion.py")
	if err := os.WriteFile(script, drifted, 0o600); err != nil {
		t.Fatal(err)
	}
	evasion := refusalCases()[0]
	python := runGuardScript(t, interpreter, script, commandPayload(t, map[string]any{"tool_input": map[string]string{"command": evasion.command}}), nil, guardEnvironment())
	if python.exit == 0 || refusalDrift(evasion.name, python.stderr, evasion.want) == "" {
		t.Errorf("a changed refusal word passed the comparison: exit %d, stderr %q", python.exit, python.stderr)
	}
	if refusalDrift("silent", nil, evasion.want) == "" {
		t.Error("an empty stderr passed for a refused command")
	}
	parsed := []byte(InvalidInputRefusal + "Expecting value: line 1 column 1 (char 0)\n")
	if refusalDrift("invalid input", parsed, []byte(InvalidInputRefusal+"hook input must be one JSON object\n")) != "" {
		t.Error("unreadable input must agree on the prefix only")
	}
}

// pythonRawString matches one r"..." entry of a Python list literal.
var pythonRawString = regexp.MustCompile(`(?m)^\s*r"([^"\n]*)",\s*$`)

// pythonPatternList returns the r"..." entries of the named list in the Python guard.
func pythonPatternList(t *testing.T, name string) []string {
	t.Helper()
	data, err := os.ReadFile(pythonGuard)
	if err != nil {
		t.Fatal(err)
	}
	// A Windows checkout under core.autocrlf writes CRLF; the committed blob is LF.
	text := strings.ReplaceAll(string(data), "\r\n", "\n")
	_, rest, found := strings.Cut(text, "\n"+name+" = [\n")
	body, _, closed := strings.Cut(rest, "\n]\n")
	if !found || !closed {
		t.Fatalf("%s has no %s list", pythonGuard, name)
	}
	var sources []string
	for _, match := range pythonRawString.FindAllStringSubmatch(body+"\n", -1) {
		sources = append(sources, match[1])
	}
	return sources
}

// TestPythonGuardCarriesTheBuiltinEvasionList holds praetor's own Python guard to the
// engine's evasion list byte for byte. The behavioural replay above only covers the corpus;
// this is what keeps a rule added on one side from being missing on the other.
func TestPythonGuardCarriesTheBuiltinEvasionList(t *testing.T) {
	got := pythonPatternList(t, "BLOCKED_PATTERNS")
	if !slices.Equal(got, builtinEvasion) {
		t.Errorf("BLOCKED_PATTERNS in %s differs from builtinEvasion:\npython %q\ngo     %q", pythonGuard, got, builtinEvasion)
	}
	if len(got) < 10 {
		t.Errorf("the evasion list lost rules: %d", len(got))
	}
}

// TestPythonGuardCarriesTheBuiltinDevRootRule holds praetor's own Python guard to the engine's
// generic dev-root rule byte for byte, so neither side names an organisation folder the other
// does not.
func TestPythonGuardCarriesTheBuiltinDevRootRule(t *testing.T) {
	got := pythonPatternList(t, "TOPOLOGY_PATTERNS")
	if !slices.Equal(got, []string{builtinDevRoot}) {
		t.Errorf("TOPOLOGY_PATTERNS in %s differs from builtinDevRoot:\npython %q\ngo     %q", pythonGuard, got, builtinDevRoot)
	}
}

// pythonIntConstant returns the value of a top-level `NAME = <digits>` line of the Python guard.
func pythonIntConstant(t *testing.T, name string) string {
	t.Helper()
	data, err := os.ReadFile(pythonGuard)
	if err != nil {
		t.Fatal(err)
	}
	match := regexp.MustCompile(`(?m)^` + name + ` = ([0-9]+)\r?$`).FindSubmatch(data)
	if match == nil {
		t.Fatalf("%s has no integer constant %s", pythonGuard, name)
	}
	return string(match[1])
}

// TestPythonGuardCarriesTheScanBounds holds praetor's own Python guard to the scan bounds the
// adopted interceptor is rendered with. The corpus replay cannot: the Go policy has no such
// bound, so a command beyond it is refused by the Python guard alone.
func TestPythonGuardCarriesTheScanBounds(t *testing.T) {
	for name, want := range map[string]int{"MAX_SCAN_CHARS": MaxScanChars, "MAX_SCAN_LINE_CHARS": MaxScanLineChars} {
		if got := pythonIntConstant(t, name); got != strconv.Itoa(want) {
			t.Errorf("%s in %s is %s, agenthook's bound is %d", name, pythonGuard, got, want)
		}
	}
	if MaxScanLineChars <= 0 || MaxScanLineChars > MaxScanChars || MaxScanChars >= MaxInputBytes {
		t.Errorf("scan bounds out of order: line %d, command %d, input %d", MaxScanLineChars, MaxScanChars, MaxInputBytes)
	}
}

func TestParityWithThePythonGuardOnTheEnvironment(t *testing.T) {
	interpreter := pythonInterpreter(t)
	root := repository(t, true)
	for _, variable := range []string{"", "LEFTHOOK=0", "LEFTHOOK=false", "LEFTHOOK=False", "LEFTHOOK=1", "LEFTHOOK_EXCLUDE=lint", "LEFTHOOK_SKIP=pre-push"} {
		var extra []string
		values := map[string]string{}
		if variable != "" {
			extra = []string{variable}
			key, value, _ := bytes.Cut([]byte(variable), []byte("="))
			values[string(key)] = string(value)
		}
		python := runGuardScript(t, interpreter, pythonGuard, nil, []string{"--environment"}, guardEnvironment(extra...))
		response := Run(context.Background(), Invocation{
			Client: "lefthook", Event: "environment", Getenv: func(key string) string { return values[key] },
			WorkDir: root, Policy: policy(t),
		})
		if python.exit != response.ExitCode {
			t.Errorf("%q: python exit %d, go exit %d (%s)", variable, python.exit, response.ExitCode, response.Stderr)
		}
		if drift := refusalDrift(variable, python.stderr, response.Stderr); drift != "" {
			t.Error(drift)
		}
	}
}
