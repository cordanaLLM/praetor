package adopt

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

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
// lefthook.yml fixture is the rendering with checkpoint jobs, the superset adopters get; the
// manifest is the current rendering of the declarations an earlier adoption wrote
// (renderedPriorManifest); the actionlint configuration is the one adoption creates with the
// default facets.
func emittedHookRenderings(t *testing.T) map[string]string {
	t.Helper()
	return map[string]string{
		lefthookFile:         buildLefthookYAMLFor(true),
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
// decodes to exactly what the current rendering decodes to, so lefthook runs the same
// commands and lefthook_identity.go sees the same jobs.
func TestLefthookRendering_Positive_FoldsWithoutChangingValues(t *testing.T) {
	fixtures := readPriorLefthookFixtures(t)
	for name, checkpoint := range map[string]bool{"unfolded.lefthook.yml": false, "unfolded-checkpoint.lefthook.yml": true} {
		var prior, current map[string]any
		if err := yaml.Unmarshal(fixtures[name], &prior); err != nil {
			t.Fatalf("decode %s: %v", name, err)
		}
		if err := yaml.Unmarshal([]byte(buildLefthookYAMLFor(checkpoint)), &current); err != nil {
			t.Fatalf("decode the current rendering (checkpoint=%v): %v", checkpoint, err)
		}
		if !reflect.DeepEqual(prior, current) {
			t.Errorf("checkpoint=%v: the current rendering changed a value of %s", checkpoint, name)
		}
	}
}

// Boundary: both renderings open with a document start and keep every line within
// yamllint's default limit, checked here without yamllint on PATH.
func TestLefthookRendering_Boundary_LinesWithinYamllintDefaults(t *testing.T) {
	for _, checkpoint := range []bool{false, true} {
		rendered := buildLefthookYAMLFor(checkpoint)
		if !strings.Contains(rendered, "\n---\n") {
			t.Errorf("checkpoint=%v: no document start", checkpoint)
		}
		for i, line := range strings.Split(rendered, "\n") {
			if len(line) > yamlLineLimit {
				t.Errorf("checkpoint=%v line %d: %d columns: %s", checkpoint, i+1, len(line), line)
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
