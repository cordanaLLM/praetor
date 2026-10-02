package adopt

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/hisscatalog"
	"github.com/cordanaLLM/praetor/internal/testsupport"
	"gopkg.in/yaml.v3"
)

// emittedFixtureRoot holds the two hook files adoption renders from templates, the manifest it
// renders and the actionlint configuration it creates, at the paths adoption writes them.
// scripts/test_emitted_hook_lint.py and scripts/test_emitted_yaml_lint.py lint them with the
// downstream defaults (make hooks-lint); TestEmittedHookFixturesMatchTheRendering keeps them
// equal to the rendering, so the lint covers what adoption writes (BUG-782).
const emittedFixtureRoot = "testdata/emitted"

// updateEmittedFixturesEnv rewrites the fixtures from the rendering instead of comparing.
const updateEmittedFixturesEnv = "PRAETOR_UPDATE_EMITTED_FIXTURES"

// emittedHookRenderings maps each rendered file to the bytes adoption writes. The
// lefthook.yml fixture is the rendering with every language's jobs and the checkpoint jobs, the
// superset adopters get; the manifest is the current rendering of the declarations an earlier
// adoption wrote (renderedPriorManifest); the actionlint configuration is the one adoption
// creates with the default facets.
func emittedHookRenderings(t *testing.T) map[string]string {
	t.Helper()
	return map[string]string{
		lefthookFile:         buildLefthookYAMLFor(lefthookJobLanguages, true),
		evasionHookFile:      buildBlockEvasionPY(),
		manifestFile:         renderedPriorManifest(t),
		actionlintConfigFile: string(renderActionlintConfig(actionlintManagedFixtureLabels(t))),
	}
}

func TestEmittedHookFixturesMatchTheRendering(t *testing.T) {
	for rel, want := range emittedHookRenderings(t) {
		path := filepath.Join(emittedFixtureRoot, filepath.FromSlash(rel))
		if os.Getenv(updateEmittedFixturesEnv) == "1" {
			mustWrite(t, path, want)
			continue
		}
		got, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read fixture %s: %v", path, err)
		}
		if strings.ReplaceAll(string(got), "\r\n", "\n") != want {
			t.Errorf("%s differs from the rendering; regenerate it with %s=1 go test ./internal/adopt -run %s",
				path, updateEmittedFixturesEnv, t.Name())
		}
	}
}

// Positive: folding changes the layout, never a value. Every earlier unfolded rendering
// decodes to exactly what the current Go rendering decodes to, so lefthook runs the same
// commands in a Go repository and lefthook_identity.go sees the same jobs. The values that
// changed since are the pre-commit audit, which now passes --offline (preCommitAuditArgs), the
// pre-push gate, which now passes --admit-unsupported (prePushGateArgs), and the two checkpoint
// jobs, which now read their interpreter from PRAETOR_PYTHON (lefthookPythonCommand); the
// unfolded renderings ran the first two without and named python3, and nothing else differs.
func TestLefthookRendering_Positive_FoldsWithoutChangingValues(t *testing.T) {
	fixtures := readPriorLefthookFixtures(t)
	for name, checkpoint := range map[string]bool{"unfolded.lefthook.yml": false, "unfolded-checkpoint.lefthook.yml": true} {
		var prior, current map[string]any
		if err := yaml.Unmarshal(fixtures[name], &prior); err != nil {
			t.Fatalf("decode %s: %v", name, err)
		}
		if err := yaml.Unmarshal([]byte(buildLefthookYAMLFor(hisscatalog.LanguageGo, checkpoint)), &current); err != nil {
			t.Fatalf("decode the current rendering (checkpoint=%v): %v", checkpoint, err)
		}
		preCommitAudit := decodedJob(t, prior, "pre-commit", "hiss-audit")
		if preCommitAudit["run"] != lefthookGovernedCommand("audit") {
			t.Fatalf("%s: the pre-commit audit was not the online one: %v", name, preCommitAudit["run"])
		}
		preCommitAudit["run"] = lefthookGovernedCommand(preCommitAuditArgs)
		gate := decodedJob(t, prior, "pre-push", "gate")
		if gate["run"] != lefthookGovernedCommand("gate run --path=.") {
			t.Fatalf("%s: the pre-push gate was not the strict one: %v", name, gate["run"])
		}
		gate["run"] = lefthookGovernedCommand(prePushGateArgs)
		for _, event := range checkpointEvents(checkpoint) {
			job := decodedJob(t, prior, "agent-checkpoint-"+event, "checkpoint")
			arguments := "-B " + checkpointScript + " --event " + event + " --json --marker"
			if job["run"] != hookPythonDefault+" "+arguments {
				t.Fatalf("%s: the %s checkpoint job did not name %s: %v", name, event, hookPythonDefault, job["run"])
			}
			job["run"] = lefthookPythonCommand(arguments)
		}
		if !reflect.DeepEqual(prior, current) {
			t.Errorf("checkpoint=%v: the current rendering changed a value of %s", checkpoint, name)
		}
	}
}

// checkpointEvents names the lifecycle events a rendering carries checkpoint jobs for.
func checkpointEvents(checkpoint bool) []string {
	if !checkpoint {
		return nil
	}
	return []string{"tool", "stop"}
}

// The generated checkpoint jobs and the canonical launcher resolve one interpreter by one
// rule (#339). Positive: the rendered line reads the variable and falls back to the default
// the launcher names, and both checkpoint jobs of every rendering use it. Negative: no
// rendering names an interpreter outside that line. Boundary: the line is plain shell with
// no ${...} expansion, and a rendering without checkpoint jobs names no interpreter at all.
func TestLefthookPythonCommandMatchesTheCanonicalLauncher(t *testing.T) {
	launcher, err := os.ReadFile(filepath.Join("..", "..", ".config", "lefthook", "python.sh"))
	if err != nil {
		t.Fatalf("read the canonical launcher: %v", err)
	}
	if rule := `python="${` + hookPythonVariable + `:-` + hookPythonDefault + `}"`; !strings.Contains(string(launcher), rule) {
		t.Fatalf("the canonical launcher does not resolve %s", rule)
	}
	const arguments = "-B hook.py --flag"
	want := `if [ -z "$PRAETOR_PYTHON" ]; then PRAETOR_PYTHON=python3; fi; "$PRAETOR_PYTHON" ` + arguments
	if got := lefthookPythonCommand(arguments); got != want {
		t.Fatalf("lefthookPythonCommand = %q, want %q", got, want)
	}
	if strings.Contains(lefthookPythonCommand(arguments), "${") {
		t.Fatal("the generated line uses a ${...} expansion")
	}
	for _, languages := range lefthookLanguageSets {
		var with map[string]any
		if err := yaml.Unmarshal([]byte(buildLefthookYAMLFor(languages, true)), &with); err != nil {
			t.Fatalf("decode the rendering with checkpoint jobs: %v", err)
		}
		for _, event := range checkpointEvents(true) {
			run := decodedJob(t, with, "agent-checkpoint-"+event, "checkpoint")["run"]
			if run != lefthookPythonCommand("-B "+checkpointScript+" --event "+event+" --json --marker") {
				t.Errorf("languages=%v: the %s checkpoint job runs %v", languages, event, run)
			}
		}
		without := buildLefthookYAMLFor(languages, false)
		if strings.Contains(without, "python") || strings.Contains(without, hookPythonVariable) {
			t.Errorf("languages=%v: a rendering without checkpoint jobs names an interpreter", languages)
		}
		named := strings.Count(buildLefthookYAMLFor(languages, true), hookPythonDefault)
		if named != len(checkpointEvents(true)) {
			t.Errorf("languages=%v: %s is named %d times, want once per checkpoint job", languages, hookPythonDefault, named)
		}
	}
}

// The generated line is run, not only compared. Positive: with the variable set, sh starts the
// interpreter it names. Negative: a name that resolves to nothing fails with the shell's
// command-not-found status, python3 on PATH notwithstanding. Boundary: unset and empty both
// select the default name.
func TestLefthookPythonCommandRunsTheNamedInterpreter(t *testing.T) {
	shell, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("no sh on PATH; Lefthook itself runs every job through sh, so no generated job runs here")
	}
	python := testsupport.PythonInterpreter(t)
	line := lefthookPythonCommand(`-c "import sys; sys.exit(41)"`)
	run := func(setting ...string) int {
		cmd := exec.CommandContext(t.Context(), shell, "-c", line)
		cmd.Env = append(envWithout(os.Environ(), hookPythonVariable), setting...)
		err := cmd.Run()
		var exit *exec.ExitError
		if err != nil && !errors.As(err, &exit) {
			t.Fatalf("run the generated line: %v", err)
		}
		return cmd.ProcessState.ExitCode()
	}
	if code := run(hookPythonVariable + "=" + python); code != 41 {
		t.Errorf("%s=%s: exit %d, want the script's 41", hookPythonVariable, python, code)
	}
	if code := run(hookPythonVariable + "=praetor-no-such-interpreter"); code != 127 {
		t.Errorf("a missing interpreter: exit %d, want 127", code)
	}
	if _, err := exec.LookPath(hookPythonDefault); err != nil {
		t.Logf("%s is not on PATH; the default is covered by the rendered text alone here", hookPythonDefault)
		return
	}
	for _, setting := range [][]string{nil, {hookPythonVariable + "="}} {
		if code := run(setting...); code != 41 {
			t.Errorf("setting %v: exit %d, want %s to run the script", setting, code, hookPythonDefault)
		}
	}
}

// envWithout returns environ without the entries of name, in the same order.
func envWithout(environ []string, name string) []string {
	kept := make([]string, 0, len(environ))
	for _, entry := range environ {
		if !strings.HasPrefix(entry, name+"=") {
			kept = append(kept, entry)
		}
	}
	return kept
}

// decodedJob returns the commands entry job of hook in a decoded rendering.
func decodedJob(t *testing.T, decoded map[string]any, hook, job string) map[string]any {
	t.Helper()
	section, isSection := decoded[hook].(map[string]any)
	commands, isCommands := section["commands"].(map[string]any)
	entry, isEntry := commands[job].(map[string]any)
	if !isSection || !isCommands || !isEntry {
		t.Fatalf("the rendering has no %s job %s", hook, job)
	}
	return entry
}

// Boundary: every rendering, for every language set, opens with a document start and keeps
// every line within yamllint's default limit, checked here without yamllint on PATH.
func TestLefthookRendering_Boundary_LinesWithinYamllintDefaults(t *testing.T) {
	for _, languages := range lefthookLanguageSets {
		for _, checkpoint := range []bool{false, true} {
			rendered := buildLefthookYAMLFor(languages, checkpoint)
			if !strings.Contains(rendered, "\n---\n") {
				t.Errorf("languages=%v checkpoint=%v: no document start", languages, checkpoint)
			}
			for i, line := range strings.Split(rendered, "\n") {
				if len(line) > yamlLineLimit {
					t.Errorf("languages=%v checkpoint=%v line %d: %d columns: %s", languages, checkpoint, i+1, len(line), line)
				}
			}
		}
	}
}

func TestFoldAtSpaces(t *testing.T) {
	lines, ok := foldAtSpaces("aa bb cc dd", 5)
	if !ok || !reflect.DeepEqual(lines, []string{"aa bb", "cc dd"}) {
		t.Errorf("boundary: a line of exactly the width must stay whole: %q %v", lines, ok)
	}
	lines, ok = foldAtSpaces("aa bbb cc", 5)
	if !ok || !reflect.DeepEqual(lines, []string{"aa", "bbb", "cc"}) {
		t.Errorf("boundary: one column over the width must break: %q %v", lines, ok)
	}
	lines, ok = foldAtSpaces("short averyveryverylongword end", 8)
	if !ok || !reflect.DeepEqual(lines, []string{"short", "averyveryverylongword", "end"}) {
		t.Errorf("a word longer than the width keeps a line of its own: %q %v", lines, ok)
	}
	for _, text := range []string{"two  spaces", " leading", "trailing ", "tab\there", "line\nbreak", "cr\rhere"} {
		if _, ok := foldAtSpaces(text, 5); ok {
			t.Errorf("negative: %q cannot be folded without changing its value", text)
		}
	}
}

func TestLefthookRun(t *testing.T) {
	fits := strings.Repeat("x", yamlLineLimit-len(yamlRunKey))
	if got := lefthookRun(fits); got != yamlRunKey+fits+"\n" {
		t.Errorf("boundary: a line of exactly %d columns stays plain: %q", yamlLineLimit, got)
	}
	long := strings.Repeat("word ", 40) + "end"
	for _, command := range []string{fits, fits + "y", long, "echo  unfoldable " + long} {
		var decoded map[string]string
		if err := yaml.Unmarshal([]byte(strings.TrimPrefix(lefthookRun(command), "      ")), &decoded); err != nil {
			t.Fatalf("%q: rendering does not decode: %v", command, err)
		}
		if decoded["run"] != command {
			t.Errorf("round trip changed the command:\nwant %q\ngot  %q", command, decoded["run"])
		}
	}
	if !strings.HasPrefix(lefthookRun(long), yamlRunKey+">-\n") {
		t.Errorf("a long command is not folded: %q", lefthookRun(long))
	}
}

func TestPyTokenLiterals(t *testing.T) {
	source := `[/\\]\x22` + strings.Repeat(`a\\`, 20)
	for width := 5; width <= 12; width++ {
		literals := pyToken{source, pyRaw}.literals(width)
		var joined strings.Builder
		for _, literal := range literals {
			body := strings.TrimSuffix(strings.TrimPrefix(literal, `r"`), `"`)
			if strings.HasSuffix(body, `\`) && !strings.HasSuffix(body, `\\`) {
				t.Errorf("width %d: raw literal %s ends in a lone backslash", width, literal)
			}
			if len(literal) > width && len(body) > 2 {
				t.Errorf("width %d: literal %s is %d bytes", width, literal, len(literal))
			}
			joined.WriteString(body)
		}
		if joined.String() != source {
			t.Errorf("width %d: literals %q do not concatenate to the source", width, literals)
		}
	}
	if got := (pyToken{`say "hi" \ now`, pyString}).whole(); got != `"say \"hi\" \\ now"` {
		t.Errorf("escaped literal: %s", got)
	}
	if got := (pyToken{"", pyString}).literals(10); !reflect.DeepEqual(got, []string{`""`}) {
		t.Errorf("empty value: %q", got)
	}
	if got := (pyToken{"alpha beta gamma", pyString}).literals(14); !reflect.DeepEqual(got, []string{`"alpha beta "`, `"gamma"`}) {
		t.Errorf("an escaped string breaks after its last space: %q", got)
	}
}

func TestPythonAssignmentAndPairLayout(t *testing.T) {
	name := "NAME"
	fits := strings.Repeat("x", blackLineLimit-len(name+` = ""`))
	if got := pythonAssignment(name, pyToken{fits, pyString}); got != name+` = "`+fits+"\"\n" {
		t.Errorf("boundary: an assignment of exactly %d columns stays one line: %q", blackLineLimit, got)
	}
	over := pythonAssignment(name, pyToken{fits + "x", pyString})
	body, found := strings.CutPrefix(over, name+" = (\n    \"")
	body, closed := strings.CutSuffix(body, "\"\n)\n")
	if !found || !closed || strings.Count(body, `" "`) != 1 || strings.ReplaceAll(body, `" "`, "") != fits+"x" {
		t.Errorf("one column over: two literals on one line inside parentheses: %q", over)
	}
	long := strings.Repeat("word ", 30)
	if got := pythonAssignment(name, pyToken{long, pyString}); strings.Count(got, "\n") != 4 {
		t.Errorf("a value past one parenthesised line takes one literal per line: %q", got)
	}
	entry := pyToken{strings.Repeat("y", blackLineLimit-len(`    (r"", NAME),`)), pyRaw}
	if got := pythonPairEntry(entry, pyToken{name, pyName}); strings.Count(got, "\n") != 1 {
		t.Errorf("boundary: a pair of exactly %d columns stays one line: %q", blackLineLimit, got)
	}
	entry.text += "y"
	if got := pythonPairEntry(entry, pyToken{name, pyName}); got != "    (\n        r\""+entry.text+"\",\n        NAME,\n    ),\n" {
		t.Errorf("one column over: one item per line with trailing commas: %q", got)
	}
}
