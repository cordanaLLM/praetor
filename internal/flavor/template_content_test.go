package flavor

import (
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"
	"testing"
)

// legacyStub is the body the scaffolder wrote for every template it had no case for, and
// the audit then scored as present (BUG-028, BUG-029, #410).
func legacyStub(templatePath string) []byte {
	return fmt.Appendf(nil, "# %s configuration for acme/widget\n", path.Base(templatePath))
}

// #336 asked for exactly this gate: a required template with no content behind it fails
// here, so an adopter can never again receive a placeholder where a real file was promised.
// Each built-in template names one content source, carries a validator, and its own
// scaffolded body satisfies that validator while the legacy placeholder does not.
func TestEveryRequiredTemplateStatesItsContent(t *testing.T) {
	checked := 0
	for _, flv := range builtinFlavorList() {
		for _, tmpl := range flv.RequiredTemplates() {
			checked++
			where := flv.Name() + " " + tmpl.Path
			if (tmpl.Source == "") == (tmpl.Producer == "") || tmpl.ContentFunc != nil {
				t.Errorf("%s: want exactly one of Source and Producer, got source %q producer %q", where, tmpl.Source, tmpl.Producer)
			}
			if tmpl.Validator == nil {
				t.Errorf("%s: carries no validator, so a placeholder would score present", where)
				continue
			}
			if tmpl.Validator(legacyStub(tmpl.Path)) || tmpl.Validator(nil) {
				t.Errorf("%s: validator accepts the comment placeholder or an empty file", where)
			}
			if tmpl.Source == "" {
				continue
			}
			body, err := templateContent(tmpl, "widget", "acme")
			if err != nil {
				t.Errorf("%s: %v", where, err)
				continue
			}
			if !tmpl.Validator([]byte(body)) {
				t.Errorf("%s: the scaffolded body fails its own validator:\n%s", where, body)
			}
		}
	}
	if checked == 0 {
		t.Fatal("no template was checked")
	}
}

// Negative: a template with no content source is an error, and nothing is written in its
// place. It used to become the one-line comment the audit then counted.
func TestScaffoldTemplate_Negative_NoSourceWritesNothing(t *testing.T) {
	repo := t.TempDir()
	orphan := TemplateItem{Path: ".github/workflows/orphan.yml", Validator: validWorkflow}
	outcome, err := scaffoldTemplate(t.Context(), repo, orphan, "widget", "acme", false)
	if err == nil || !strings.Contains(err.Error(), "has no content source") {
		t.Fatalf("want a no-content-source error, got outcome %d err %v", outcome, err)
	}
	if _, statErr := os.Stat(filepath.Join(repo, ".github", "workflows", "orphan.yml")); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("a template without content wrote a file anyway: %v", statErr)
	}
	missing := TemplateItem{Path: "x.yml", Source: "go/absent.tmpl"}
	if _, err := templateContent(missing, "widget", "acme"); err == nil {
		t.Fatal("a Source naming no embedded template must fail to render")
	}
}

// Boundary: a producer-owned template is deferred, never written, --force included.
func TestScaffoldTemplate_Boundary_ProducerOwnedIsDeferredUnderForce(t *testing.T) {
	repo := t.TempDir()
	manifest := TemplateItem{Path: ".standards.yaml", Producer: producerAdopt, Validator: validYAMLMapping}
	for _, force := range []bool{false, true} {
		outcome, err := scaffoldTemplate(t.Context(), repo, manifest, "widget", "acme", force)
		if err != nil || outcome != templateDeferred {
			t.Fatalf("force=%v: want deferred, got outcome %d err %v", force, outcome, err)
		}
	}
	if _, err := os.Stat(filepath.Join(repo, ".standards.yaml")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("flavor apply wrote a producer-owned file: %v", err)
	}
}

// validatorCase is one input and the verdict a validator must reach on it.
type validatorCase struct {
	name    string
	content string
	want    bool
}

func runValidatorCases(t *testing.T, validator func([]byte) bool, cases []validatorCase) {
	t.Helper()
	for _, tc := range cases {
		if got := validator([]byte(tc.content)); got != tc.want {
			t.Errorf("%s: got %v, want %v for %q", tc.name, got, tc.want, tc.content)
		}
	}
}

func TestValidWorkflow_3D(t *testing.T) {
	runValidatorCases(t, validWorkflow, []validatorCase{
		{"positive: one job", "on: push\njobs:\n  test:\n    runs-on: ubuntu-26.04\n", true},
		{"negative: no jobs key", "name: ci\non: push\n", false},
		{"negative: prose", "this is not a workflow", false},
		{"boundary: empty jobs mapping", "jobs: {}\n", false},
		{"boundary: jobs as a list", "jobs: [test]\n", false},
	})
}

func TestValidDockerfile_3D(t *testing.T) {
	runValidatorCases(t, validDockerfile, []validatorCase{
		{"positive: stage", "FROM scratch\nCOPY app /\n", true},
		{"positive: lowercase named stage", "from golang:1.27 as builder\n", true},
		{"negative: commented FROM", "# FROM scratch\n", false},
		{"negative: FROM without an image", "FROM\n", false},
		{"boundary: indented instruction", "  FROM scratch\n", true},
		{"boundary: parser directive only", "# syntax=docker/dockerfile:1\n", false},
	})
}

func TestAssignsTOMLKey_3D(t *testing.T) {
	runValidatorCases(t, assignsTOMLKey, []validatorCase{
		{"positive: bare key", "line-length = 100\n", true},
		{"positive: dotted and quoted key", "lint.\"per-file\" = []\n", true},
		{"negative: comment only", "# line-length = 100\n", false},
		{"negative: table header only", "[lint]\n", false},
		{"boundary: key after a table", "[lint]\nselect = [\"E\"]\n", true},
		{"boundary: prose without an equals sign", "configure ruff here\n", false},
	})
}

func TestValidGitleaksConfig_3D(t *testing.T) {
	runValidatorCases(t, validGitleaksConfig, []validatorCase{
		{"positive: extends the defaults", "title = \"x\"\n\n[extend]\nuseDefault = true\n", true},
		{"positive: extends another config", "[extend]\npath = \"base.toml\"\n", true},
		{"positive: declares its own rules", "[[rules]]\nid = \"token\"\nregex = '''tok_[a-z]+'''\n", true},
		{"positive: dotted top-level key", "extend.useDefault = true\n", true},
		{"negative: title only loads no rule", "title = \"x\"\n", false},
		{"negative: defaults switched off", "[extend]\nuseDefault = false\n", false},
		{"negative: useDefault outside [extend]", "useDefault = true\n[allowlist]\n", false},
		{"boundary: spaced header and trailing comment", "[ extend ]\nuseDefault = true # keep\n", true},
		{"boundary: empty extend path", "[extend]\npath = \"\"\n", false},
		{"boundary: rules header with spaces", "[[ rules ]]\nid = \"x\"\n", true},
	})
}

func TestValidXMLDocument_3D(t *testing.T) {
	runValidatorCases(t, validXMLDocument, []validatorCase{
		{"positive: element", "<module name=\"Checker\"><module name=\"TreeWalker\"/></module>\n", true},
		{"negative: unclosed element", "<module>\n", false},
		{"negative: not XML", "checkstyle rules go here\n", false},
		{"boundary: declaration without an element", "<?xml version=\"1.0\"?>\n", false},
		{"boundary: one empty element", "<a/>", true},
	})
}

func TestValidMarkdownDocument_3D(t *testing.T) {
	runValidatorCases(t, validMarkdownDocument, []validatorCase{
		{"positive: heading and body", "# Harness\n\nRun make verify-all.\n", true},
		{"negative: heading only", "# AGENTS.md configuration for acme/widget\n", false},
		{"negative: comment and heading", "<!-- markdownlint-disable -->\n# Title\n", false},
		{"boundary: body without a heading", "Run make verify-all.\n", true},
		{"boundary: blank lines only", "\n\n  \n", false},
	})
}

func TestCarriesCode_3D(t *testing.T) {
	runValidatorCases(t, carriesCode, []validatorCase{
		{"positive: module", "export default {};\n", true},
		{"positive: JSON with comments", "// compiler options\n{\n  \"compilerOptions\": {}\n}\n", true},
		{"negative: line comments only", "# eslint.config.mjs configuration\n// nothing\n", false},
		{"negative: block comment only", "/*\n  nothing here\n*/\n", false},
		{"boundary: code after a closing block comment", "/* header */ export default [];\n", true},
		{"boundary: code after a multi-line block comment", "/*\n header\n*/ export default [];\n", true},
	})
}
