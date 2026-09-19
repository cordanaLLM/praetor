package flavor_test

import (
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/flavor"
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
	accepted := map[settingShape][]string{
		shapeJSON: {"{\"a\": 1}", "{\"a\": {\"b\": [1, 2]}}"},
		shapeYAML: {"a: 1\n", "pre-commit:\n  commands:\n    x:\n      run: true\n"},
	}
	// Rejected by both shapes: nothing, a container with no members, a non-mapping
	// document, and JSON with comments, which neither consumer of these paths parses
	// through this audit.
	rejected := map[settingShape][]string{
		shapeJSON: {"", "{}", "[]", "null", "{ // operator note\n\"a\": 1}", "{\"a\": 1,}", "a: 1"},
		shapeYAML: {"", "{}", "just a scalar", "- a\n- b\n", "pre-commit: [unterminated\n"},
	}

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
		}
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
