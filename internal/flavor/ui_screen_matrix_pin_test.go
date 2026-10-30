// SPDX-FileCopyrightText: 2026 Lusoris <lusoris@pm.me>
// SPDX-License-Identifier: EUPL-1.2

package flavor

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/config"
)

func matrixPinFixture(t *testing.T) screenMatrixPin {
	t.Helper()
	schema, err := os.ReadFile("testdata/a11y-matrix/schema.fixture.json")
	if err != nil {
		t.Fatal(err)
	}
	return screenMatrixPin{Source: "test fixture only", Version: "fixture-1", Unit: screenMatrixUnit,
		SHA256: fmt.Sprintf("%x", sha256.Sum256(schema)), Schema: schema}
}

func TestPinnedScreenMatrixSchema(t *testing.T) {
	valid := matrixPinFixture(t)
	cases := []struct {
		name   string
		change func(*screenMatrixPin)
		valid  bool
	}{
		{"complete", func(*screenMatrixPin) {}, true},
		{"missing source", func(p *screenMatrixPin) { p.Source = "" }, false},
		{"missing version", func(p *screenMatrixPin) { p.Version = "" }, false},
		{"missing schema", func(p *screenMatrixPin) { p.Schema = nil }, false},
		{"missing digest", func(p *screenMatrixPin) { p.SHA256 = "" }, false},
		{"wrong digest", func(p *screenMatrixPin) { p.SHA256 = strings.Repeat("0", 64) }, false},
		{"changed schema", func(p *screenMatrixPin) { p.Schema = []byte(`true`) }, false},
		{"oversized schema", func(p *screenMatrixPin) { p.Schema = make([]byte, maxSettingBytes+1) }, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			pin := valid
			tc.change(&pin)
			if _, err := pinnedScreenMatrixSchema(pin); (err == nil) != tc.valid {
				t.Fatalf("valid=%v, error=%v", tc.valid, err)
			}
		})
	}
}

func TestAuditUIScreenMatrixPinned(t *testing.T) {
	root := t.TempDir()
	matrix, err := os.ReadFile("testdata/a11y-matrix/complete.fixture.json")
	if err != nil {
		t.Fatal(err)
	}
	writeScreenMatrixFiles(t, root, matrix)
	manifest := &config.Manifest{UI: &config.UIPolicy{InputLatencyMs: 100, ScreenMatrix: "matrix.json"}}
	pin := matrixPinFixture(t)
	line, err := auditUIScreenMatrix(root, manifest, pin)
	if err != nil || !strings.Contains(line, "[PASS] A11Y-CHECK1 schema:") {
		t.Fatalf("valid fixture pin: %q, %v", line, err)
	}
	writeScreenMatrixFiles(t, root, []byte(`{}`))
	if line, err := auditUIScreenMatrix(root, manifest, pin); err == nil || strings.Contains(line, "[PASS]") {
		t.Fatalf("invalid matrix must fail with pinned schema: %q, %v", line, err)
	}
	pin.Schema = []byte(`{"type":7}`)
	pin.SHA256 = fmt.Sprintf("%x", sha256.Sum256(pin.Schema))
	if _, err := auditUIScreenMatrix(root, manifest, pin); err == nil {
		t.Fatal("invalid pinned schema must fail closed")
	}
}

func writeScreenMatrixFiles(t *testing.T, root string, data []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(root, "tests"), 0o750); err != nil {
		t.Fatal(err)
	}
	files := map[string][]byte{
		"matrix.json":          data,
		"Makefile":             []byte("ui-gates:\n\tnpx playwright test tests/matrix.spec.js\n"),
		"tests/matrix.spec.js": []byte("import matrix from '../matrix.json';\n"),
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(root, name), content, 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

func TestScreenMatrixSpecMustRun(t *testing.T) {
	root := t.TempDir()
	writeScreenMatrixFiles(t, root, []byte(`{}`))
	if err := checkScreenMatrixSpec(root, "matrix.json"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "tests/matrix.spec.js"), []byte("import m from 'other-matrix.json';"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := checkScreenMatrixSpec(root, "matrix.json"); err == nil {
		t.Fatal("unrelated filename passed")
	}
	writeScreenMatrixFiles(t, root, []byte(`{}`))
	if err := os.WriteFile(filepath.Join(root, "Makefile"), []byte("ui-gates:\n\tnpx playwright test tests/other.spec.ts\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := checkScreenMatrixSpec(root, "matrix.json"); err == nil {
		t.Fatal("spec outside ui-gates run passed")
	}
}

func TestScreenMatrixFileStaysWithinRoot(t *testing.T) {
	root := t.TempDir()
	writeScreenMatrixFiles(t, root, []byte(`{}`))
	if err := os.Remove(filepath.Join(root, "matrix.json")); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "outside.json")
	if err := os.WriteFile(outside, []byte(`{}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "matrix.json")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	manifest := &config.Manifest{UI: &config.UIPolicy{InputLatencyMs: 100, ScreenMatrix: "matrix.json"}}
	if _, err := auditUIScreenMatrix(root, manifest, screenMatrixSchemaPin); err == nil || !strings.Contains(err.Error(), "missing or unreadable") {
		t.Fatalf("matrix symlink escape must fail: %v", err)
	}
}

func TestScreenMatrixReferenceStrings(t *testing.T) {
	for _, tc := range []struct {
		content string
		valid   bool
	}{
		{`import matrix from '../data/matrix.json';`, true},
		{`readFileSync('../../data/matrix.json')`, true},
		{"const file = `matrix.json`;", true},
		{`import matrix from "data/matrix.json";`, true},
		{`readFileSync("matrix.json")`, true},
		{`const file = "other-matrix.json"`, false},
		{`const file = "matrix.json.backup"`, false},
		{`const file = matrix.json`, false},
		{`const file = 'matrix.json"`, false},
	} {
		if got := referencesScreenMatrix(tc.content, "data/matrix.json"); got != tc.valid {
			t.Errorf("reference %q = %v, want %v", tc.content, got, tc.valid)
		}
	}
}
