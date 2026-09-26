package adopt

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cordanaLLM/praetor/internal/agenthook"
)

// evasionCorpus is agenthook's replay corpus: the payloads praetor's own guard and the Go
// policy are held to, both directions (HISS-20).
var evasionCorpus = filepath.Join("..", "agenthook", "testdata", "pre-tool", "cases.json")

type evasionCase struct {
	Name     string          `json:"name"`
	Allow    bool            `json:"allow"`
	Operator bool            `json:"operator"`
	Payload  json.RawMessage `json:"payload"`
}

// emittedInterceptor writes the interceptor adoption renders and returns the interpreter
// and script path, or skips with the reason on a host without python3 (HISS-21).
func emittedInterceptor(t *testing.T) (string, string) {
	t.Helper()
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 is not on PATH; the emitted interceptor cannot run on this leg")
	}
	script := filepath.Join(t.TempDir(), "block_evasion.py")
	mustWrite(t, script, buildBlockEvasionPY())
	return python, script
}

// runInterceptor feeds stdin (and optional argv) to the emitted script under a clean
// environment plus extra, and returns its exit code.
func runInterceptor(t *testing.T, python, script string, stdin []byte, extra []string, args ...string) int {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, python, append([]string{"-B", script}, args...)...) //nolint:gosec // fixed interpreter and test script
	cmd.Stdin = bytes.NewReader(stdin)
	cmd.Env = append([]string{"PATH=" + os.Getenv("PATH"), "SYSTEMROOT=" + os.Getenv("SYSTEMROOT")}, extra...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	err := cmd.Run()
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		if strings.Contains(stderr.String(), "Traceback") {
			t.Errorf("interceptor crashed instead of refusing: %s", stderr.String())
		}
		return exitErr.ExitCode()
	}
	if err != nil {
		t.Fatalf("run interceptor: %v", err)
	}
	return 0
}

// TestEmittedInterceptorReplaysTheEngineCorpus pins BUG-807 and BUG-808: the emitted script
// carries the engine's rules, so it allows and denies what the engine does, and it refuses
// every malformed shape with the blocking exit 2. Operator rules are repository data and
// are not shipped, so an operator-only case is allowed here.
func TestEmittedInterceptorReplaysTheEngineCorpus(t *testing.T) {
	python, script := emittedInterceptor(t)
	data, err := os.ReadFile(evasionCorpus)
	if err != nil {
		t.Fatal(err)
	}
	var cases []evasionCase
	if err := json.Unmarshal(data, &cases); err != nil || len(cases) < 60 {
		t.Fatalf("corpus unreadable or shrunk: %d cases, %v", len(cases), err)
	}
	for _, tc := range cases {
		want := 2
		if tc.Allow || tc.Operator {
			want = 0
		}
		if got := runInterceptor(t, python, script, tc.Payload, nil); got != want {
			t.Errorf("%s: exit %d, want %d", tc.Name, got, want)
		}
	}
}

// TestEmittedInterceptorRefusesUnreadableInput covers the byte-level shapes JSON cannot
// express: each used to be allowed or to crash with the non-blocking exit 1.
func TestEmittedInterceptorRefusesUnreadableInput(t *testing.T) {
	python, script := emittedInterceptor(t)
	valid := []byte(`{"tool_input":{"command":"git status"}}`)
	boundary := append(bytes.Clone(valid), bytes.Repeat([]byte(" "), agenthook.MaxInputBytes-len(valid))...)
	for name, tc := range map[string]struct {
		stdin []byte
		want  int
	}{
		"empty stdin":                {nil, 2},
		"raw text":                   {[]byte("git status"), 2},
		"JSON array":                 {[]byte(`[{"tool_input":{"command":"git status"}}]`), 2},
		"cursor shape":               {[]byte(`{"command":"git status","cwd":"/w"}`), 2},
		"invalid UTF-8":              {[]byte{0xff}, 2},
		"deeply nested":              {append(bytes.Repeat([]byte("["), 5000), bytes.Repeat([]byte("]"), 5000)...), 2},
		"exactly one mebibyte":       {boundary, 0},
		"one mebibyte plus one":      {append(bytes.Clone(boundary), ' '), 2},
		"denial with trailing space": {append([]byte(`{"tool_input":{"command":"lefthook uninstall"}}`), bytes.Repeat([]byte(" "), 16)...), 2},
	} {
		if got := runInterceptor(t, python, script, tc.stdin, nil); got != tc.want {
			t.Errorf("%s: exit %d, want %d", name, got, tc.want)
		}
	}
}

// TestEmittedInterceptorEnvironmentAndArguments covers the environment check, rendered from
// agenthook's lists, and the argv entry point.
func TestEmittedInterceptorEnvironmentAndArguments(t *testing.T) {
	python, script := emittedInterceptor(t)
	benign := []byte(`{"tool_input":{"command":"go test ./..."}}`)
	for variable, want := range map[string]int{
		"LEFTHOOK=0": 2, "LEFTHOOK=false": 2, "LEFTHOOK=1": 0, "LEFTHOOK=False": 0,
		"LEFTHOOK_EXCLUDE=lint": 2, "LEFTHOOK_SKIP=pre-push": 2, "LEFTHOOK_VERBOSE=1": 0,
	} {
		if got := runInterceptor(t, python, script, benign, []string{variable}); got != want {
			t.Errorf("%s: exit %d, want %d", variable, got, want)
		}
	}
	if got := runInterceptor(t, python, script, nil, nil, "rm", "-rf", ".git/hooks"); got != 2 {
		t.Errorf("argv denial: exit %d", got)
	}
	if got := runInterceptor(t, python, script, nil, nil, "git", "status"); got != 0 {
		t.Errorf("argv allow: exit %d", got)
	}
}

// TestBuildBlockEvasionPY_NoOperatorData asserts the rendered script carries every built-in
// rule and none of praetor's own organisation names (ADR-0011: operator data is configured,
// never shipped as engine rules).
func TestBuildBlockEvasionPY_NoOperatorData(t *testing.T) {
	script := buildBlockEvasionPY()
	for _, rule := range agenthook.BuiltinRules() {
		if !strings.Contains(script, `(r"`+rule.Source+`", "`+rule.Invariant+`"),`) {
			t.Errorf("rendered script lacks rule %q", rule.Source)
		}
	}
	for _, operator := range []string{"cordanaLLM", "lusoris", "vmafx", "golusoris", "TOPOLOGY_PATTERNS"} {
		if strings.Contains(script, operator) {
			t.Errorf("rendered script ships operator data %q", operator)
		}
	}
	if strings.Contains(script, "{{") {
		t.Error("rendered script has an unfilled placeholder")
	}
}
