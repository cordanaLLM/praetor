package adopt

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/forge"
	"github.com/cordanaLLM/praetor/internal/govuln"
	"github.com/cordanaLLM/praetor/internal/hisscatalog"
	"github.com/cordanaLLM/praetor/internal/testsupport"
	"github.com/cordanaLLM/praetor/internal/util"
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
// lefthook.yml fixture is the rendering with every language's jobs, the reuse-lint job and the
// checkpoint jobs, the superset adopters get; the hosted REUSE gate is the one a repository
// declaring REUSE gets (reuseWorkflow) for the default branch main; the manifest is the current rendering of the
// declarations an earlier adoption wrote (renderedPriorManifest); the actionlint configuration
// is the one adoption creates with the default facets.
func emittedHookRenderings(t *testing.T) map[string]string {
	t.Helper()
	return map[string]string{
		lefthookFile:         buildLefthookYAMLFor(lefthookShape{languages: lefthookJobLanguages, reuse: true}, true),
		reuseWorkflowFile:    reuseWorkflow(forge.FallbackDefaultBranch),
		evasionHookFile:      buildBlockEvasionPY(),
		engineLauncherFile:   engineLauncherScript,
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
// pre-push gate, which now passes --admit-unsupported (prePushGateArgs), the pre-push security job,
// which now runs the Go vulnerability gate (govulnGateArgs), and the two checkpoint jobs, which now
// start their interpreter through the launcher (lefthookPythonCommand); the unfolded renderings ran
// the first two without, ran govulncheck ./... and named python3, and nothing else differs.
//
// The unfolded renderings resolved the engine from PATH in every governance job; the launcher now
// does (#906), so pathResolvedCommand rewrites each of those lines to its launcher line first, and
// nothing else about them changes.
func TestLefthookRendering_Positive_FoldsWithoutChangingValues(t *testing.T) {
	fixtures := readPriorLefthookFixtures(t)
	for name, checkpoint := range map[string]bool{"unfolded.lefthook.yml": false, "unfolded-checkpoint.lefthook.yml": true} {
		var prior, current map[string]any
		if err := yaml.Unmarshal([]byte(pathResolvedCommand.ReplaceAllString(string(fixtures[name]), "sh "+engineLauncherFile+" $1")), &prior); err != nil {
			t.Fatalf("decode %s: %v", name, err)
		}
		if err := yaml.Unmarshal([]byte(buildLefthookYAMLFor(lefthookShape{languages: hisscatalog.LanguageGo}, checkpoint)), &current); err != nil {
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
		security := decodedJob(t, prior, "pre-push", "security")
		if security["run"] != goModuleCommand("govulncheck", optionalToolCommand("govulncheck", "./...")) {
			t.Fatalf("%s: the pre-push security job did not run govulncheck ./...: %v", name, security["run"])
		}
		security["run"] = goModuleCommand(govuln.DefaultScanner, optionalToolGuard(govuln.DefaultScanner, lefthookGovernedCommand(govulnGateArgs)))
		for _, event := range checkpointEvents(checkpoint) {
			job := decodedJob(t, prior, "agent-checkpoint-"+event, "checkpoint")
			arguments := "-B " + checkpointScript + " --event " + event + " --json --marker"
			if job["run"] != "python3 "+arguments {
				t.Fatalf("%s: the %s checkpoint job did not name python3: %v", name, event, job["run"])
			}
			job["run"] = lefthookPythonCommand(arguments)
		}
		if !reflect.DeepEqual(prior, current) {
			t.Errorf("checkpoint=%v: the current rendering changed a value of %s", checkpoint, name)
		}
	}
}

// pathResolvedCommand matches the run line every governance job had before the engine launcher: the
// engine under either name on PATH, and a refusal when neither is installed. Group 1 is the
// arguments.
var pathResolvedCommand = regexp.MustCompile(`if praetor_cli=\$\(command -v praetorctl 2>/dev/null \|\| command -v standardsctl 2>/dev/null\); ` +
	`then "\$praetor_cli" (.*?); else echo HISS governance hook cannot run because neither praetorctl ` +
	`nor standardsctl is installed >&2; exit 1; fi`)

// checkpointEvents names the lifecycle events a rendering carries checkpoint jobs for.
func checkpointEvents(checkpoint bool) []string {
	if !checkpoint {
		return nil
	}
	return []string{"tool", "stop"}
}

// lefthookWindowsUnsafe are the characters a generated checkpoint line must not hold. Lefthook's
// Windows executor wraps a run line in one pair of double quotes for sh -c without escaping the
// quotes inside it, so a quoted word loses its quotes there, and an expansion or a glob is then
// split or matched by the shell.
const lefthookWindowsUnsafe = "\"'$`\\;|&<>(){}*?[]~#"

// The generated checkpoint jobs start their interpreter through the launcher adoption writes
// (#339). Positive: the rendered line is the launcher path and the arguments, the launcher is a
// file of the checkpoint bundle, and both checkpoint jobs of every rendering use it. Negative:
// no rendering names an interpreter or the variable that once selected one. Boundary: the line
// holds no quote, expansion or other shell syntax, and a rendering without checkpoint jobs
// names neither the launcher nor an interpreter.
func TestLefthookPythonCommandStartsTheBundledLauncher(t *testing.T) {
	const arguments = "-B hook.py --flag"
	line := lefthookPythonCommand(arguments)
	if want := "sh .config/lefthook/python.sh " + arguments; line != want {
		t.Fatalf("lefthookPythonCommand = %q, want %q", line, want)
	}
	if strings.ContainsAny(line, lefthookWindowsUnsafe) {
		t.Fatalf("the generated line %q holds shell syntax Lefthook's Windows executor does not preserve", line)
	}
	if !strings.ContainsAny(`"$PRAETOR_PYTHON" `+arguments, lefthookWindowsUnsafe) {
		t.Fatal("the inline rule this line replaced passes the same check")
	}
	if !slices.Contains(checkpointBundle, checkpointLauncher) {
		t.Fatalf("the launcher %s is not a file of the checkpoint bundle %v", checkpointLauncher, checkpointBundle)
	}
	if _, err := os.Stat(filepath.Join("..", "..", filepath.FromSlash(checkpointLauncher))); err != nil {
		t.Fatalf("the canonical launcher adoption copies: %v", err)
	}
	for _, languages := range lefthookLanguageSets {
		rendering := buildLefthookYAMLFor(lefthookShape{languages: languages}, true)
		var with map[string]any
		if err := yaml.Unmarshal([]byte(rendering), &with); err != nil {
			t.Fatalf("decode the rendering with checkpoint jobs: %v", err)
		}
		for _, event := range checkpointEvents(true) {
			run := decodedJob(t, with, "agent-checkpoint-"+event, "checkpoint")["run"]
			if run != lefthookPythonCommand("-B "+checkpointScript+" --event "+event+" --json --marker") {
				t.Errorf("languages=%v: the %s checkpoint job runs %v", languages, event, run)
			}
		}
		if named := strings.Count(rendering, "python"); named != strings.Count(rendering, checkpointLauncher) || named != len(checkpointEvents(true)) {
			t.Errorf("languages=%v: python is named %d times, want only in the launcher path of each checkpoint job", languages, named)
		}
		without := buildLefthookYAMLFor(lefthookShape{languages: languages}, false)
		if strings.Contains(without, "python") || strings.Contains(rendering+without, "PRAETOR_PYTHON") {
			t.Errorf("languages=%v: a rendering names an interpreter or the variable that selected one", languages)
		}
	}
}

// The generated line is run, not only compared: through sh -c, as Lefthook runs it, in a
// directory holding the launcher adoption writes. Positive: the hook's status is the line's.
// Negative: with no interpreter on PATH the line fails as a missing dependency instead of
// passing. Boundary: a variable naming a program that exits 0 does not take the line off the
// interpreter, and a CRLF checkout of the launcher still runs the hook.
func TestLefthookPythonCommandRunsTheHookThroughTheLauncher(t *testing.T) {
	shell, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("no sh on PATH; Lefthook itself runs every job through sh, so no generated job runs here")
	}
	python := testsupport.PythonInterpreter(t)
	launcher := mustRead(t, filepath.Join("..", "..", filepath.FromSlash(checkpointLauncher)))
	line := lefthookPythonCommand("-B hook.py 41")
	run := func(launcherText, path string, setting ...string) (int, string) {
		root := t.TempDir()
		mustWrite(t, filepath.Join(root, filepath.FromSlash(checkpointLauncher)), launcherText)
		mustWrite(t, filepath.Join(root, "hook.py"), "import sys\nsys.exit(int(sys.argv[1]))\n")
		cmd := exec.CommandContext(t.Context(), shell, "-c", line)
		cmd.Dir = root
		environ := util.FilterEnvironment(os.Environ(), func(name string) bool { return name == "PATH" })
		cmd.Env = append(append(environ, "PATH="+path), setting...)
		output, err := cmd.CombinedOutput()
		var exit *exec.ExitError
		if err != nil && !errors.As(err, &exit) {
			t.Fatalf("run the generated line: %v", err)
		}
		return cmd.ProcessState.ExitCode(), string(output)
	}
	// The line starts sh by name and the launcher starts the interpreter by name, so PATH holds
	// the directories of both and nothing else.
	interpreters := filepath.Dir(python) + string(os.PathListSeparator) + filepath.Dir(shell)
	if code, output := run(launcher, interpreters); code != 41 {
		t.Errorf("exit %d, want the hook's 41: %s", code, output)
	}
	// A PATH that holds the shell and nothing else: a link to it in a directory of its own.
	// On Windows sh.exe does not start from a link, away from the libraries beside it, so the
	// case runs on the other platforms; test_no_candidate_on_path_is_a_missing_dependency in
	// .config/lefthook/scripts/test_hooks.py runs the launcher without one on every platform.
	shellOnly := t.TempDir()
	if runtime.GOOS == "windows" {
		t.Log("the missing-interpreter case needs a link to sh, which does not start on Windows")
	} else if err := os.Symlink(shell, filepath.Join(shellOnly, "sh")); err != nil {
		t.Errorf("link %s: %v", shell, err)
	} else if code, output := run(launcher, shellOnly); code != 127 || !strings.Contains(output, "missing dependency") {
		t.Errorf("no interpreter on PATH: exit %d, want 127 and a missing dependency: %s", code, output)
	}
	if code, output := run(launcher, interpreters, "PRAETOR_PYTHON=true"); code != 41 {
		t.Errorf("PRAETOR_PYTHON=true: exit %d, want the hook's 41: %s", code, output)
	}
	if code, output := run(crlfText(launcher), interpreters); code != 41 {
		t.Errorf("a CRLF checkout of the launcher: exit %d, want the hook's 41: %s", code, output)
	}
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
			rendered := buildLefthookYAMLFor(lefthookShape{languages: languages}, checkpoint)
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
