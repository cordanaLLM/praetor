package adopt

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cordanaLLM/praetor/internal/agenthook"
	"github.com/cordanaLLM/praetor/internal/testsupport"
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

// interceptorRules loads the emitted script as a module, without running main, and returns
// its RULES as (pattern, refusal) pairs: the values Python reads back from the adjacent
// literals the layout splits long patterns into.
func interceptorRules(t *testing.T, python, script string) [][2]string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), interceptorWallClock)
	defer cancel()
	const program = "import json, runpy, sys; module = runpy.run_path(sys.argv[1]); print(json.dumps(module['RULES']))"
	out, err := exec.CommandContext(ctx, python, "-B", "-c", program, script).Output() //nolint:gosec // fixed interpreter and test script
	if err != nil {
		t.Fatalf("load the emitted script's rules: %v", err)
	}
	var rules [][2]string
	if err := json.Unmarshal(out, &rules); err != nil {
		t.Fatalf("decode the emitted script's rules: %v", err)
	}
	return rules
}

// runInterceptor feeds stdin (and optional argv) to the emitted script under a clean
// environment plus extra, and returns its exit code.
func runInterceptor(t *testing.T, python, script string, stdin []byte, extra []string, args ...string) int {
	t.Helper()
	code, _ := runInterceptorOutput(t, python, script, stdin, extra, args...)
	return code
}

// interceptorWallClock is the ceiling on one interceptor run, a hook timeout's worth of
// wall-clock time. The scan-bound test bounds the matcher's work instead (scanPassBudget).
const interceptorWallClock = 30 * time.Second

// runInterceptorOutput runs the emitted interceptor and returns its exit code and its stderr
// as LF text.
func runInterceptorOutput(t *testing.T, python, script string, stdin []byte, extra []string, args ...string) (int, string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), interceptorWallClock)
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
		return exitErr.ExitCode(), interceptorStderr(t, stderr.Bytes())
	}
	if err != nil {
		t.Fatalf("run interceptor: %v", err)
	}
	return 0, interceptorStderr(t, stderr.Bytes())
}

// interceptorStderr reads the interceptor's stderr as text (testsupport.PythonText): CPython
// writes it with CRLF line endings on Windows, and the engine's refusals end in LF.
func interceptorStderr(t *testing.T, stderr []byte) string {
	t.Helper()
	text, err := testsupport.PythonText(stderr)
	if err != nil {
		t.Errorf("interceptor stderr %q: %v", stderr, err)
		return string(stderr)
	}
	return string(text)
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
// rule and no organisation-folder alternation (ADR-0011: operator data is configured, never
// shipped as engine rules). praetor's own guard names none either
// (TestPythonGuardCarriesTheBuiltinDevRootRule in internal/agenthook).
func TestBuildBlockEvasionPY_NoOperatorData(t *testing.T) {
	python, path := emittedInterceptor(t)
	rules := interceptorRules(t, python, path)
	builtins := agenthook.BuiltinRules()
	if len(rules) != len(builtins) {
		t.Fatalf("rendered script carries %d rules, the engine %d", len(rules), len(builtins))
	}
	for i, rule := range builtins {
		if rules[i][0] != rule.Source || !strings.HasPrefix(rules[i][1], "[BLOCKED BY "+rule.Invariant+"] ") {
			t.Errorf("rule %d: rendered %q, engine %q (%s)", i, rules[i], rule.Source, rule.Invariant)
		}
	}
	script := buildBlockEvasionPY()
	for _, operator := range []string{"TOPOLOGY_PATTERNS", "/dev/("} {
		if strings.Contains(script, operator) {
			t.Errorf("rendered script ships operator data %q", operator)
		}
	}
	if strings.Contains(script, "{{") {
		t.Error("rendered script has an unfilled placeholder")
	}
}

// scanPassBudget bounds the work the emitted interceptor's matcher may spend on one command
// inside the scan bounds, in passes. One pass is what the matcher spends on a benign command
// at the bound (benignBoundCommand), every rule reading it once. Both are measured in the same
// run on the same interpreter (scanCostProbe), so the budget means the same work on every
// runner; a fixed CPU-time budget of 3 s failed on a hosted runner about 3.3 times slower than
// a workstation (#829). The costliest shapes here, the find lines and `$` runs before a brace
// after `git am`, cost about 10 passes; a rule that backtracks superlinearly through the bound
// costs hundreds (TestScanPassBudgetRefusesPlantedSuperlinearMatchers).
const scanPassBudget = 50

// scanCostProbe measures the emitted interceptor's matcher in its own process. It loads the
// script as a module, so main does not run, and times check_command with CPU time: the pass
// on the reference command in three batches of at least 0.2 s each, because process_time
// advances in 15.6 ms ticks on Windows, keeping the cheapest; then each command `runs` times,
// keeping the least time with the verdict (2 for a refusal). The least reading is the one
// Python's timeit documentation recommends: higher readings come from interference, not from
// the code.
const scanCostProbe = `import json, runpy, sys, time

module = runpy.run_path(sys.argv[1])
check, blocked = module["check_command"], module["Blocked"]
request = json.loads(sys.stdin.buffer.read().decode("utf-8"))


def timed(command):
    start = time.process_time()
    try:
        check(command)
    except blocked:
        return time.process_time() - start, 2
    return time.process_time() - start, 0


def one_pass(command):
    best = None
    for _ in range(3):
        start, count = time.process_time(), 0
        while count < 100000:
            timed(command)
            count += 1
            spent = time.process_time() - start
            if spent >= 0.2:
                break
        best = spent / count if best is None else min(best, spent / count)
    return best


def least(command, runs):
    readings = [timed(command) for _ in range(runs)]
    return [min(reading[0] for reading in readings), readings[0][1]]


costs = [least(command, request["runs"]) for command in request["commands"]]
print(json.dumps({"pass": one_pass(request["reference"]), "costs": costs}))
`

// scanCosts is what scanCostProbe reports: the CPU seconds of one pass and, per command, the
// least CPU seconds over its runs and its verdict.
type scanCosts struct {
	Pass  float64      `json:"pass"`
	Costs [][2]float64 `json:"costs"`
}

// passes is the cost of command index in passes.
func (c scanCosts) passes(index int) float64 { return c.Costs[index][0] / c.Pass }

// measureScanCosts runs scanCostProbe against script for the commands, each timed runs times.
func measureScanCosts(t *testing.T, python, script string, commands []string, runs int) scanCosts {
	t.Helper()
	input, err := json.Marshal(map[string]any{"reference": benignBoundCommand(), "commands": commands, "runs": runs})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*interceptorWallClock)
	defer cancel()
	cmd := exec.CommandContext(ctx, python, "-B", "-c", scanCostProbe, script) //nolint:gosec // fixed interpreter, program and test script
	cmd.Stdin = bytes.NewReader(input)
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("measure the interceptor's scan costs: %v", err)
	}
	var costs scanCosts
	if err := json.Unmarshal(out, &costs); err != nil || costs.Pass <= 0 || len(costs.Costs) != len(commands) {
		t.Fatalf("scan cost probe output %q: %v", out, err)
	}
	return costs
}

// scanShape is one command the scan-bound tests send and the verdict the interceptor owes it.
type scanShape struct {
	name    string
	command string
	want    int
}

// scanBudgetFindings returns one line per shape whose verdict differs from its want or whose
// cost exceeds scanPassBudget passes.
func scanBudgetFindings(shapes []scanShape, costs scanCosts) []string {
	var findings []string
	for index, shape := range shapes {
		if verdict := int(costs.Costs[index][1]); verdict != shape.want {
			findings = append(findings, fmt.Sprintf("%s: verdict %d, want %d", shape.name, verdict, shape.want))
		}
		if passes := costs.passes(index); passes > scanPassBudget {
			findings = append(findings, fmt.Sprintf("%s: %.1f passes (%.3f s of CPU time, one pass %.4f s), over the budget of %d",
				shape.name, passes, costs.Costs[index][0], costs.Pass, scanPassBudget))
		}
	}
	return findings
}

// boundLine is prefix followed by unit repeated, cut to width characters.
func boundLine(prefix, unit string, width int) string {
	return (prefix + strings.Repeat(unit, width/len(unit)+1))[:width]
}

// boundLines fills the scan bound with lines of unit repeated, each one character short of
// agenthook.MaxScanLineChars so the line break fits.
func boundLines(unit string) string {
	line := boundLine("", unit, agenthook.MaxScanLineChars-1) + "\n"
	return strings.Repeat(line, agenthook.MaxScanChars/agenthook.MaxScanLineChars)
}

// benignBoundCommand fills the scan bound with lines of x: every rule reads it once and none
// matches. Its cost is the pass scanPassBudget counts in.
func benignBoundCommand() string { return boundLines("x") }

// scanBoundShapes are the commands TestEmittedInterceptorScanBounds sends: the line and command
// bounds, exactly at and one over, and the costliest shapes measured against the rules, each
// filling the bound. Those are the find line and lines of `sed -i `, which cost cubic time
// before the two rules started at the first command word of a line, and `$` runs before a
// brace inside an open double quote after `git am` and after SKIP= (the costliest shapes the
// reviews of an earlier quote-aware scan measured). A line break turns the read-only exemption
// off (agenthook.ReadOnlyVeto), so it only ever reads one line; its shapes are single lines at
// the line bound: a read-only command with a full line of words, separators back to back, a
// separator before blanks, a Git name before blanks.
//
// One shape is left to test_guard_answers_the_slowest_admitted_commands_inside_the_bound in
// .config/lefthook/scripts/test_hooks.py: Git global options repeated to the line bound (` -git`).
// The commit rule restarts at every git word of the chain and reads the options after it, so
// it costs quadratic time in the line, about 300 passes, and no anchor keeps that rule's
// language.
func scanBoundShapes() []scanShape {
	width := agenthook.MaxScanLineChars
	blanks := strings.Repeat(" \t", width)
	full := benignBoundCommand()
	return []scanShape{
		{"pathological find line", strings.Repeat("find .git/hooks ", 1024), 2},
		{"line at the bound", strings.Repeat("x", width), 0},
		{"line over the bound", strings.Repeat("x", width+1), 2},
		{"command at the bound", full, 0},
		{"command over the bound", full + "x", 2},
		{"find lines", boundLines("find .git/hooks "), 0},
		{"in-place edit lines", boundLines("sed -i "), 0},
		{"am before odd dollar runs", boundLines(`git am "$$$$$$$$$${`), 0},
		{"am before even dollar run", boundLines(`git am "$$$${`), 0},
		{"skip variable dollar runs", boundLines(`SKIP="$$$$$${`), 0},
		{"read-only words to the end", boundLine("git commit -m x; git log", " -n", width), 0},
		{"read-only words on lines", boundLines("git commit -m x; git log -n 5 "), 2},
		{"separators back to back", boundLine("", "git commit -m x || ", width), 0},
		{"separator before blanks", boundLine("git commit -m x;", blanks, width-1) + "x", 0},
		{"read-only name after blanks", boundLine("git commit -m x;git", blanks, width-1) + "x", 0},
	}
}

// TestEmittedInterceptorScanBounds pins the scan bounds of the emitted script. Python's re
// backtracks: one 16 KiB find..hooks line held the find rule for 16 s, past a harness's hook
// timeout, and a harness that lets a timed-out hook through turns the stall into an evasion.
// A command over agenthook's bounds is refused at once, and each scanBoundShapes command gets
// its verdict, end to end through stdin and in the matcher's own process, within
// scanPassBudget passes. The bound counts work, not seconds, so a slower or loaded runner does
// not fail it; the wall-clock ceilings are the runs' contexts.
func TestEmittedInterceptorScanBounds(t *testing.T) {
	python, script := emittedInterceptor(t)
	shapes := scanBoundShapes()
	commands := make([]string, 0, len(shapes))
	for _, shape := range shapes {
		payload, err := json.Marshal(map[string]any{"tool_input": map[string]string{"command": shape.command}})
		if err != nil {
			t.Fatal(err)
		}
		if got := runInterceptor(t, python, script, payload, nil); got != shape.want {
			t.Errorf("%s: exit %d, want %d", shape.name, got, shape.want)
		}
		commands = append(commands, shape.command)
	}
	costs := measureScanCosts(t, python, script, commands, 3)
	for index, shape := range shapes {
		t.Logf("%s: %.1f passes", shape.name, costs.passes(index))
	}
	for _, finding := range scanBudgetFindings(shapes, costs) {
		t.Error(finding)
	}
}

// plantedInterceptor returns the rendered interceptor with pattern added as its first rule.
func plantedInterceptor(t *testing.T, pattern string) string {
	t.Helper()
	rendered := buildBlockEvasionPY()
	planted := strings.Replace(rendered, "RULES = [\n", "RULES = [\n    (r\""+pattern+"\", HISS_REFUSAL),\n", 1)
	if planted == rendered {
		t.Fatal("the rendered interceptor has no RULES list to plant a rule in")
	}
	return planted
}

// TestScanPassBudgetRefusesPlantedSuperlinearMatchers proves the budget (rule 13): the same
// measurement refuses an interceptor with a superlinear rule planted ahead of the real ones,
// on the shape that rule backtracks through, a shape the real interceptor answers within the
// budget (TestEmittedInterceptorScanBounds). The plants are a nested quantifier over the
// dollar runs, which tries every way to split each run, and the find rule as it stood before
// #829, cubic in the line length. Neither matches its shape, so the verdict stays an allow.
func TestScanPassBudgetRefusesPlantedSuperlinearMatchers(t *testing.T) {
	python, _ := emittedInterceptor(t)
	shapes := map[string]scanShape{}
	for _, shape := range scanBoundShapes() {
		shapes[shape.name] = shape
	}
	for _, tc := range []struct{ name, pattern, shape string }{
		{"nested quantifier over the dollar runs", `(\$+\$?)+\{x`, "am before odd dollar runs"},
		{"find rule before #829", `\bfind\b[^\n]*(?i:\.git[/\\]hooks)[^\n]*\s-(delete|exec|execdir|ok)\b`, "find lines"},
	} {
		script := filepath.Join(t.TempDir(), "planted_block_evasion.py")
		mustWrite(t, script, plantedInterceptor(t, tc.pattern))
		shape := shapes[tc.shape]
		costs := measureScanCosts(t, python, script, []string{shape.command}, 1)
		findings := scanBudgetFindings([]scanShape{shape}, costs)
		t.Logf("%s on %s: %.1f passes", tc.name, tc.shape, costs.passes(0))
		if len(findings) != 1 || !strings.Contains(findings[0], "over the budget") {
			t.Errorf("%s: the budget did not refuse the planted rule on %s: %q", tc.name, tc.shape, findings)
		}
	}
}

// TestEmittedInterceptorRefusesInTheEngineWords pins BUG-1014 for the interceptor adoption
// renders: each refusal prints agenthook's text character for character, for an evasion flag,
// the dev-root rule, a command over the scan bound, a disabled or narrowed Lefthook run and
// unreadable input (whose parser error after the shared prefix is Python's own). A command
// exactly at the scan bound and a benign one print nothing.
func TestEmittedInterceptorRefusesInTheEngineWords(t *testing.T) {
	python, script := emittedInterceptor(t)
	builtin, err := agenthook.NewPolicy(nil)
	if err != nil {
		t.Fatal(err)
	}
	payload := func(command string) []byte {
		data, err := json.Marshal(map[string]any{"tool_input": map[string]string{"command": command}})
		if err != nil {
			t.Fatal(err)
		}
		return data
	}
	benign := payload("go test ./...")
	for _, tc := range []struct {
		name  string
		stdin []byte
		env   []string
		want  string
	}{
		{"evasion flag", payload("git push --no-verify"), nil, builtin.Command("git push --no-verify").Reason + "\n"},
		{"dev root", payload("praetorctl adopt /srv/dev"), nil, builtin.Command("praetorctl adopt /srv/dev").Reason + "\n"},
		{"scan bound exceeded", payload(strings.Repeat("x", agenthook.MaxScanLineChars+1)), nil, agenthook.ScanBoundRefusal() + "\n"},
		{"scan bound exactly", payload(strings.Repeat("x", agenthook.MaxScanLineChars)), nil, ""},
		{"benign", benign, nil, ""},
		{"lefthook disabled", benign, []string{"LEFTHOOK=false"}, agenthook.LefthookDisabledRefusal("false") + "\n"},
		{"lefthook narrowed", benign, []string{"LEFTHOOK_SKIP=lint"}, agenthook.NarrowingRefusal + "\n"},
	} {
		code, stderr := runInterceptorOutput(t, python, script, tc.stdin, tc.env)
		if stderr != tc.want || (code == 0) != (tc.want == "") {
			t.Errorf("%s: exit %d\ngot  %q\nwant %q", tc.name, code, stderr, tc.want)
		}
	}
	if _, stderr := runInterceptorOutput(t, python, script, []byte("not json"), nil); !strings.HasPrefix(stderr, agenthook.InvalidInputRefusal) {
		t.Errorf("unreadable input: %q lacks the engine's prefix", stderr)
	}
}

// TestEmittedInterceptorRefusesInTheEngineWordsUnderTheWindowsNewline runs the emitted
// interceptor with CPython's Windows newline translation on its standard streams
// (testsupport.WindowsNewlinePython), so a run on any host shows what the Windows leg sees:
// a refusal still reads as the engine's text, and an allowed command still prints nothing.
func TestEmittedInterceptorRefusesInTheEngineWordsUnderTheWindowsNewline(t *testing.T) {
	python, script := emittedInterceptor(t)
	launcher := testsupport.WindowsNewlinePython(t, script)
	builtin, err := agenthook.NewPolicy(nil)
	if err != nil {
		t.Fatal(err)
	}
	benign := []byte(`{"tool_input":{"command":"go test ./..."}}`)
	for _, tc := range []struct {
		name  string
		stdin []byte
		env   []string
		want  string
	}{
		{"evasion flag", []byte(`{"tool_input":{"command":"git push --no-verify"}}`), nil, builtin.Command("git push --no-verify").Reason + "\n"},
		{"lefthook narrowed", benign, []string{"LEFTHOOK_SKIP=lint"}, agenthook.NarrowingRefusal + "\n"},
		{"benign", benign, nil, ""},
	} {
		code, stderr := runInterceptorOutput(t, python, launcher, tc.stdin, tc.env)
		if stderr != tc.want || (code == 0) != (tc.want == "") {
			t.Errorf("%s: exit %d\ngot  %q\nwant %q", tc.name, code, stderr, tc.want)
		}
	}
}
