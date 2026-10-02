package flavor_test

import (
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/flavor"
	"github.com/cordanaLLM/praetor/internal/strictjson"
)

// The settings cases elsewhere in this package all reach the catalog through
// settingsFor(t, "go-library", ...), which sees two of the twenty-three declared settings:
// go-library's lefthook.yml and its ruleset. The eight .vscode/settings.json entries and
// the other ten flavors' entries were never exercised, so deleting their Validator field --
// which is exactly the defect this batch closes -- left the suite green, and python-ml's
// only setting is one of the eight. These two cases sweep the whole catalog instead.

// settingShape is the parser a settings path implies for the tool that reads it.
type settingShape int

const (
	shapeNone settingShape = iota
	shapeJSON
	shapeYAML
)

func shapeOf(path string) settingShape {
	switch {
	case strings.HasSuffix(path, ".json"):
		return shapeJSON
	case strings.HasSuffix(path, ".yml"), strings.HasSuffix(path, ".yaml"):
		return shapeYAML
	default:
		return shapeNone
	}
}

// minimumDeclaredSettings guards the sweep against passing vacuously: the catalog declares
// 23 settings across 12 flavors, and a sweep that suddenly inspects three of them is
// reporting on a catalog that no longer exists rather than on a conforming one.
const minimumDeclaredSettings = 20

func TestCatalog_Positive_EverySettingWithACheckableShapeCarriesAValidator(t *testing.T) {
	checked := 0
	for _, flv := range flavor.List() {
		for _, s := range flv.RequiredSettings() {
			if shapeOf(s.Path) == shapeNone {
				continue
			}
			checked++
			if s.Validator == nil {
				t.Errorf("%s declares %s with no validator, so the audit counts it on presence alone",
					flv.Name(), s.Path)
			}
		}
	}
	if checked < minimumDeclaredSettings {
		t.Fatalf("the sweep inspected %d settings, fewer than the %d the catalog declares",
			checked, minimumDeclaredSettings)
	}
}

// TestCatalog_Negative_EveryDeclaredValidatorJudgesItsShape calls each declared validator
// rather than only asserting that one is present, so a validator wired to the wrong shape --
// a YAML mapping check on a .json path -- is caught with the missing one.
func TestCatalog_Negative_EveryDeclaredValidatorJudgesItsShape(t *testing.T) {
	// The YAML documents run the pre-commit jobs rust-systems claims for its lefthook.yml, the
	// one YAML setting whose validator reads content beyond the shape (lefthook_setting_test.go).
	accepted := map[settingShape][]string{
		shapeJSON: {"{\"a\": 1}", "{\"a\": {\"b\": [1, 2]}}"},
		shapeYAML: {
			"pre-commit:\n  commands:\n    fmt:\n      run: cargo fmt --all --check\n    lint:\n      run: cargo clippy -- -D warnings\n",
			"pre-commit:\n  jobs:\n    - run: cargo fmt --all --check\n    - name: clippy\n      run: cargo clippy\n",
		},
	}
	// Rejected by both shapes: nothing, a container with no members and a non-mapping
	// document. For JSON also what no dialect accepts: a member name repeated in one object, a
	// block comment nothing closes and a document holding comments alone.
	rejected := map[settingShape][]string{
		shapeJSON: {"", "{}", "[]", "null", "a: 1", "{\"a\": 1, \"a\": 2}", "{\"a\": 1 /* open }", "// {\"a\": 1}\n", "{ // only a comment\n}"},
		shapeYAML: {"", "{}", "just a scalar", "- a\n- b\n", "pre-commit: [unterminated\n"},
	}
	// JSON with Comments is a valid document exactly where the consumer of the path documents
	// it (strictjson.DialectOf): VS Code for .vscode/settings.json. Every other JSON setting
	// stays strict, so a validator wired to the wrong dialect fails here in either direction.
	jsonc := []string{"{ // operator note\n\"a\": 1}", "{/* operator note */\"a\": 1}", "{\"a\": 1,}", "{\"a\": \"// data\", /* note */ \"b\": [1,],}"}
	dialects := map[strictjson.Dialect]int{}

	for _, flv := range flavor.List() {
		for _, s := range flv.RequiredSettings() {
			shape := shapeOf(s.Path)
			if shape == shapeNone || s.Validator == nil {
				continue
			}
			for _, body := range accepted[shape] {
				if !s.Validator([]byte(body)) {
					t.Errorf("%s %s: %q is a valid document for this path and was rejected",
						flv.Name(), s.Path, body)
				}
			}
			for _, body := range rejected[shape] {
				if s.Validator([]byte(body)) {
					t.Errorf("%s %s: %q configures nothing and was accepted",
						flv.Name(), s.Path, body)
				}
			}
			if shape != shapeJSON {
				continue
			}
			dialect := strictjson.DialectOf(s.Path)
			dialects[dialect]++
			for _, body := range jsonc {
				if got, want := s.Validator([]byte(body)), dialect == strictjson.JSONC; got != want {
					t.Errorf("%s %s is read as %v: validator returned %v for %q, want %v",
						flv.Name(), s.Path, dialect, got, body, want)
				}
			}
		}
	}
	if dialects[strictjson.JSONC] != 8 || dialects[strictjson.StrictJSON] == 0 {
		t.Errorf("the sweep judged %d JSONC and %d strict JSON settings, want the 8 .vscode/settings.json entries and at least one strict one",
			dialects[strictjson.JSONC], dialects[strictjson.StrictJSON])
	}
}

// TestCatalog_Negative_JSONTemplatesStayStrict is the template half of the dialect line: a
// template the catalog validates as a JSON object is at a path nothing documents as JSONC, and
// its validator refuses a comment and a trailing comma.
func TestCatalog_Negative_JSONTemplatesStayStrict(t *testing.T) {
	checked := 0
	for _, flv := range flavor.List() {
		for _, tmpl := range flv.RequiredTemplates() {
			// tsconfig.json is validated as code, not as an object (carriesCode accepts any
			// text, an array included): tsc reads it with comments.
			if shapeOf(tmpl.Path) != shapeJSON || tmpl.Validator == nil || tmpl.Validator([]byte(`["a"]`)) {
				continue
			}
			checked++
			if dialect := strictjson.DialectOf(tmpl.Path); dialect != strictjson.StrictJSON {
				t.Errorf("%s %s is read as %v, want strict JSON", flv.Name(), tmpl.Path, dialect)
			}
			for _, body := range []string{"{ // note\n\"a\": 1}", "{/* note */\"a\": 1}", "{\"a\": 1,}", "{\"a\": 1, \"a\": 2}"} {
				if tmpl.Validator([]byte(body)) {
					t.Errorf("%s %s: strict JSON template accepted %q", flv.Name(), tmpl.Path, body)
				}
			}
		}
	}
	if checked < 2 {
		t.Fatalf("the sweep inspected %d JSON object templates, fewer than the 2 the catalog declares (.gosec.json, .paperclip/harness.json)", checked)
	}
}

// TestCatalog_Boundary_ShapeOf pins the classifier the two sweeps depend on: a path with no
// parseable shape is skipped rather than silently demanding a validator.
func TestCatalog_Boundary_ShapeOf(t *testing.T) {
	cases := map[string]settingShape{
		".vscode/settings.json":      shapeJSON,
		".github/rulesets/main.json": shapeJSON,
		"lefthook.yml":               shapeYAML,
		"config.yaml":                shapeYAML,
		".standards.lock":            shapeNone,
		"":                           shapeNone,
		"json":                       shapeNone,
	}
	for path, want := range cases {
		if got := shapeOf(path); got != want {
			t.Errorf("shapeOf(%q) = %v, want %v", path, got, want)
		}
	}
}
