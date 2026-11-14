package agenthook

import (
	"bytes"
	"context"
	"errors"
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
		// Order engine-block-evasion-commit-span-20261114 round 3: backticks retain fallback.
		{"backtick message", "git commit -m \"`cat msg.txt`\" && tail -n 1", 2},
		{"backtick flag", "git commit \"`printf %s -n`\"", 2},
		{"nested substitution", `git commit -m "$(echo "$(cat msg.txt)")" && tail -n 1`, 0},
		{"nested substitution flag", `git commit "$(echo "$(printf %s -n)")"`, 2},
		// Order engine-block-evasion-commit-span-20261114 round 3: ordinary line breaks retain fallback.
		{"newline", "git commit -m x\ngit log -1 | tail -n 1", 2},
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
		// Order engine-block-evasion-commit-span-20261114 round 3: unquoted substitution retains fallback.
		{"nesting at bound", "git commit -m " + strings.Repeat("$(", 16) + "cat msg.txt" + strings.Repeat(")", 16) + " && tail -n 1", 2},
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
		{"own continuation", "git commit -m x\\\n && tail -n 1", 2},
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

// TestParityEvasionCommitSegments judges every planted row with both implementations.
func TestParityEvasionCommitSegments(t *testing.T) {
	interpreter := testsupport.PythonInterpreter(t)
	builtin := policy(t)
	cases := append(commitSegmentCases(), redirectSegmentCases()...)
	for _, tc := range append(cases, fallbackSegmentCases()...) {
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
		})
	}
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
		// Order engine-block-evasion-commit-span-20261114 round 3: unquoted parameter retains fallback.
		{"substitution braces", `git commit -m "$(echo ${msg})" && tail -n 1`, 2},
	}
}
