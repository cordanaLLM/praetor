// SPDX-FileCopyrightText: 2026 Lusoris <lusoris@pm.me>
// SPDX-License-Identifier: EUPL-1.2

package flavor_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/config"
	"github.com/cordanaLLM/praetor/internal/flavor"
)

func writeMatrixFixture(t *testing.T, root, data string, reference bool) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(root, "screen-matrix.json"), []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
	if reference {
		filename := filepath.Join(root, flavor.UIA11ySpecPath)
		spec, err := os.ReadFile(filename)
		if err != nil {
			t.Fatal(err)
		}
		spec = append(spec, []byte("\nimport matrix from '../screen-matrix.json';\n")...)
		if err := os.WriteFile(filename, spec, 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

func TestAuditUIGatesScreenMatrix(t *testing.T) {
	for _, tc := range []struct {
		name, data, want string
		reference        bool
	}{
		{"missing file", "", "missing or unreadable", false},
		{"malformed JSON", "{", "not valid JSON", true},
		{"no reference", "{}", "must reference", false},
		{"complete unpinned", "{}", "", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := writeBaseSvelteDir(t, `{"name":"ui","devDependencies":{"@axe-core/playwright":"4.11.1"}}`)
			renderUISpecFixture(t, dir)
			if tc.data != "" {
				writeMatrixFixture(t, dir, tc.data, tc.reference)
			}
			manifest := &config.Manifest{UI: &config.UIPolicy{InputLatencyMs: 100, ScreenMatrix: "screen-matrix.json"}}
			line, err := flavor.AuditUIGates(dir, manifest)
			if !strings.Contains(line, "schema validation pending A11Y-CHECK1 pin (sveltesentio-a11y-matrix-check-20261028)") {
				t.Fatalf("missing pending line: %q", line)
			}
			if tc.want == "" && err != nil {
				t.Fatalf("unpinned key, file, JSON and spec: %q, %v", line, err)
			}
			if tc.want != "" && (err == nil || !strings.Contains(err.Error(), tc.want)) {
				t.Fatalf("want %q; got %v", tc.want, err)
			}
		})
	}
}

func matrixFixture(t *testing.T, name string) []byte {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", "a11y-matrix", name+".fixture.json"))
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestValidateUIScreenMatrixFloor(t *testing.T) {
	cases := []struct {
		name        string
		removeClass string
		dpr         int
		field       string
	}{
		{name: "complete"},
		{name: "missing 4K DPR 2", removeClass: "4k-3840", dpr: 2},
		{name: "missing ultrawide 5120", removeClass: "ultrawide-5120", dpr: 1},
		{name: "missing zoom 400", field: "zoom"},
		{name: "missing source fingerprint", field: "class_source"},
		{name: "missing source date", field: "date"},
		{name: "missing reflow", field: "reflow"},
		{name: "missing text spacing", field: "text_spacing"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			data := plantMatrixFault(t, tc.removeClass, tc.dpr, tc.field)
			err := flavor.ValidateUIScreenMatrix(data, matrixFixture(t, "schema"))
			if (err == nil) != (tc.name == "complete") {
				t.Fatalf("fixture validation: %v", err)
			}
		})
	}
}

func plantMatrixFault(t *testing.T, class string, dpr int, field string) []byte {
	t.Helper()
	var data map[string]any
	if err := json.Unmarshal(matrixFixture(t, "complete"), &data); err != nil {
		t.Fatal(err)
	}
	classes, ok := data["classes"].([]any)
	if !ok {
		t.Fatal("fixture classes must be an array")
	}
	for i := 0; i < len(classes) && i < 128; i++ {
		row, ok := classes[i].(map[string]any)
		if !ok {
			t.Fatal("fixture class must be an object")
		}
		if row["name"] == class && row["dpr"] == float64(dpr) {
			classes[i] = classes[0] // Keep the count: the required class, not minItems, must reject this.
			break
		}
	}
	plantMatrixField(t, data, field)
	raw, err := json.Marshal(data)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func plantMatrixField(t *testing.T, data map[string]any, field string) {
	t.Helper()
	if field == "class_source" {
		delete(data, field)
		return
	}
	if field == "date" {
		source, ok := data["class_source"].(map[string]any)
		if !ok {
			t.Fatal("fixture class_source must be an object")
		}
		delete(source, field)
		return
	}
	checks, ok := data["checks"].(map[string]any)
	if !ok {
		t.Fatal("fixture checks must be an object")
	}
	if field == "zoom" {
		checks[field] = []int{200}
		return
	}
	delete(checks, field)
}

func TestValidateUIScreenMatrixErrorsAndBounds(t *testing.T) {
	cases := []struct {
		name         string
		data, schema []byte
		valid        bool
	}{
		{"nil schema pending", []byte(`{}`), nil, true},
		{"bad data", []byte(`{`), nil, false},
		{"trailing data", []byte(`{} {}`), nil, false},
		{"empty schema pinned", []byte(`{}`), []byte{}, false},
		{"invalid schema JSON", []byte(`{}`), []byte(`{`), false},
		{"invalid schema shape", []byte(`{}`), []byte(`{"type":7}`), false},
		{"external reference", []byte(`{}`), []byte(`{"$ref":"file:///etc/passwd"}`), false},
		{"false schema", []byte(`{}`), []byte(`false`), false},
		{"true schema", []byte(`{}`), []byte(`true`), true},
		{"exact data limit", []byte(`{}` + strings.Repeat(" ", (1<<20)-2)), nil, true},
		{"data too large", []byte(strings.Repeat(" ", (1<<20)+1)), nil, false},
		{"schema too large", []byte(`{}`), []byte(strings.Repeat(" ", (1<<20)+1)), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := flavor.ValidateUIScreenMatrix(tc.data, tc.schema); (err == nil) != tc.valid {
				t.Fatalf("valid=%v, error=%v", tc.valid, err)
			}
		})
	}
}
