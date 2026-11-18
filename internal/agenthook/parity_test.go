package agenthook

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/config"
	"github.com/cordanaLLM/praetor/internal/testsupport"
	"github.com/cordanaLLM/praetor/internal/util"
)

// pythonGuard is the implementation the Go policy replaces. The parity replay lives
// exactly as long as that file: the change that deletes the adapters deletes this test.
var pythonGuard = filepath.Join("..", "..", ".config", "agent", "hooks", "block_evasion.py")

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
// printed the policy marker, and its stderr. The test deadline bounds pathological
// backtracking without treating process startup latency as a policy defect.
func runGuardScript(t *testing.T, interpreter, script string, payload []byte, arguments []string, environment []string) guardResult {
	t.Helper()
	ctx := testsupport.DeadlineContext(t)
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
// empty stderr of an allowed command, must match character for character (BUG-1014). The
// guard's stderr is read as text first (testsupport.PythonText): CPython writes it with CRLF
// line endings on Windows, the engine with LF everywhere, and the line ending is not wording.
func refusalDrift(name string, stderr, engine []byte) string {
	python, err := testsupport.PythonText(stderr)
	if err != nil {
		return name + ": python stderr " + strconv.Quote(string(stderr)) + ": " + err.Error()
	}
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
	interpreter := testsupport.PythonInterpreter(t)
	root := repository(t, true)
	builtin := policy(t)
	payloads := rawCases()
	for _, fixture := range loadCases(t) {
		payloads = append(payloads, rawCase{fixture.Name, fixture.Payload, fixture.Allow || fixture.Operator})
	}
	for _, tc := range payloads {
		python := runGuardScript(t, interpreter, pythonGuard, tc.payload, nil, guardEnvironment())
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
		{"gh pr merge admin", "gh pr merge 123 --admin", []byte(BuiltinRules()[10].RefusalPrefix() + builtinEvasion[10] + "\n")},
		{"gh api merge", "gh api repos/owner/repo/pulls/123/merge", []byte(BuiltinRules()[11].RefusalPrefix() + builtinEvasion[11] + "\n")},
		{"gh api graphql merge", "gh api graphql -f query='mutation { mergePullRequest }'", []byte(BuiltinRules()[12].RefusalPrefix() + builtinEvasion[12] + "\n")},
		{"curl pulls merge", "curl https://api.github.com/repos/owner/repo/pulls/123/merge", []byte(BuiltinRules()[13].RefusalPrefix() + builtinEvasion[13] + "\n")},
		{"gh pr merge admin continuation", "gh pr merge 5 \\\n  --admin", []byte(BuiltinRules()[10].RefusalPrefix() + builtinEvasion[10] + "\n")},
		{"gh api merge continuation", "gh api -X PUT \\\n repos/owner/repo/pulls/5/merge", []byte(BuiltinRules()[11].RefusalPrefix() + builtinEvasion[11] + "\n")},
		{"continuation allowed", "gh pr merge 5 \\\n--squash", nil},
		{"plain newline allowed", "echo gh pr merge 5\necho --admin", nil},
		{"dev root", "praetorctl adopt /home/user/dev", []byte(BuiltinRules()[len(builtinEvasion)].RefusalPrefix() + builtinDevRoot + "\n")},
		{"scan bound exceeded", strings.Repeat("x", MaxScanLineChars+1), []byte(ScanBoundRefusal() + "\n")},
		{"scan bound exactly", strings.Repeat("x", MaxScanLineChars), nil},
		{"privilege refusal bounded", "sudo -n true", []byte(boundReason(rulePrefix("HISS", evasionMessage)+privilegeCommandRule()) + "\n")},
		{"benign", "git status", nil},
	}
}

// TestParityRefusalTextWithThePythonGuard holds praetor's own guard to the engine's wording:
// for each refusal case its stderr is the engine's text character for character, and an
// allowed command prints nothing. The engine's text comes from the Go policy itself where the
// policy judges the command.
func TestParityRefusalTextWithThePythonGuard(t *testing.T) {
	interpreter := testsupport.PythonInterpreter(t)
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

// TestParityRefusalTextSurvivesTheWindowsNewline replays the refusal cases and one
// environment refusal through a launcher that gives the guard's standard streams CPython's
// Windows newline translation, so a run on any host shows what the Windows leg sees: each
// refusal arrives as CRLF text carrying the engine's words, and the approval marker, written as
// bytes, still parses. Mixed line endings stay drift rather than pass as one style.
func TestParityRefusalTextSurvivesTheWindowsNewline(t *testing.T) {
	interpreter := testsupport.PythonInterpreter(t)
	launcher := testsupport.WindowsNewlinePython(t, pythonGuard)
	for _, tc := range refusalCases() {
		python := runGuardScript(t, interpreter, launcher, commandPayload(t, map[string]any{"tool_input": map[string]string{"command": tc.command}}), nil, guardEnvironment())
		if tc.want != nil && !bytes.HasSuffix(python.stderr, []byte("\r\n")) {
			t.Errorf("%s: the launcher did not translate the newline: %q", tc.name, python.stderr)
		}
		if drift := refusalDrift(tc.name, python.stderr, tc.want); drift != "" {
			t.Error(drift)
		}
		if python.marker != (tc.want == nil) {
			t.Errorf("%s: marker %v under the Windows newline, want %v", tc.name, python.marker, tc.want == nil)
		}
	}
	narrowed := runGuardScript(t, interpreter, launcher, nil, []string{"--environment"}, guardEnvironment("LEFTHOOK_SKIP=lint"))
	if drift := refusalDrift("narrowed", narrowed.stderr, []byte(NarrowingRefusal+"\n")); drift != "" {
		t.Error(drift)
	}
	mixed := []byte(NarrowingRefusal + "\r\n" + NarrowingRefusal + "\n")
	if refusalDrift("mixed", mixed, []byte(NarrowingRefusal+"\n"+NarrowingRefusal+"\n")) == "" {
		t.Error("mixed line endings passed the comparison")
	}
}

// TestRefusalDriftFailsOnAChangedWord proves the comparison above can fail: a copy of the
// guard with one word of its evasion refusal changed is reported, as is a guard that stays
// silent where the engine refuses, while unreadable input only has to share the prefix.
func TestRefusalDriftFailsOnAChangedWord(t *testing.T) {
	interpreter := testsupport.PythonInterpreter(t)
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

// The guard's declared prefilter must match the canonical Go rule, including its
// binding to the last evasion pattern. Runtime replay covers the actual verdicts.
func TestPythonGuardCarriesTheRequiredLiterals(t *testing.T) {
	data, err := os.ReadFile(pythonGuard)
	if err != nil {
		t.Fatal(err)
	}
	quoted := make([]string, 0, len(privilegeNames))
	for _, name := range privilegeNames {
		quoted = append(quoted, strconv.Quote(name))
	}
	want := "RULE_PREFILTERS = {BLOCKED_PATTERNS[-1]: (" + strings.Join(quoted, ", ") + ")}"
	if !strings.Contains(string(data), want) || builtinEvasion[len(builtinEvasion)-1] != privilegeCommandSource {
		t.Fatal("guard prefilter differs from the Go privilege rule")
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

// pythonRawConstant returns the text of a top-level `NAME = r"..."` line of the Python guard.
func pythonRawConstant(t *testing.T, name string) string {
	t.Helper()
	data, err := os.ReadFile(pythonGuard)
	if err != nil {
		t.Fatal(err)
	}
	match := regexp.MustCompile(`(?m)^` + name + ` = r"([^"\n]*)"\r?$`).FindSubmatch(data)
	if match == nil {
		t.Fatalf("%s has no raw string constant %s", pythonGuard, name)
	}
	return string(match[1])
}

// TestPythonGuardCarriesTheReadOnlyExemption holds praetor's own Python guard to the engine's
// read-only exemption byte for byte: the rules it covers, the read-only command pattern and
// the veto. The corpus replay checks the behaviour; this keeps a change on one side from
// passing unseen where the corpus has no case for it.
func TestPythonGuardCarriesTheReadOnlyExemption(t *testing.T) {
	if got := pythonPatternList(t, "READ_ONLY_EXEMPT_PATTERNS"); !slices.Equal(got, readOnlyExemptRules) {
		t.Errorf("READ_ONLY_EXEMPT_PATTERNS in %s differs from readOnlyExemptRules:\npython %q\ngo     %q", pythonGuard, got, readOnlyExemptRules)
	}
	for name, want := range map[string]string{"READ_ONLY_WORDS": ReadOnlyWords, "READ_ONLY_VETO": ReadOnlyVeto} {
		if got := pythonRawConstant(t, name); got != want {
			t.Errorf("%s in %s differs from agenthook's:\npython %q\ngo     %q", name, pythonGuard, got, want)
		}
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
	for name, want := range map[string]int{
		"MAX_SCAN_CHARS": MaxScanChars, "MAX_SCAN_LINE_CHARS": MaxScanLineChars, "MAX_REASON_BYTES": MaxReasonBytes,
	} {
		if got := pythonIntConstant(t, name); got != strconv.Itoa(want) {
			t.Errorf("%s in %s is %s, agenthook's bound is %d", name, pythonGuard, got, want)
		}
	}
	if MaxScanLineChars <= 0 || MaxScanLineChars > MaxScanChars || MaxScanChars >= MaxInputBytes {
		t.Errorf("scan bounds out of order: line %d, command %d, input %d", MaxScanLineChars, MaxScanChars, MaxInputBytes)
	}
}

func TestParityWithThePythonGuardOnTheEnvironment(t *testing.T) {
	interpreter := testsupport.PythonInterpreter(t)
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

func buildParityPraetorctl(t *testing.T) string {
	t.Helper()
	binDir := t.TempDir()
	binName := "praetorctl"
	if runtime.GOOS == "windows" {
		binName += ".exe"
	}
	if _, err := util.RunCommand(context.Background(), "", "go", "build", "-o", filepath.Join(binDir, binName), "../../cmd/standardsctl"); err != nil {
		t.Fatalf("build praetorctl: %v", err)
	}
	return binDir
}

func TestParityWithThePythonGuardOnAdmission(t *testing.T) {
	interpreter := testsupport.PythonInterpreter(t)
	fleet := filepath.Join(t.TempDir(), "fleet.yaml")
	if err := os.WriteFile(fleet, []byte("hooks:\n  admission:\n    enforce: true\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	payload := commandPayload(t, map[string]any{"tool_input": map[string]string{"command": "make verify-all"}})
	// Enforcement off: allowed without praetorctl on PATH.
	if off := runGuardScript(t, interpreter, pythonGuard, payload, nil, []string{"PATH="}); off.exit != 0 || !off.marker {
		t.Fatalf("enforcement off without praetorctl: exit %d marker %v stderr %q", off.exit, off.marker, off.stderr)
	}
	// Enforcement on, praetorctl absent: fail closed.
	absent := runGuardScript(t, interpreter, pythonGuard, payload, nil, []string{"PATH=", "PRAETOR_FLEET_CONFIG=" + fleet})
	if absent.exit == 0 || !bytes.Contains(absent.stderr, []byte("[BLOCKED BY HISS]")) {
		t.Fatalf("praetorctl absent with enforcement on: exit %d marker %v stderr %q", absent.exit, absent.marker, absent.stderr)
	}
	// Enforcement on, praetorctl on PATH: Python guard refusal matches Go Policy.Command byte for byte.
	binDir := buildParityPraetorctl(t)
	policy, err := BuildPolicy(config.HookSettings{Admission: config.AdmissionSettings{Enforce: true}})
	if err != nil {
		t.Fatal(err)
	}
	for _, cmd := range []string{"make verify-all", "make -j999"} {
		cmdPayload := commandPayload(t, map[string]any{"tool_input": map[string]string{"command": cmd}})
		python := runGuardScript(t, interpreter, pythonGuard, cmdPayload, nil,
			[]string{"PATH=" + binDir + string(os.PathListSeparator) + os.Getenv("PATH"), "PRAETOR_FLEET_CONFIG=" + fleet})
		goVerdict := policy.Command(cmd)
		if drift := refusalDrift(cmd, python.stderr, []byte(goVerdict.Reason+"\n")); drift != "" {
			t.Error(drift)
		}
	}
}

// commitSegmentCases preserves round-one rows and plants bounded shell spans.
func commitSegmentCases() []struct {
	name, command string
	want          int
} {
	return []struct {
		name, command string
		want          int
	}{
		{"chained tail", `git commit -m 'fix: message' && git log -1 | tail -n 1`, 0},
		{"chained tail with ref", `git commit -m 'fix: message' && git log -1 "$ref" | tail -n 1`, 0},
		{"quoted separator then flag", `git commit -m 'message && text' -n`, 2},
		{"bare short flag", `git commit -n`, 2},
		{"long flag", `git commit --no-` + `verify`, 2},
		{"am short flag", `git am -n patch`, 2},
		{"am long flag", `git am --no-` + `verify patch`, 2},
		{"unbalanced quote", `git commit -m 'unfinished`, 2},
		{"exempt span", `git commit -m 'message $name' && tail -n 1`, 0},
		{"quoted operator token", `git commit -m '&&' -n`, 2},
		{"escaped separator", `git commit -m message\&\&text -n`, 2},
		{"nested backtick quotes", "git commit -m \"`echo \"a;b\"`\" -n", 2},
		{"semicolon", `git commit -m 'fix' ; tail -n 1`, 0},
		{"or", `git commit -m 'fix' || tail -n 1`, 0},
		{"pipe", `git commit -m 'fix' | tail -n 1`, 0},
		{"background", `git commit -m 'fix' & tail -n 1`, 0},
		{"next commit", `git commit -m 'fix' && git commit -n`, 2},
		{"redirect pipe", `git commit -m x 2>&1 | tail -n 1`, 0},
		{"substitution message", `git commit -m "$(cat msg.txt)" && git log -1 | tail -n 1`, 0},
		{"redirect grep then log", `git commit -F msg.txt 2>&1 | grep -c ok; git log -1 | tail -n 1`, 0},
		{"heredoc message", "git commit -m \"$(cat <<'EOF'\nmessage ; && |\nEOF\n)\" && git log -1 | tail -n 1", 0},
		{"redirect then flag", `git commit -m x 2>&1 -n`, 2},
		{"substitution flag", `git commit "$(printf %s -n)"`, 2},
		// Message backticks remain data; positional backticks retain fallback.
		{"backtick message", "git commit -m \"`cat msg.txt`\" && tail -n 1", 0},
		{"backtick flag", "git commit \"`printf %s -n`\"", 2},
		{"nested substitution", `git commit -m "$(echo "$(cat msg.txt)")" && tail -n 1`, 0},
		{"nested substitution flag", `git commit "$(echo "$(printf %s -n)")"`, 2},
		// Outer newlines separate commands.
		{"newline", "git commit -m x\ngit log -1 | tail -n 1", 0},
		{"file redirections", `git commit -F msg.txt >>out 2>/dev/null <in && tail -n 1`, 0},
		{"descriptor input", `git commit -m x 3<&0 2>&- & tail -n 1`, 0},
		{"heredoc outer", "git commit -F - <<EOF\nmessage ' ; && |\nEOF\ntail -n 1", 0},
		{"heredoc tabs", "git commit -F - <<-EOF\n\tmessage\n\tEOF\ntail -n 1", 0},
		{"multiple heredocs", "git commit -F - <<A <<\"B\"\nfirst\nA\nsecond\nB\ntail -n 1", 0},
		{"substitution separator flag", `git commit "$(cat msg.txt; printf %s -n)"`, 2},
		{"unterminated substitution", `git commit -m "$(cat msg.txt" && tail -n 1`, 2},
		{"unterminated heredoc", "git commit -F - <<EOF\nmessage && tail -n 1", 2},
		{"state alias", `alias git=other; git commit -m x && tail -n 1`, 2},
		{"state export", `export X=1; git commit -m x && tail -n 1`, 2},
		{"state function", `function git() { other; }; git commit -m x && tail -n 1`, 2},
		// The nesting bound admits parsing; unquoted execution retains splitting.
		{"nesting at bound", "git commit -m " + strings.Repeat("$(", 16) + "cat msg.txt" + strings.Repeat(")", 16) + " && tail -1", 2},
		{"nesting past bound", "git commit -m " + strings.Repeat("$(", 17) + "cat msg.txt" + strings.Repeat(")", 17) + " && tail -n 1", 2},
		{"heredoc lines at bound", "git commit -F - <<EOF\n" + strings.Repeat("x\n", 255) + "EOF\ntail -n 1", 0},
		{"heredoc lines past bound", "git commit -F - <<EOF\n" + strings.Repeat("x\n", 256) + "EOF\ntail -n 1", 2},
	}
}

// fallbackSegmentCases covers order engine-block-evasion-commit-span-20261114 round 3.
func fallbackSegmentCases() []struct {
	name, command string
	want          int
} {
	return []struct {
		name, command string
		want          int
	}{
		// Order engine-block-evasion-commit-span-20261114 round 3: own syntax vetoes segmentation.
		{"own parameter", `git commit $flags -m x && tail -n 1`, 2},
		{"am parameter", `git am $flags patch && tail -n 1`, 2},
		{"own substitution", `git commit $(cat flags) -m x && tail -n 1`, 2},
		{"own escape", `git commit -m a\b && tail -n 1`, 2},
		{"own continuation", "git commit -m x\\\n && tail -n 1", 0},
		{"single quoted newline", "git commit -m 'a\nb' && tail -n 1", 2},
		{"double quoted newline", "git commit -m \"a\nb\" && tail -n 1", 2},
		{"later newline", "git commit -m x && printf \"a\nb\" | tail -n 1", 0},
		{"later escape", `git commit -m x && printf a\b | tail -n 1`, 0},
		{"later parameter", `git commit -m x && git log $flags | tail -n 1`, 0},
		{"quoted parameter", `git commit -m 'a$b' && tail -n 1`, 0},
		{"quoted escape", `git commit -m 'a\b' && tail -n 1`, 0},
		{"own input", `git commit -m a<b && tail -n 1`, 0},
		{"own output", `git commit -m a>b && tail -n 1`, 0},
		{"background log", `git commit -m x & git log -n 1`, 0},
		{"combined pipe", `git commit -m x |& head -n 3`, 0},
		{"unrelated quote", `printf 'unfinished`, 0},
		{"unrelated git quote", `git status 'unfinished`, 0},
		{"commit option quote", `git -C repo --no-pager commit -m 'unfinished`, 2},
		{"am option quote", `git -c x=y am 'unfinished`, 2},
		{"unicode commit quote", "git\u00a0commit -m 'unfinished", 2},
	}
}

// roundFourSegmentCases covers message operands, executable expansions and malformed shell input.
func roundFourSegmentCases() []struct {
	name, command string
	want          int
} {
	return []struct {
		name, command string
		want          int
	}{
		{"am bundled flag", `git am -cn patch`, 2},
		{"positional parameter", `git commit "$x" && git log -n 1`, 2},
		{"positional prefixed parameter", `git commit "-$suffix" && git log -n 1`, 2},
		{"positional substitution", `git commit "$(printf %s -n)" && git log -n 1`, 2},
		{"positional glob", `git commit * && git log -n 1`, 2},
		{"positional question glob", `git commit file? && git log -n 1`, 2},
		{"positional bracket glob", `git commit [ab] && git log -n 1`, 2},
		{"positional heredoc substitution", "git commit $(cat <<EOF\n-n\nEOF\n) && git log -n 1", 2},
		{"message heredoc flag text", "git commit -m \"$(cat <<EOF\n-n\nEOF\n)\" && git log -n 1", 0},
		{"stdin message flag text", "git commit -F - <<EOF\n-n\nEOF\ngit log -n 1", 0},
		{"stdin message parameter text", "git commit -F - <<EOF\ngit commit -n $name\nEOF\ngit log -n 1", 0},
		{"stdin message executable", "git commit -F - <<EOF\n$(git commit -n)\nEOF", 2},
		{"stdin message invalid execution", "git commit -F - <<EOF\n$(cat <<MISSING)\nEOF", 2},
		{"quoted stdin message expansion", "git commit -F - <<'EOF'\n$(printf %s -n)\nEOF\ngit log -n 1", 0},
		{"message short flag text", `git commit -m 'mention -n here'`, 0},
		{"message option operand", `git commit -m -n`, 0},
		{"message substitution flag text", `git commit -m "$(printf %s -n)" && git log -n 1`, 0},
		{"message positional flag", `git commit -m "$x" "$flags" && git log -n 1`, 2},
		{"message joined parameter", `git commit --message="$x" && git log -n 1`, 0},
		{"file parameter", `git commit -F "$x" && git log -n 1`, 0},
		{"file joined parameter", `git commit --file="$x" && git log -n 1`, 0},
		{"reuse parameter", `git commit -C "$x" && git log -n 1`, 0},
		{"reedit parameter", `git commit -c "$x" && git log -n 1`, 0},
		{"author parameter", `git commit --author "$x" && git log -n 1`, 0},
		{"date parameter", `git commit --date="$x" && git log -n 1`, 0},
		{"message inline flag text", `git commit -m'mention -n here'`, 0},
		{"message read only span", `git commit -m 'git log -n 1; done'`, 0},
		{"comment apostrophe", `git commit -m x # author's note`, 0},
		{"comment flag text", "git commit -m x # -n author's note\ngit log -n 1", 0},
		{"hash within word", `git commit path#name && git log -n 1`, 0},
		{"substitution comment", "git commit -m \"$(printf x # )\"; \"\\\n)\" -n", 2},
		{"substitution comment fallback", "git commit -m \"$(printf x # )\"; \"\\\n)\" && git log -n 1", 2},
		{"bare unmatched backtick", "git commit `unfinished", 2},
		{"quote inside substitution", `git commit -m "$(printf 'unfinished)"`, 2},
		{"heredoc without delimiter", "git commit -F - <<EOF\nmessage", 2},
		{"literal message nested commit", `git commit -m 'git commit -n'`, 0},
		{"executable nested commit", `git commit -m "$(git commit -n)"`, 2},
		{"executable nested message", `git commit -m "$(git commit -m 'mention -n here')"`, 0},
	}
}

// roundFiveSegmentCases covers process bodies, splitting operands and escaped comment boundaries.
func roundFiveSegmentCases() []struct {
	name, command string
	want          int
} {
	return []struct {
		name, command string
		want          int
	}{
		{"process input flag", `git commit <(printf %s -n)`, 2},
		{"process output flag", `git commit >(printf %s -n)`, 2},
		{"process am flag", `git am <(printf %s -n)`, 2},
		{"process file flag", `git commit -F <(printf %s -n)`, 2},
		{"process nested executable", `git commit -F <(git commit -n)`, 2},
		{"process quoted literal", `git commit -m '<(printf %s -n)'`, 0},
		{"process double quoted literal", `git commit -m ">(printf %s -n)"`, 0},
		{"splitting message execution", `git commit -m $(printf '%s %s' msg -n)`, 2},
		{"single word message execution", `git commit -m "$(printf '%s %s' msg -n)"`, 0},
		{"splitting nested execution", "git commit -m " + strings.Repeat("$(", 16) + "cat msg.txt" + strings.Repeat(")", 16) + " && tail -n 1", 2},
		{"splitting message parameter", `git commit -m $msg && git log -n 1`, 2},
		{"splitting file parameter", `git commit -F $file && git log -n 1`, 2},
		{"splitting reuse parameter", `git commit -C $msg && git log -n 1`, 2},
		{"splitting reedit parameter", `git commit -c $msg && git log -n 1`, 2},
		{"splitting author parameter", `git commit --author=$author && git log -n 1`, 2},
		{"splitting date parameter", `git commit --date $date && git log -n 1`, 2},
		{"splitting message glob", `git commit -m * && git log -n 1`, 2},
		{"splitting joined message", `git commit --message=$(printf '%s %s' msg -n)`, 2},
		{"splitting message backticks", "git commit -m `printf '%s %s' msg -n`", 2},
		{"single word reuse long", `git commit --reuse-message="$msg" && git log -n 1`, 0},
		{"single word reedit long", `git commit --reedit-message "$msg" && git log -n 1`, 0},
		{"substitution suffix hash flag", `git commit -m $(printf %s fix)#note -n`, 2},
		{"escaped space hash flag", `git commit -m fix\ #note -n`, 2},
		{"escaped tab hash flag", "git commit -m fix\\\t#note -n", 2},
		{"escaped operator hash flag", `git commit -m fix\;#note -n`, 2},
		{"unescaped space comment", `git commit -m fix #note -n`, 0},
		{"unescaped operator comment", `git commit -m fix;#note -n`, 0},
		{"even escapes comment", `git commit -m fix\\ #note -n`, 0},
		{"odd escapes hash flag", `git commit -m fix\\\ #note -n`, 2},
		{"nested escaped space hash", `git commit -m "$(printf fix\ #note -n)" -n`, 2},
	}
}

// roundSevenSegmentCases covers argument shifts after splitting expansions and quoted boundaries.
func roundSevenSegmentCases() []struct {
	name, command string
	want          int
} {
	return []struct {
		name, command string
		want          int
	}{
		{"empty substitution shifts message", `git commit -m $(printf '') -m -n`, 2},
		{"empty variable shifts message", `git commit -m $msg -m -n`, 2},
		{"empty backtick shifts message", "git commit -m `printf ''` -m -n", 2},
		{"glob shifts message", `git commit -m * -m -n`, 2},
		{"positional substitution shifts message", `git commit $(printf '') -m -n`, 2},
		{"positional variable shifts message", `git commit $msg -m -n`, 2},
		{"positional backtick shifts message", "git commit `printf ''` -m -n", 2},
		{"positional glob shifts message", `git commit * -m -n`, 2},
		{"attached substitution shifts message", `git commit --message=$(printf '') -m -n`, 2},
		{"file substitution shifts message", `git commit -F $(printf '') -m -n`, 2},
		{"am substitution retains operands", `git am $(printf '') > -n`, 2},
		{"am variable retains operands", `git am $patch > -n`, 2},
		{"am backtick retains operands", "git am `printf ''` > -n", 2},
		{"am glob retains operands", `git am * > -n`, 2},
		{"quoted empty substitution message", `git commit -m "$(printf '')" -m 'mention -n here'`, 0},
		{"quoted empty variable message", `git commit -m "$msg" -m 'mention -n here'`, 0},
		{"quoted empty backtick message", "git commit -m \"`printf ''`\" -m 'mention -n here'", 0},
		{"quoted glob message", `git commit -m "*" -m 'mention -n here'`, 0},
		{"uncertainty resets at separator", `git commit -m $msg; git commit -m 'mention -n here'`, 2},
	}
}

// roundEightSegmentCases covers runtime splitting, quoted arrays and scalar masking boundaries.
func roundEightSegmentCases() []struct {
	name, command string
	want          int
} {
	return []struct {
		name, command string
		want          int
	}{
		{"runtime splitting message parameter", `git commit -m $msg && git log -n 1`, 2},
		{"quoted positional array message", `git commit -m "$@" && git log -n 1`, 2},
		{"quoted braced positional array message", `git commit -m "${@}" && git log -n 1`, 2},
		{"quoted array message", `git commit -m "${arr[@]}" && git log -n 1`, 2},
		{"quoted prefix names message", `git commit -m "${!prefix@}" && git log -n 1`, 2},
		{"quoted array keys message", `git commit -m "${!arr[@]}" && git log -n 1`, 2},
		{"quoted array slice message", `git commit -m "${arr[@]:1}" && git log -n 1`, 2},
		{"quoted array shifts message", `git commit -m "${arr[@]}" -m -n`, 2},
		{"quoted runtime message parameter", `git commit -m "$msg" && git log -n 1`, 0},
		{"quoted positional star message", `git commit -m "$*" && git log -n 1`, 0},
		{"quoted braced positional star message", `git commit -m "${*}" && git log -n 1`, 0},
		{"quoted array star message", `git commit -m "${arr[*]}" && git log -n 1`, 0},
		{"quoted array length message", `git commit -m "${#arr[@]}" && git log -n 1`, 0},
		{"quoted prefix star message", `git commit -m "${!prefix*}" && git log -n 1`, 0},
		{"quoted substitution positional array", `git commit -m "$(printf '%s' "$@")" && git log -n 1`, 0},
		{"quoted substitution array message", `git commit -m "$(printf '%s' "${arr[@]}")" && git log -n 1`, 0},
	}
}

// roundNineSegmentCases covers splitting before a subcommand and command-word masking.
func roundNineSegmentCases() []struct {
	name, command string
	want          int
} {
	return []struct {
		name, command string
		want          int
	}{
		{"pre-subcommand positional array", `set -- . commit -n -m; git -C "$@" commit -m -n`, 2},
		{"pre-subcommand quoted array", `arr=(. commit -n -m); git -C "${arr[@]}" commit -m -n`, 2},
		{"pre-subcommand splitting scalar", `var='. commit -n -m'; git -C $var commit -m -n`, 2},
		{"command word scalar", `$HOME/bin/git commit -m 'mention -n here'`, 0},
		{"indented command word scalar", `  $HOME/bin/git commit -m 'mention -n here'`, 0},
		{"pre-subcommand quoted scalar", `git -C "$dir" commit -m 'mention -n here'`, 0},
		{"pre-subcommand uncertainty resets", `git -C $dir commit -m x; git commit -m 'mention -n here'`, 2},
	}
}

// roundTenSegmentCases removes later flags from the reviewer's runtime-only examples.
func roundTenSegmentCases() []struct {
	name, command string
	want          int
} {
	previous := roundEightSegmentCases()
	tail := " && git log -n 1"
	values := strings.Join([]string{"msg", "-n"}, " ")
	variable := "msg='" + values + "'; "
	positional := "set -- " + values + "; "
	cases := []struct {
		name, command string
		want          int
	}{
		{"runtime-only message parameter", variable + strings.TrimSuffix(previous[0].command, tail), 2},
		{"runtime-only positional array", positional + strings.TrimSuffix(previous[1].command, tail), 2},
		{"runtime-only quoted message parameter", variable + strings.TrimSuffix(previous[8].command, tail), 0},
		{"runtime-only quoted positional scalar", positional + strings.TrimSuffix(previous[9].command, tail), 0},
		{"runtime-only quoted positional message", positional + `msg="$*"; ` + strings.TrimSuffix(previous[8].command, tail), 0},
	}
	for _, tc := range previous {
		tc.name = "standalone " + tc.name
		tc.command = strings.TrimSuffix(tc.command, tail)
		cases = append(cases, tc)
	}
	for _, tc := range roundNineSegmentCases()[:3] {
		tc.name = "standalone " + tc.name
		tc.command = strings.TrimSuffix(tc.command, " -m -n")
		cases = append(cases, tc)
	}
	return append(cases, roundTenSegmentCasesControls()...)
}

// roundTenSegmentCasesControls plants literal arguments and separator-local uncertainty controls.
func roundTenSegmentCasesControls() []struct {
	name, command string
	want          int
} {
	previous := roundSevenSegmentCases()
	reset := strings.Replace(roundNineSegmentCases()[6].command, "commit -m x;", "status;", 1)
	return []struct {
		name, command string
		want          int
	}{
		{"standalone substitution", strings.TrimSuffix(previous[0].command, " -m -n"), 2},
		{"standalone glob", strings.TrimSuffix(previous[3].command, " -m -n"), 2},
		{"standalone am substitution", strings.TrimSuffix(previous[10].command, " > -n"), 2},
		{"literal commit arguments", strings.Join([]string{"git", "commit", "-m", "msg"}, " "), 0},
		{"read-only uncertainty resets", reset, 0},
	}
}

// TestParityEvasionCommitSegments judges every planted row with both implementations.
func TestParityEvasionCommitSegments(t *testing.T) {
	interpreter := testsupport.PythonInterpreter(t)
	builtin := policy(t)
	cases := append(commitSegmentCases(), redirectSegmentCases()...)
	cases = append(append(cases, fallbackSegmentCases()...), roundFourSegmentCases()...)
	cases = append(cases, roundFiveSegmentCases()...)
	cases = append(cases, roundSevenSegmentCases()...)
	cases = append(cases, roundEightSegmentCases()...)
	cases = append(cases, roundNineSegmentCases()...)
	for _, tc := range append(cases, roundTenSegmentCases()...) {
		t.Run(tc.name, func(t *testing.T) {
			payload := commandPayload(t, map[string]any{"tool_input": map[string]string{"command": tc.command}})
			python := runGuardScript(t, interpreter, pythonGuard, payload, nil, guardEnvironment())
			verdict := builtin.Command(tc.command)
			goExit := 0
			if verdict.Outcome != Allow {
				goExit = 2
			}
			pythonWant := 0
			if tc.want != 0 {
				pythonWant = 1
			}
			t.Logf("Go rc=%d Python rc=%d want=%d", goExit, python.exit, tc.want)
			if goExit != tc.want || python.exit != pythonWant {
				t.Errorf("Go rc=%d Python rc=%d want=%d", goExit, python.exit, tc.want)
			}
			checkSegmentRefusal(t, tc.name, tc.want, verdict.Reason, python.stderr)
		})
	}
}

// maskWithoutArgumentUncertainty drops retained splitting at the chosen mutation boundary.
func maskWithoutArgumentUncertainty(t *testing.T, command string, beforeCommand bool) string {
	t.Helper()
	scanner := shellSpans{command: command, judged: []byte(command), wordStart: -1, executionStart: -1}
	for scanner.position < len(command) {
		if !scanner.step() {
			t.Fatal(scanner.err)
		}
		if !beforeCommand || scanner.gitCommand == "" {
			scanner.argumentsUncertain = false
		}
	}
	scanner.split(len(command), len(command))
	return string(scanner.judged)
}

// TestEvasionArgumentUncertaintyMutant proves losing splitting state hides shifted flags.
func TestEvasionArgumentUncertaintyMutant(t *testing.T) {
	for _, tc := range []struct {
		name, command string
		beforeCommand bool
	}{
		{"after subcommand", `git commit -m $(printf '') -m -n`, false},
		{"before subcommand", roundNineSegmentCases()[0].command, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			builtin := policy(t)
			fixed := builtin.Command(tc.command)
			masked := maskWithoutArgumentUncertainty(t, tc.command, tc.beforeCommand)
			matched := builtinRule(BuiltinRules()[1], evasionMessage).matches(masked)
			t.Logf("Go masking mutant flag match=%v; fixed outcome=%v (rc=2)", matched, fixed.Outcome)
			if matched || fixed.Outcome != Deny {
				t.Errorf("Go masking mutant flag match=%v; fixed outcome=%v", matched, fixed.Outcome)
			}
		})
	}
}

// TestParityEvasionArgumentUncertaintyMutant proves canonical masking needs splitting state.
func TestParityEvasionArgumentUncertaintyMutant(t *testing.T) {
	interpreter := testsupport.PythonInterpreter(t)
	source, err := os.ReadFile(pythonGuard)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, replacement, command string
	}{
		{"all argument uncertainty", "False", `git commit -m $(printf '') -m -n`},
		{"pre-subcommand uncertainty", "bool(self.git_command) and self.word_splitting", roundNineSegmentCases()[0].command},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mutant := strings.Replace(string(source),
				"self.arguments_uncertain |= self.command_word_seen and self.word_splitting",
				"self.arguments_uncertain |= "+tc.replacement, 1)
			if mutant == string(source) {
				t.Fatal("argument uncertainty mutation did not change the guard")
			}
			script := filepath.Join(t.TempDir(), "mutant_block_evasion.py")
			if err := os.WriteFile(script, []byte(mutant), 0o600); err != nil {
				t.Fatal(err)
			}
			payload := commandPayload(t, map[string]any{"tool_input": map[string]string{"command": tc.command}})
			fixed := runGuardScript(t, interpreter, pythonGuard, payload, nil, guardEnvironment())
			planted := runGuardScript(t, interpreter, script, payload, nil, guardEnvironment())
			t.Logf("canonical mutant rc=%d; fixed rc=%d", planted.exit, fixed.exit)
			if planted.exit != 0 || !planted.marker || fixed.exit != 1 {
				t.Errorf("canonical mutant rc=%d marker=%v; fixed rc=%d", planted.exit, planted.marker, fixed.exit)
			}
		})
	}
}

// TestParityEvasionUnknownArgumentsMutant proves both exact examples need explicit refusal.
func TestParityEvasionUnknownArgumentsMutant(t *testing.T) {
	interpreter := testsupport.PythonInterpreter(t)
	source, err := os.ReadFile(pythonGuard)
	if err != nil {
		t.Fatal(err)
	}
	mutant := strings.Replace(string(source),
		"if self.git_command and self.arguments_uncertain and not self.stack:", "if False:", 1)
	if mutant == string(source) {
		t.Fatal("unknown argument refusal mutation did not change the guard")
	}
	script := filepath.Join(t.TempDir(), "mutant_block_evasion.py")
	if err := os.WriteFile(script, []byte(mutant), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, tc := range roundTenSegmentCases()[:2] {
		t.Run(tc.name, func(t *testing.T) {
			payload := commandPayload(t, map[string]any{"tool_input": map[string]string{"command": tc.command}})
			fixed := runGuardScript(t, interpreter, pythonGuard, payload, nil, guardEnvironment())
			planted := runGuardScript(t, interpreter, script, payload, nil, guardEnvironment())
			t.Logf("canonical mutant rc=%d; fixed rc=%d", planted.exit, fixed.exit)
			if planted.exit != 0 || !planted.marker || fixed.exit != 1 {
				t.Errorf("canonical mutant rc=%d marker=%v; fixed rc=%d", planted.exit, planted.marker, fixed.exit)
			}
		})
	}
}

// checkSegmentRefusal rejects crashes and unrelated refusals for flagged parity rows.
func checkSegmentRefusal(t *testing.T, name string, code int, reason string, stderr []byte) {
	t.Helper()
	if strings.Contains(string(stderr), "Traceback") {
		t.Errorf("Python traceback: %s", stderr)
	}
	if code == 0 {
		return
	}
	want := segmentRefusal(name)
	if reason != want || strings.TrimSpace(string(stderr)) != want {
		t.Errorf("refusal: Go %q Python %q, want %q", reason, stderr, want)
	}
}

// splittingSegmentCase enumerates rows whose argument lists can split at runtime.
func splittingSegmentCase(name string) bool {
	name = strings.TrimPrefix(name, "standalone ")
	return slices.Contains([]string{
		"nesting at bound", "own parameter",
		"am parameter", "own substitution",
		"positional glob", "positional question glob",
		"positional bracket glob", "positional heredoc substitution",
		"process input flag", "process output flag",
		"process am flag", "process file flag",
		"process nested executable", "splitting message execution",
		"splitting nested execution", "splitting message parameter",
		"splitting file parameter", "splitting reuse parameter",
		"splitting reedit parameter", "splitting author parameter",
		"splitting date parameter", "splitting message glob",
		"splitting joined message", "splitting message backticks",
		"substitution suffix hash flag", "empty substitution shifts message",
		"empty variable shifts message", "empty backtick shifts message",
		"glob shifts message", "positional substitution shifts message",
		"positional variable shifts message", "positional backtick shifts message",
		"positional glob shifts message", "attached substitution shifts message",
		"file substitution shifts message", "am substitution retains operands",
		"am variable retains operands", "am backtick retains operands",
		"am glob retains operands", "uncertainty resets at separator",
		"runtime splitting message parameter", "quoted positional array message",
		"quoted braced positional array message", "quoted array message",
		"quoted prefix names message", "quoted array keys message",
		"quoted array slice message", "quoted array shifts message",
		"pre-subcommand positional array", "pre-subcommand quoted array",
		"pre-subcommand splitting scalar", "pre-subcommand uncertainty resets",
		"runtime-only message parameter", "runtime-only positional array",
		"substitution", "glob",
		"am substitution",
		"substitution flag", "backtick flag", "nested substitution flag",
		"substitution separator flag", "positional substitution", "state function",
	}, name)
}

// segmentRefusal names the exact refusal required by each malformed or flagged row.
func segmentRefusal(name string) string {
	if splittingSegmentCase(name) {
		return InvalidInputRefusal + "argument list unknown at scan time: write arguments literally or quote expansion"
	}
	errors := map[string]string{
		"unbalanced quote": "unbalanced quote", "commit option quote": "unbalanced quote",
		"am option quote": "unbalanced quote", "unicode commit quote": "unbalanced quote",
		"quote inside substitution": "unbalanced quote",
		"unbalanced double quote":   "unbalanced shell stack", "unterminated backtick": "unbalanced shell stack",
		"unterminated substitution": "unbalanced shell stack", "bare unmatched backtick": "unbalanced shell stack",
		"unterminated heredoc": "unterminated here-document", "heredoc without delimiter": "unterminated here-document",
		"stdin message invalid execution": "unterminated here-document",
		"nesting past bound":              "nesting bound exceeded: 16",
		"heredoc pending past bound":      "pending here-document bound exceeded: 16",
		"heredoc lines past bound":        "here-document body-line bound exceeded: 256",
	}
	if suffix, ok := errors[name]; ok {
		return InvalidInputRefusal + "invalid shell command: " + suffix
	}
	rule := BuiltinRules()[1]
	if name == "long flag" || name == "am long flag" {
		rule = BuiltinRules()[0]
	}
	return rule.RefusalPrefix() + rule.Source
}

// redirectSegmentCases holds redirection variants that must never split their own flags.
func redirectSegmentCases() []struct {
	name, command string
	want          int
} {
	return []struct {
		name, command string
		want          int
	}{
		{"combined output", `git commit -m x &>out && tail -n 1`, 0},
		{"combined output flag", `git commit -m x &>out -n`, 2},
		{"clobber output", `git commit -m x >|out && tail -n 1`, 0},
		{"clobber output flag", `git commit -m x >|out -n`, 2},
		{"descriptor expansion", `git commit -m x >&"$fd" && tail -n 1`, 0},
		{"descriptor expansion flag", `git commit -m x >&"$fd" -n`, 2},
		{"multiline message flag", "git commit -m 'one\ntwo && three' -n", 2},
		{"unbalanced double quote", `git commit -m "unfinished`, 2},
		{"unterminated backtick", "git commit -m `cat msg.txt && tail -n 1", 2},
		{"heredoc own flag", "git commit -F - -n <<EOF\nmessage\nEOF\ntail -1", 2},
		{"single quoted substitution", `git commit -m '$(cat msg.txt)' && tail -n 1`, 0},
		{"state set", `set -a; git commit -m x && tail -n 1`, 2},
		{"heredoc queued chain", "git commit -F - <<EOF && tail -n 1\nmessage\nEOF", 0},
		{"heredoc queued flag", "git commit -F - <<EOF -n && tail -1\nmessage\nEOF", 2},
		{"heredoc queued pipeline", "git commit -F - <<EOF | tail -n 1\nmessage\nEOF", 0},
		{"heredoc nested context", "git commit -F - <<OUT -m \"$(cat <<IN\ninside\nIN\n)\" && tail -n 1\noutside\nOUT", 0},
		{"heredoc pending at bound", "git commit -F -" + strings.Repeat(" <<EOF", 16) + "\n" + strings.Repeat("EOF\n", 16) + "tail -n 1", 0},
		{"heredoc pending past bound", "git commit -F -" + strings.Repeat(" <<EOF", 17) + "\n" + strings.Repeat("EOF\n", 17) + "tail -n 1", 2},
		// Parameter expansions within message data preserve segmentation.
		{"substitution braces", `git commit -m "$(echo ${msg})" && tail -n 1`, 0},
	}
}

// delimiterBraceCases covers exact heredoc words, body continuations and unknown brace arguments.
func delimiterBraceCases() []struct {
	name, command string
	want          int
} {
	return append([]struct {
		name, command string
		want          int
	}{
		{"brace continuation", "git commit -m {message,\\\n-n}", 2},
		{"partial delimiter", "cat <<'true'X\ndata\ntrueX\ngit commit -n -m msg\ntrue", 2},
		{"literal delimiter quotes", "cat <<\"'true'\"\ndata\n'true'\ngit commit -n -m msg\ntrue", 2},
		{"body continuation", "cat <<true\ntr\\\nue\ngit commit -n -m msg\ntrue", 2},
		{"skip control", "git commit -n -m msg", 2},
		{"message control", "git commit -m msg", 0},
		{"unsupported delimiter expansion", "cat <<$end\ndata\n$end\ngit commit -m msg", 2},
		{"unterminated delimiter quote", "cat <<'EOF\ngit commit -m msg", 2},
		{"unknown brace word", "git commit -m {msg,other}", 2},
		{"unterminated brace word", "git commit -m {msg,-n", 2},
		{"quoted brace word", "git commit -m '{msg,-n}'", 0},
		{"escaped delimiter", "git commit -F - <<\\EOF\n-n\nEOF", 0},
		{"quoted body continuation", "cat <<'true'\ntr\\\nue\ntrue\ngit commit -m msg", 0},
		{"even body escapes", "cat <<true\ntr\\\\\nue\ntrue\ngit commit -m msg", 0},
		{"continued delimiter data", "git commit -F - <<EOF\ntext\\\nEOF\n-n\nEOF", 0},
		{"continued delimiter tabs", "git commit -F - <<-EOF\n\tE\\\nOF\ngit commit -n\nEOF", 2},
		{"brace subcommand", `git {commit,-n} -m msg`, 2},
		// codespell:ignore-next-line ommit
		{"partial brace subcommand", `git c{ommit,-n} -m msg`, 2},
		{"brace command", `{git,commit,-n,-m,msg}`, 2},
		{"partial brace command", `g{it,} commit -n -m msg`, 2},
		{"quoted brace subcommand", `git '{commit,-n}' -m msg`, 0},
		{"quoted brace message", `git commit -m '{a,b}'`, 0},
		{"non git brace operand", `echo {a,b}`, 0},
		// codespell:ignore-next-line ommit
		{"brace subcommand after option", `git -C . c{ommit,-n} -m msg`, 2},
		{"brace option value", `git -C {.,other} commit -m msg`, 2},
		{"quoted brace option value", `git -C '{.,other}' commit -m msg`, 0},
		{"double quoted brace subcommand", `git "{commit,-n}" -m msg`, 0},
		{"double quoted brace message", `git commit -m "{a,b}"`, 0},
		{"quoted brace command", `"g{it,}" commit -n -m msg`, 0},
		{"escaped brace command", `g\{it,\} commit -n -m msg`, 0},
		{"brace assignment control", `echo '{a,b}'; result=$?; echo done`, 0},
		{"non git subcommand brace operand", `git status {a,b}`, 0},
		{"brace group control", `{ echo ready; }`, 0},
		{"brace group commit control", `{ git commit -m msg; }`, 0},
		{"brace group skip flag", `{ git commit -n -m msg; }`, 2},
		{"bare brace subcommand", `git { -m msg`, 2},
		{"assignment brace data", `VALUE={a,b}`, 0},
		{"brace command after assignment", `VALUE={a,b} {git,commit,-n,-m,msg}`, 2},
		{"brace command inside group", `{ {git,commit,-n,-m,msg}; }`, 2},
		{"brace option hides subcommand", `git -c {x.y=z,commit,-n} -m msg`, 2},
		{"quoted option hides no subcommand", `git -c '{x.y=z,commit,-n}' -m msg`, 0},
		{"non git operand names git", `echo git {commit,-n} -m msg`, 0},
		{"git data operand names git", `git log git {commit,-n} -m msg`, 0},
	}, roundSixDelimiterBraceCases()...)
}

// roundSixDelimiterBraceCases covers literal message braces, expanding braces and inner delimiter quotes.
func roundSixDelimiterBraceCases() []struct {
	name, command string
	want          int
} {
	return []struct {
		name, command string
		want          int
	}{
		{"literal brace pair in commit message", `git commit -m fix{docs}`, 0},
		{"brace comma list in commit message", `git commit -m fix{docs,tests}`, 2},
		{"brace numeric sequence in commit message", `git commit -m fix{1..3}`, 2},
		{"brace letter sequence in commit message", `git commit -m fix{a..c}`, 2},
		{"nested braces with comma in commit message", `git commit -m fix{{a,b}}`, 2},
		{"parameter expansion brace in commit message", `git commit -m fix${docs}`, 2},
		{"unclosed brace in commit message", `git commit -m fix{docs`, 2},
		{"single-quoted inner delimiter quotes", "cat <<'\"EOF\"'\ndata\n\"EOF\"\ngit commit -n -m msg\nEOF", 2},
	}
}

// TestParityDelimiterBraceRegression checks the repository guard against exact Go verdicts.
func TestParityDelimiterBraceRegression(t *testing.T) {
	interpreter := testsupport.PythonInterpreter(t)
	builtin := policy(t)
	for _, tc := range delimiterBraceCases() {
		t.Run(tc.name, func(t *testing.T) {
			payload := commandPayload(t, map[string]any{"tool_input": map[string]string{"command": tc.command}})
			python := runGuardScript(t, interpreter, pythonGuard, payload, nil, guardEnvironment())
			want := 0
			if tc.want != 0 {
				want = 1
			}
			if python.exit != want {
				t.Errorf("guard rc=%d want=%d: %s", python.exit, want, python.stderr)
			}
			if got := builtin.Command(tc.command); (got.Outcome == Allow) != (tc.want == 0) {
				t.Errorf("Go verdict: %+v, want rc=%d", got, tc.want)
			}
		})
	}
}

// heredocFeedCases loads the shared acceptance rows without executing their commands.
func heredocFeedCases(t *testing.T) []struct {
	Name, Command string
	Want          int
} {
	t.Helper()
	data, err := os.ReadFile("testdata/heredoc-consumers.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases []struct {
		Name, Command string
		Want          int
	}
	if err := json.Unmarshal(data, &cases); err != nil {
		t.Fatal(err)
	}
	return cases
}

// TestParityEvasionHeredocFeed checks consumer ownership in both policy implementations.
func TestParityEvasionHeredocFeed(t *testing.T) {
	interpreter := testsupport.PythonInterpreter(t)
	builtin := policy(t)
	for _, tc := range heredocFeedCases(t) {
		t.Run(tc.Name, func(t *testing.T) {
			payload := commandPayload(t, map[string]any{"tool_input": map[string]string{"command": tc.Command}})
			got := runGuardScript(t, interpreter, pythonGuard, payload, nil, guardEnvironment())
			want := tc.Want / 2
			t.Logf("canonical rc=%d want=%d", got.exit, want)
			if got.exit != want {
				t.Errorf("canonical rc=%d want=%d: %s", got.exit, want, got.stderr)
			}
			if verdict := builtin.Command(tc.Command); (verdict.Outcome == Allow) != (tc.Want == 0) {
				t.Errorf("Go verdict: %+v, want rc=%d", verdict, tc.Want)
			}
		})
	}
}

// readPythonGuard reads the canonical Python guard through a rooted handle.
func readPythonGuard(t *testing.T) string {
	t.Helper()
	root, err := os.OpenRoot(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("open repository root: %v", err)
	}
	defer func() {
		if closeErr := root.Close(); closeErr != nil {
			t.Errorf("close repository root: %v", closeErr)
		}
	}()
	data, err := root.ReadFile(filepath.FromSlash(".config/agent/hooks/block_evasion.py"))
	if err != nil {
		t.Fatalf("read canonical guard: %v", err)
	}
	return string(data)
}

// parseCanonicalHeredocConsumers extracts the literal HEREDOC_CONSUMERS map from canonical guard source.
func parseCanonicalHeredocConsumers(t *testing.T, source string) map[string]string {
	t.Helper()
	normalized := strings.ReplaceAll(source, "\r\n", "\n")
	_, text, ok := strings.Cut(normalized, "HEREDOC_CONSUMERS = {\n")
	body, _, closed := strings.Cut(text, "}\n")
	if !ok || !closed {
		t.Fatal("consumer table missing")
	}
	entries := regexp.MustCompile(`(?m)^    "([^"]+)": "([^"]+)",$`)
	got := map[string]string{}
	for _, pair := range entries.FindAllStringSubmatch(body, len(body)) {
		got[pair[1]] = pair[2]
	}
	return got
}

// mutateCanonicalHeredocConsumers replaces the HEREDOC_CONSUMERS block and fails by name when unchanged.
func mutateCanonicalHeredocConsumers(source, name, replacement string) (string, error) {
	normalized := strings.ReplaceAll(source, "\r\n", "\n")
	table := regexp.MustCompile(`(?s)HEREDOC_CONSUMERS = \{.*?\}\n`)
	mutated := table.ReplaceAllLiteralString(normalized, replacement)
	if mutated == normalized {
		return "", fmt.Errorf("heredoc consumer mutation %s did not change the guard", name)
	}
	return mutated, nil
}

// TestParityEvasionHeredocConsumers compares the canonical literal table with Go policy.
func TestParityEvasionHeredocConsumers(t *testing.T) {
	lf := strings.ReplaceAll(readPythonGuard(t), "\r\n", "\n")
	crlf := strings.ReplaceAll(lf, "\n", "\r\n")
	for name, source := range map[string]string{"lf": lf, "crlf": crlf} {
		t.Run(name, func(t *testing.T) {
			if got := parseCanonicalHeredocConsumers(t, source); !maps.Equal(got, HeredocConsumers()) {
				t.Errorf("consumer table differs: %v", got)
			}
		})
	}
	var quoted []string
	for _, option := range admissionWrappers["env"].values {
		quoted = append(quoted, strconv.Quote(option))
	}
	if want := "values = (" + strings.Join(quoted, ", ") + ")"; !strings.Contains(lf, want) {
		t.Errorf("env value-option arities differ: want %s", want)
	}
}

// TestParityEvasionHeredocConsumerMutants proves each canonical ownership barrier matters.
func TestParityEvasionHeredocConsumerMutants(t *testing.T) {
	interpreter := testsupport.PythonInterpreter(t)
	lf := strings.ReplaceAll(readPythonGuard(t), "\r\n", "\n")
	crlf := strings.ReplaceAll(lf, "\n", "\r\n")
	if _, err := mutateCanonicalHeredocConsumers("print(1)\n", "unchanged-table", "HEREDOC_CONSUMERS = {}\n"); err == nil || !strings.Contains(err.Error(), "unchanged-table") {
		t.Fatalf("no-op mutation error = %v, want failure naming unchanged-table", err)
	}
	for style, source := range map[string]string{"lf": lf, "crlf": crlf} {
		for _, tc := range []struct {
			name, replacement, command string
			want                       int
		}{
			{"empty", "HEREDOC_CONSUMERS = {}\n", "bash <<'EOF'\ngit commit -n -m x\nEOF", 1},
			{"data as shells", `HEREDOC_CONSUMERS = {"*": "shell"}` + "\n", "git commit -F - <<'EOF'\ngit commit -n -m x\nEOF", 1},
			{"unknown as data", `HEREDOC_CONSUMERS = {"*": "data"}` + "\n", "python3 - <<'EOF'\ngit commit -n -m x\nEOF", 1},
			{"shell named as data", `HEREDOC_CONSUMERS = {"bash": "data"}` + "\n", "bash <<'EOF'\ngit commit -n -m x\nEOF", 0},
		} {
			t.Run(style+"/"+tc.name, func(t *testing.T) {
				assertCanonicalHeredocConsumerMutant(t, interpreter, source, tc.name, tc.replacement, tc.command, tc.want)
			})
		}
	}
}

// assertCanonicalHeredocConsumerMutant writes one mutated canonical guard and checks its verdict.
func assertCanonicalHeredocConsumerMutant(t *testing.T, interpreter, source, name, replacement, command string, want int) {
	t.Helper()
	mutated, err := mutateCanonicalHeredocConsumers(source, name, replacement)
	if err != nil {
		t.Fatal(err)
	}
	script := filepath.Join(t.TempDir(), "mutant.py")
	if err := os.WriteFile(script, []byte(mutated), 0o600); err != nil {
		t.Fatal(err)
	}
	payload := commandPayload(t, map[string]any{"tool_input": map[string]string{"command": command}})
	got := runGuardScript(t, interpreter, script, payload, nil, guardEnvironment())
	t.Logf("mutant rc=%d want=%d", got.exit, want)
	if got.exit != want {
		t.Errorf("mutant rc=%d want=%d: %s", got.exit, want, got.stderr)
	}
}

// TestParityEvasionShellWords compares canonical unquoting with Go word by word; malformed input refuses.
func TestParityEvasionShellWords(t *testing.T) {
	const corpus = "testdata/shell-words.json"
	data, err := os.ReadFile(corpus)
	if err != nil {
		t.Fatalf("read shell word corpus: %v", err)
	}
	var cases []struct {
		Name, Word string
		Want       *string
	}
	if err := json.Unmarshal(data, &cases); err != nil {
		t.Fatalf("decode shell word corpus: %v", err)
	}
	for _, tc := range cases {
		t.Run(tc.Name, func(t *testing.T) {
			if tc.Want == nil {
				if _, err := commandSegments("git commit -m " + tc.Word); err == nil {
					t.Error("Go accepted malformed shell word")
				}
			} else if got := unquoteShellWord(tc.Word); got != *tc.Want {
				t.Errorf("Go word %q want %q", got, *tc.Want)
			}
		})
	}
	const program = `import json, runpy, sys
hook = runpy.run_path(sys.argv[1])
with open(sys.argv[2], encoding="utf-8") as source:
    cases = json.load(source)
for index in range(len(cases)):
    case = cases[index]
    try:
        got = hook["shell_unquote_word"](case["word"])
    except ValueError:
        got = None
    if got != case["want"]:
        sys.exit(case["name"] + ": got " + repr(got) + ", want " + repr(case["want"]))
`
	result, err := util.RunCommandBytes(testsupport.DeadlineContext(t), "", testsupport.PythonInterpreter(t),
		util.MaxCommandOutputBytes, "-B", "-c", program, pythonGuard, corpus)
	if err != nil {
		t.Fatalf("replay canonical shell words: %v: %s", err, result.Stderr)
	}
}
