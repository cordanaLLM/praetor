package agenthook

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/cordanaLLM/praetor/internal/testsupport"
	"github.com/cordanaLLM/praetor/internal/util"
)

// shellOracleLimit bounds both generated commands and the complete replay.
const shellOracleLimit = 2000

// shellOracleCase carries shell text and an optional explicit false-refusal control.
type shellOracleCase struct {
	ID      string `json:"id"`
	Command string `json:"command"`
	Expect  string `json:"expect"`
}

// oracleRoot opens fixture files through a confined handle and checks its close.
func oracleRoot(t *testing.T, path string) *os.Root {
	t.Helper()
	root, err := os.OpenRoot(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := root.Close(); err != nil {
			t.Error(err)
		}
	})
	return root
}

// oracleJSON reads one corpus through its rooted handle.
func oracleJSON(t *testing.T, root *os.Root, path string, value any) {
	t.Helper()
	data, err := root.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, value); err != nil {
		t.Fatal(err)
	}
}

// oracleFixtures retains all existing command corpora and the review's 24 rows.
func oracleFixtures(t *testing.T) []shellOracleCase {
	t.Helper()
	root := oracleRoot(t, "testdata")
	var cases []shellOracleCase
	oracleJSON(t, root, "shell-oracle.json", &cases)
	var feeds []struct {
		Name, Command string
		Want          int
	}
	oracleJSON(t, root, "heredoc-consumers.json", &feeds)
	for _, row := range feeds {
		expect := ""
		if row.Want == 0 {
			expect = "allow"
		}
		cases = append(cases, shellOracleCase{ID: "feed/" + row.Name, Command: row.Command, Expect: expect})
	}
	var fixtures []fixtureCase
	oracleJSON(t, root, "pre-tool/cases.json", &fixtures)
	for _, row := range fixtures {
		if command := oracleFixtureCommand(row.Payload); command != "" {
			cases = append(cases, shellOracleCase{ID: "pre-tool/" + row.Name, Command: command})
		}
	}
	var words []struct{ Name, Word string }
	oracleJSON(t, root, "shell-words.json", &words)
	for _, row := range words {
		cases = append(cases, shellOracleCase{ID: "word/" + row.Name, Command: "git commit -m " + row.Word})
	}
	return cases
}

// oracleFixtureCommand extracts executable text; malformed hook-input controls have no shell command.
func oracleFixtureCommand(data []byte) string {
	var payload struct {
		Input struct{ Command string } `json:"tool_input"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		return ""
	}
	return payload.Input.Command
}

// oracleReader builds a carrier with a closed vocabulary and a literal body.
func oracleReader(carrier int, payload string) string {
	if carrier == 2 {
		return "cat <<< '" + strings.ReplaceAll(payload, "'", "'\"'\"'") + "'"
	}
	delimiter := "'EOF'"
	if carrier == 1 {
		delimiter = "EOF"
	}
	return "cat <<" + delimiter + "\n" + payload + "\nEOF\n"
}

// oracleRoute connects each carrier to one of seven executable shell routes.
func oracleRoute(reader string, route int, prefix string) string {
	line, body, document := strings.Cut(reader, "\n")
	pipe := prefix + line + " | sh"
	if document {
		pipe += "\n" + body
	}
	switch route {
	case 0:
		return pipe
	case 1:
		return prefix + "bash -c \"$(" + reader + ")\""
	case 2:
		return prefix + "$(" + reader + ")"
	case 3:
		return "(" + prefix + reader + ") | sh"
	case 4:
		if document {
			return "{ :; " + prefix + reader + "} | sh"
		}
		return "{ :; " + prefix + reader + "; } | sh"
	case 5:
		return "bash <<OUTER\n" + pipe + "\nOUTER"
	default:
		return prefix + "bash < <(" + reader + ")"
	}
}

// oracleProduct covers every carrier, route, prefix and spelling before adding gap variants.
func oracleProduct() []shellOracleCase {
	prefixes := []string{"", "env ", "env -i ", "command ", "exec ", "builtin command ", "VALUE=x ", "'env' ", "\\env ", "\"$SHELL\" -c "}
	payloads := []string{"git commit -n -m x", "git commit --no-verify -m x", "git commit --no-''verify -m x"}
	var cases []shellOracleCase
	for carrier := range 3 {
		for route := range 7 {
			for prefix, launcher := range prefixes {
				for spelling, payload := range payloads {
					reader := oracleReader(carrier, payload)
					if spelling == 1 {
						reader = strings.Replace(reader, "cat ", "cat 2> /dev/null ", 1)
					}
					command := oracleRoute(reader, route, launcher)
					cases = append(cases, shellOracleCase{ID: fmt.Sprintf("product/%d/%d/%d/%d", carrier, route, prefix, spelling), Command: command})
				}
			}
		}
	}
	return cases
}

// oracleOuterExpansions crosses two inner depths, three output roles and five expansion spellings.
func oracleOuterExpansions() []shellOracleCase {
	var cases []shellOracleCase
	for depth := 1; depth <= 2; depth++ {
		for role := range 3 {
			for spelling := range 5 {
				body := "cat <<'11'\n"
				if depth == 2 {
					body += "cat <<'22'\n"
				}
				body += oracleOuterPayload(role, spelling)
				if depth == 2 {
					body += "22\n"
				}
				body += "11\n"
				command := "D=11; B=; P=7\nbash <<OUTER\n" + body + "OUTER"
				id := fmt.Sprintf("outer-unquoted-product/%d/%d/%d", depth, role, spelling)
				cases = append(cases, shellOracleCase{ID: id, Command: command})
			}
		}
	}
	return cases
}

// oracleOuterPayload places expansion output at a delimiter, a blank suffix or a payload operand.
func oracleOuterPayload(role, spelling int) string {
	values := []string{"11", "", "7"}
	expansion := oracleExpansion(spelling, values[role])
	switch role {
	case 0:
		return expansion + "\ngit commit -n -m x\n"
	case 1:
		return "11" + expansion + "\ngit commit -n -m x\n"
	default:
		return "11\ngit commit -n -m " + expansion + "\n"
	}
}

// oracleExpansion emits one value; blank arithmetic uses a zero-length parameter slice.
func oracleExpansion(spelling int, value string) string {
	switch spelling {
	case 0:
		return "$(printf '%s' '" + value + "')"
	case 1:
		return "`printf '%s' '" + value + "'`"
	case 2:
		return map[string]string{"11": "${D}", "": "${B}", "7": "${P}"}[value]
	case 3:
		if value == "" {
			return "${B:0:$((0))}"
		}
		return "$((" + value + "))"
	default:
		if value == "" {
			return "\\\n"
		}
		return value[:1] + "\\\n" + value[1:]
	}
}

// oracleGapVariants enumerates bounded continuation placements, including indented forms.
func oracleGapVariants(base []shellOracleCase) []shellOracleCase {
	var cases []shellOracleCase
	for gap := range 16 {
		for _, row := range base {
			line, _, _ := strings.Cut(row.Command, "\n")
			spaces := strings.Count(line, " ")
			if gap >= spaces {
				continue
			}
			for indent := range 2 {
				command := oracleGap(row.Command, gap, indent)
				cases = append(cases, shellOracleCase{ID: fmt.Sprintf("%s/gap-%d/indent-%d", row.ID, gap, indent), Command: command})
			}
		}
	}
	return cases
}

// oracleGap inserts one continuation without changing the carrier's document body.
func oracleGap(command string, gap, indent int) string {
	line, body, newline := strings.Cut(command, "\n")
	position := 0
	for word := 0; word <= gap; word++ {
		position += strings.IndexByte(line[position:], ' ') + 1
	}
	command = line[:position] + "\\\n" + strings.Repeat("  ", indent) + line[position:]
	if newline {
		command += "\n" + body
	}
	return command
}

// oracleContinuations keeps the complete base product and evenly samples the bounded gap product.
func oracleContinuations(base []shellOracleCase, capacity int) []shellOracleCase {
	cases := slices.Clone(base)
	variants := oracleGapVariants(base)
	remaining := min(capacity-len(base), len(variants))
	for index := 0; index < remaining; index++ {
		cases = append(cases, variants[index*len(variants)/remaining])
	}
	return cases
}

// oraclePythonVerdicts asks the actual canonical and emitted entry points for every command.
func oraclePythonVerdicts(t *testing.T, cases []shellOracleCase, script, entry string) []bool {
	t.Helper()
	const program = `import contextlib, io, json, runpy, sys
hook = runpy.run_path(sys.argv[1])
cases = json.load(sys.stdin)
results = []
for index in range(len(cases)):
    try:
        with contextlib.redirect_stderr(io.StringIO()):
            result = hook[sys.argv[2]](cases[index]["command"])
        results.append(result is not False)
    except hook.get("Blocked", ValueError):
        results.append(False)
json.dump(results, sys.stdout)
`
	input, err := json.Marshal(cases)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(testsupport.DeadlineContext(t), 2*time.Minute)
	defer cancel()
	ctx = oracleContext(t, ctx, guardEnvironment(), input)
	result, err := util.RunCommandBytes(ctx, "", testsupport.PythonInterpreter(t), util.MaxCommandOutputBytes,
		"-I", "-B", "-c", program, script, entry)
	if err != nil {
		t.Fatalf("oracle %s: %v: %s", script, err, result.Stderr)
	}
	var verdicts []bool
	if err := json.Unmarshal(result.Stdout, &verdicts); err != nil || len(verdicts) != len(cases) {
		t.Fatalf("oracle verdicts: count %d want %d, error %v: %s", len(verdicts), len(cases), err, result.Stdout)
	}
	return verdicts
}

// oracleContext confines a child's environment and standard input without inherited Git settings.
func oracleContext(t *testing.T, ctx context.Context, environment []string, input []byte) context.Context {
	t.Helper()
	ctx, err := util.WithCommandEnvironment(ctx, environment)
	if err != nil {
		t.Fatal(err)
	}
	ctx, err = util.WithCommandStdin(ctx, input)
	if err != nil {
		t.Fatal(err)
	}
	return ctx
}

// oracleSandboxArgs exposes system tools read-only, replaces real Git and isolates all writes and networking.
func oracleSandboxArgs(root, bash, command string) []string {
	args := []string{"--unshare-all", "--die-with-parent", "--new-session", "--ro-bind", "/usr", "/usr", "--symlink", "usr/bin", "/bin"}
	for _, library := range []string{"/lib", "/lib64"} {
		if _, err := os.Stat(library); err == nil {
			args = append(args, "--ro-bind", library, library)
		}
	}
	return append(args, "--proc", "/proc", "--dev", "/dev", "--tmpfs", "/tmp", "--bind", root, "/work",
		"--ro-bind", filepath.Join(root, "bin", "git"), "/usr/bin/git", "--chdir", "/work", bash,
		"--noprofile", "--norc", "-c", "printf ready > .oracle-ready\n"+command)
}

// TestEvasionOracleDetector distinguishes subcommands, option values, clusters and pathspecs.
func TestEvasionOracleDetector(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want bool
	}{
		{"log count", []string{"log", "--grep", "commit", "-n", "1"}, false},
		{"message then switch", []string{"commit", "-m", "text", "-n"}, true},
		{"message switch value", []string{"commit", "-m", "-n"}, false},
		{"cluster", []string{"commit", "-anm", "x"}, true},
		{"global directory", []string{"-C", "dir", "commit", "-n"}, true},
		{"pathspec", []string{"commit", "--", "-n"}, false},
		{"attached message", []string{"commit", "-m-n"}, false},
		{"cluster value", []string{"commit", "-am", "-n"}, false},
		{"value contains n", []string{"commit", "-mn"}, false},
		{"attached signing key", []string{"commit", "-Sn"}, false},
		{"separate signing switch", []string{"commit", "-S", "-n"}, true},
		{"long value", []string{"commit", "--author", "--no-verify"}, false},
		{"long attached value", []string{"commit", "--message=--no-verify"}, false},
		{"long bypass", []string{"commit", "--no-verify"}, true},
		{"global value commit", []string{"-C", "commit", "log", "-n"}, false},
		{"global attached", []string{"--git-dir=dir", "commit", "-n"}, true},
		{"global config", []string{"-c", "demo.value=commit", "commit", "-n"}, true},
		{"global flags", []string{"--no-pager", "--literal-pathspecs", "commit", "-n"}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := oracleBypassed([][]string{tc.args}); got != tc.want {
				t.Errorf("oracleBypassed(%q)=%v want=%v", tc.args, got, tc.want)
			}
		})
	}
}

// TestEvasionOracleValueOptions plants both bypass spellings as every required option value.
func TestEvasionOracleValueOptions(t *testing.T) {
	options := []string{"-m", "-F", "-C", "-c", "-t", "-U", "--message", "--file", "--author",
		"--date", "--fixup", "--squash", "--reuse-message", "--reedit-message", "--template",
		"--cleanup", "--pathspec-from-file", "--trailer", "--unified", "--inter-hunk-context"}
	for _, option := range options {
		for _, flag := range []string{"-n", "--no-verify"} {
			args := []string{"commit", option, flag}
			if oracleBypassed([][]string{args}) {
				t.Errorf("option value counted as bypass: %q", args)
			}
			args = append(args, flag)
			if !oracleBypassed([][]string{args}) {
				t.Errorf("option after value missed: %q", args)
			}
		}
	}
}

// TestEvasionOracleGlobalOptions plants misleading commit and flag words in global values.
func TestEvasionOracleGlobalOptions(t *testing.T) {
	for _, option := range []string{"-C", "-c", "--git-dir", "--work-tree", "--namespace"} {
		args := []string{option, "commit", "log", "-n", "1"}
		if oracleBypassed([][]string{args}) {
			t.Errorf("global value counted as command: %q", args)
		}
		args = []string{option, "dir", "commit", "-n"}
		if !oracleBypassed([][]string{args}) {
			t.Errorf("commit after global value missed: %q", args)
		}
	}
}

// oracleBypassed reads recorded argv with Git 2.56.1's documented option arities.
func oracleBypassed(calls [][]string) bool {
	for _, call := range calls {
		args := oracleCommitArgs(call)
		if oracleCommitBypass(args) {
			return true
		}
	}
	return false
}

// oracleCommitArgs skips git(1) global options; only the first command can be commit.
func oracleCommitArgs(call []string) []string {
	for index := 0; index < len(call); index++ {
		word := call[index]
		if !strings.HasPrefix(word, "-") {
			if word == "commit" {
				return call[index+1:]
			}
			return nil
		}
		width := oracleGlobalWidth(word)
		if width == 0 {
			return nil
		}
		index += width - 1
	}
	return nil
}

// oracleGlobalWidth follows git(1), Git 2.56.1; zero marks unknown or immediate-exit options.
func oracleGlobalWidth(word string) int {
	values := []string{"-C", "-c", "--git-dir", "--work-tree", "--namespace"}
	if slices.Contains(values, word) {
		return 2
	}
	option, _, attached := strings.Cut(word, "=")
	attachedValues := append(values[2:], "--config-env", "--exec-path", "--attr-source")
	if attached && slices.Contains(attachedValues, option) {
		return 1
	}
	if len(word) > 2 && (strings.HasPrefix(word, "-C") || strings.HasPrefix(word, "-c")) {
		return 1
	}
	flags := []string{"-p", "--paginate", "-P", "--no-pager", "--bare", "--no-replace-objects",
		"--no-lazy-fetch", "--no-optional-locks", "--no-advice", "--literal-pathspecs",
		"--glob-pathspecs", "--noglob-pathspecs", "--icase-pathspecs"}
	if slices.Contains(flags, word) {
		return 1
	}
	return 0
}

// oracleCommitBypass excludes git-commit(1) values and everything after the pathspec boundary.
func oracleCommitBypass(args []string) bool {
	for index := 0; index < len(args) && args[index] != "--"; index++ {
		word := args[index]
		if word == "--no-verify" {
			return true
		}
		if strings.HasPrefix(word, "--") {
			values := []string{"--message", "--file", "--author", "--date", "--fixup", "--squash",
				"--reuse-message", "--reedit-message", "--template", "--cleanup", "--pathspec-from-file",
				"--trailer", "--unified", "--inter-hunk-context"}
			if slices.Contains(values, word) {
				index++
			}
			continue
		}
		bypass, value := oracleShortOptions(word)
		if bypass {
			return true
		}
		if value {
			index++
		}
	}
	return false
}

// oracleShortOptions stops a cluster at required or attached optional git-commit(1) values.
func oracleShortOptions(word string) (bypass, value bool) {
	if !strings.HasPrefix(word, "-") {
		return false, false
	}
	for index := 1; index < len(word); index++ {
		option := word[index]
		if option == 'n' {
			return true, false
		}
		if strings.ContainsRune("mFCctU", rune(option)) {
			return false, index+1 == len(word)
		}
		if strings.ContainsRune("Su", rune(option)) {
			return false, false
		}
	}
	return false, false
}

// oracleRecorder creates a private executable that records exact Git argument lists.
func oracleRecorder(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	// nosemgrep: go.lang.correctness.permissions.file_permission.incorrect-default-permission -- executable fixture directory, private temporary parent.
	if err := os.Mkdir(filepath.Join(root, "bin"), 0o700); err != nil {
		t.Fatal(err)
	}
	const stub = "#!/usr/bin/python3 -I\nimport json, sys\nwith open('/work/calls.jsonl', 'a') as log:\n    log.write(json.dumps(sys.argv[1:]) + '\\n')\n"
	// nosemgrep: go.lang.correctness.permissions.file_permission.incorrect-default-permission, go_file-permissions_rule-fileperm -- executable Git recorder, confined namespace and private temporary parent.
	if err := os.WriteFile(filepath.Join(root, "bin", "git"), []byte(stub), 0o700); err != nil {
		t.Fatal(err)
	}
	return root
}

// oracleRun shares the probe and replay's exact namespace, recorder, environment and timeout.
func oracleRun(t *testing.T, sandbox, bash, command string) (string, util.CommandBytes, error) {
	t.Helper()
	root := oracleRecorder(t)
	ctx, cancel := context.WithTimeout(testsupport.DeadlineContext(t), 10*time.Second)
	defer cancel()
	ctx = oracleContext(t, ctx, []string{"PATH=/work/bin:/usr/bin", "HOME=/work", "SHELL=/usr/bin/sh", "TARGET=/work/t"}, nil)
	result, runErr := util.RunCommandBytes(ctx, "", sandbox, util.MaxCommandOutputBytes, oracleSandboxArgs(root, bash, command)...)
	if ctx.Err() != nil {
		t.Fatalf("shell oracle timeout: %v: %s", ctx.Err(), result.Stderr)
	}
	var exit *exec.ExitError
	if runErr != nil && !errors.As(runErr, &exit) {
		t.Fatalf("shell oracle execution failed: %v: %s", runErr, result.Stderr)
	}
	return root, result, runErr
}

// oracleShell runs one corpus command; namespace startup failures remain fatal after the probe.
func oracleShell(t *testing.T, sandbox, bash, command string) bool {
	t.Helper()
	root, result, runErr := oracleRun(t, sandbox, bash, command)
	return oracleCalls(t, root, runErr, result.Stderr)
}

// oracleProbeDecision allows only bubblewrap's namespace diagnostics with exit status 1 to skip.
func oracleProbeDecision(exitCode int, stderr []byte) string {
	if exitCode == 0 {
		return "replay"
	}
	if testsupport.NamespaceProbeUnavailable(exitCode, stderr) {
		return "skip"
	}
	return "fail"
}

// oracleProbe mirrors internal/sandbox/network_integration_test.go's realNetworkBinaries prerequisite check.
// Its private helper uses a different sandbox; this probe must use the oracle's own invocation.
func oracleProbe(t *testing.T, sandbox, bash string) {
	t.Helper()
	root, result, runErr := oracleRun(t, sandbox, bash, "true")
	exitCode := 0
	var exit *exec.ExitError
	if errors.As(runErr, &exit) {
		exitCode = exit.ExitCode()
	}
	switch oracleProbeDecision(exitCode, result.Stderr) {
	case "skip":
		t.Skipf("bubblewrap user namespaces unavailable: %s; the shell execution oracle did not replay the corpus on this host", strings.TrimSpace(string(result.Stderr)))
	case "fail":
		t.Fatalf("shell oracle sandbox probe failed: %v: %s", runErr, result.Stderr)
	}
	oracleCalls(t, root, runErr, result.Stderr)
}

// TestEvasionOracleProbeDecision plants namespace denials, unrelated failures and successful replay.
func TestEvasionOracleProbeDecision(t *testing.T) {
	const denied = "bwrap: No permissions to create a new namespace, likely because the kernel does not allow non-privileged user namespaces"
	cases := []struct {
		name     string
		exitCode int
		stderr   string
		want     string
	}{
		{"namespace denied", 1, denied, "skip"},
		{"namespace denied with advice", 1, denied + ". On e.g. debian this can be enabled with 'sysctl kernel.unprivileged_userns_clone=1'.\n", "skip"},
		{"namespace not permitted", 1, "bwrap: Creating new namespace failed: Operation not permitted", "skip"},
		{"namespace permission denied", 1, "bwrap: Creating new namespace failed: Permission denied", "skip"},
		{"namespace unsupported", 1, "bwrap: Creating new namespace failed, likely because the kernel does not support user namespaces.", "skip"},
		{"bash missing", 1, "bwrap: execvp bash: No such file or directory", "fail"},
		{"empty failure", 1, "", "fail"},
		{"unrelated permission", 1, "bwrap: Can't bind mount /work: Permission denied", "fail"},
		{"namespace different exit", 2, denied, "fail"},
		{"signal termination", -1, denied, "fail"},
		{"success", 0, "", "replay"},
		{"success with diagnostic", 0, denied, "replay"},
	}
	for index := 0; index < len(cases); index++ {
		tc := cases[index]
		t.Run(tc.name, func(t *testing.T) {
			if got := oracleProbeDecision(tc.exitCode, []byte(tc.stderr)); got != tc.want {
				t.Errorf("oracleProbeDecision(%d, %q)=%q want=%q", tc.exitCode, tc.stderr, got, tc.want)
			}
		})
	}
}

// oracleCalls checks namespace startup and decodes each recording through a rooted handle.
func oracleCalls(t *testing.T, root string, runErr error, stderr []byte) bool {
	t.Helper()
	files, err := os.OpenRoot(root)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := files.Close(); err != nil {
			t.Error(err)
		}
	}()
	if _, err := files.Stat(".oracle-ready"); err != nil {
		t.Fatalf("shell namespace did not start: %v: %s", runErr, stderr)
	}
	data, err := files.ReadFile("calls.jsonl")
	if errors.Is(err, os.ErrNotExist) {
		return false
	}
	if err != nil {
		t.Fatal(err)
	}
	var calls [][]string
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		var call []string
		if err := json.Unmarshal([]byte(line), &call); err != nil {
			t.Fatal(err)
		}
		calls = append(calls, call)
	}
	return oracleBypassed(calls)
}

// oracleCheck checks policy safety and keeps the recorder's execution controls live.
func oracleCheck(t *testing.T, row shellOracleCase, allowed []bool, ran bool) bool {
	t.Helper()
	if allowed[0] != allowed[1] || allowed[0] != allowed[2] {
		t.Errorf("verdict parity %s: Go/canonical/emitted=%v", row.ID, allowed)
	}
	controls := []string{"control-plain", "control-shell-heredoc", "control-pipe-shell", "unmodeled-reader-function", "unmodeled-spaced-reader-function"}
	controls = append(controls, "outer-unquoted-execution", "outer-unquoted-expanded-delimiter", "outer-unquoted-escaped-pair", "outer-unquoted-escaped-option")
	if (slices.Contains(controls, row.ID) || strings.HasPrefix(row.ID, "outer-unquoted-product/")) && !ran {
		t.Errorf("shell execution control %s did not record its bypass", row.ID)
	}
	if row.Expect == "allow" && slices.Contains(allowed, false) {
		t.Errorf("false refusal %s: Go/canonical/emitted=%v; command=%q", row.ID, allowed, row.Command)
	}
	if ran && slices.Contains(allowed, true) {
		t.Errorf("bypass %s: Go/canonical/emitted=%v; command=%q", row.ID, allowed, row.Command)
		return true
	}
	return false
}

// TestEvasionShellOracle compares three policy forms with actual shell execution, independent of parity.
func TestEvasionShellOracle(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("Linux namespace sandbox unavailable: shell execution oracle cannot safely replay destructive corpus on this platform")
	}
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Fatal("bash missing: shell execution oracle requires Bash")
	}
	sandbox, err := exec.LookPath("bwrap")
	if err != nil {
		t.Fatal("bwrap missing: shell execution oracle requires filesystem and network isolation")
	}
	oracleProbe(t, sandbox, bash)
	cases := oracleFixtures(t)
	fixtures := len(cases)
	expansions := oracleOuterExpansions()
	cases = append(cases, expansions...)
	cases = append(cases, oracleContinuations(oracleProduct(), shellOracleLimit-fixtures-len(expansions))...)
	if len(cases) > shellOracleLimit {
		t.Fatalf("oracle corpus bound: %d > %d", len(cases), shellOracleLimit)
	}
	canonical := oraclePythonVerdicts(t, cases, pythonGuard, "audit_command")
	emitted := oraclePythonVerdicts(t, cases, "../adopt/testdata/evasion/current-rendering.block_evasion.py", "check_command")
	builtin := policy(t)
	failures := 0
	for index := 0; index < len(cases) && index < shellOracleLimit; index++ {
		row := cases[index]
		allowed := []bool{builtin.Command(row.Command).Outcome == Allow, canonical[index], emitted[index]}
		ran := oracleShell(t, sandbox, bash, row.Command)
		if oracleCheck(t, row, allowed, ran) {
			failures++
		}
	}
	t.Logf("oracle corpus=%d fixtures=%d product=%d shell-executed bypasses allowed=%d", len(cases), fixtures, len(cases)-fixtures, failures)
}
