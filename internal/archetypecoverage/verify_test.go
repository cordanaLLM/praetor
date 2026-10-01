// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package archetypecoverage

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fixtureSchema is a schema package with one bound key per state and one plumbing function.
const fixtureSchema = `package schema

type Layer struct {
	Tools []string
	Level int
	Mode  string
	Box   Box
}

type Box struct{ Size int }

func merge(a, b Layer) Layer {
	a.Tools = append(a.Tools, b.Tools...)
	return a
}

func validate(l Layer) bool { return l.Mode == "" }
`

// fixtureUse reads Level and Mode outside the schema package.
const fixtureUse = `package use

import "example.com/fixture/schema"

func Report(l schema.Layer) int { return l.Level }

func Gate(l schema.Layer) bool { return l.Mode == "solo" }
`

// fixtureManifest binds the fixture: tools unconsumed, level reported, mode refused,
// box.size unconsumed.
const fixtureManifest = `version: 1
schema: schema
carriers: [Layer, Box]
containers: [Layer.Box]
plumbing:
  - func: schema.merge
    role: joins two layers
  - func: schema.validate
    role: refuses mode in a catalog layer
fields:
  - key: tools
    go: [Layer.Tools]
    state: unconsumed
    reason: nothing runs them
  - key: level
    go: [Layer.Level]
    state: reported
    consumers:
      - func: use.Report
        kind: reports
        effect: returns the level
  - key: mode
    go: [Layer.Mode]
    state: refused
    refused_by: schema.validate
    reason: repository-only
    consumers:
      - func: use.Gate
        kind: acts
        effect: gates on the mode
  - key: box.size
    go: [Box.Size]
    state: unconsumed
    reason: nothing reads it
`

var fixtureKeys = []string{"box.size", "level", "mode", "tools"}

// writeFixture lays out the fixture module, then the extra files given as path, content pairs.
func writeFixture(t *testing.T, extra ...string) string {
	t.Helper()
	root := t.TempDir()
	files := append([]string{
		"go.mod", "module example.com/fixture\n\ngo 1.22\n",
		"schema/schema.go", fixtureSchema,
		"use/use.go", fixtureUse,
		"testdata/ignored.go", "package broken\n\nthis does not parse\n",
		"use/use_test.go", "package use\n\nvar _ = undefinedSymbol\n",
	}, extra...)
	for i := 0; i+1 < len(files); i += 2 {
		path := filepath.Join(root, filepath.FromSlash(files[i]))
		if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(files[i+1]), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func verifyFixture(t *testing.T, root, manifest string, keys []string) *Report {
	t.Helper()
	parsed, err := Parse([]byte(manifest))
	if err != nil {
		t.Fatal(err)
	}
	report, err := Verify(t.Context(), Options{Root: root, Manifest: parsed, Keys: keys})
	if err != nil {
		t.Fatal(err)
	}
	return report
}

func requireFinding(t *testing.T, report *Report, fragment string) {
	t.Helper()
	for _, finding := range report.Findings {
		if strings.Contains(finding, fragment) {
			return
		}
	}
	t.Fatalf("no finding contains %q; findings: %q", fragment, report.Findings)
}

func TestVerifyPassesAMatchingManifest(t *testing.T) {
	report := verifyFixture(t, writeFixture(t), fixtureManifest, fixtureKeys)
	if !report.Passed() {
		t.Fatalf("findings: %q", report.Findings)
	}
	if report.Keys != 4 || report.Readers != 2 || report.States[StateUnconsumed] != 2 || report.States[StateRefused] != 1 {
		t.Fatalf("report = %+v", report)
	}
}

// An unconsumed key that gains a reader fails until the entry lists it: the direction that
// stops the manifest staying pessimistic after a consumer lands.
func TestVerifyFailsWhenAnUnconsumedKeyGainsAReader(t *testing.T) {
	root := writeFixture(t, "use/run.go", "package use\n\nimport \"example.com/fixture/schema\"\n\nfunc Run(l schema.Layer) []string { return l.Tools }\n")
	report := verifyFixture(t, root, fixtureManifest, fixtureKeys)
	requireFinding(t, report, "key tools (unconsumed): use.Run reads Layer.Tools")
}

// A listed consumer that stops reading its key fails: the direction that stops the manifest
// staying optimistic after a consumer is removed.
func TestVerifyFailsWhenAConsumerStopsReading(t *testing.T) {
	root := writeFixture(t)
	if err := os.WriteFile(filepath.Join(root, "use", "use.go"), []byte(strings.Replace(fixtureUse, "return l.Level", "return 0", 1)), 0o600); err != nil {
		t.Fatal(err)
	}
	report := verifyFixture(t, root, fixtureManifest, fixtureKeys)
	requireFinding(t, report, "key level: consumer use.Report does not read Layer.Level")
}

func TestVerifyFailsOnUnboundAndUnknownKeys(t *testing.T) {
	report := verifyFixture(t, writeFixture(t), fixtureManifest, []string{"level", "mode", "tools", "extra"})
	requireFinding(t, report, "key extra: the schema decodes it and the manifest binds it")
	requireFinding(t, report, "key box.size: the archetype schema decodes no such key")
	twice := fixtureManifest + "  - key: tools\n    go: [Layer.Tools]\n    state: unconsumed\n    reason: again\n"
	requireFinding(t, verifyFixture(t, writeFixture(t), twice, fixtureKeys), "key tools: bound twice")
}

// A field added to a carrier must be bound or declared a container, or a value could reach it
// with no key accounting for it.
func TestVerifyFailsOnAnUnboundCarrierField(t *testing.T) {
	root := writeFixture(t)
	schema := strings.Replace(fixtureSchema, "Mode  string", "Mode  string\n\tName  string", 1)
	if err := os.WriteFile(filepath.Join(root, "schema", "schema.go"), []byte(schema), 0o600); err != nil {
		t.Fatal(err)
	}
	report := verifyFixture(t, root, fixtureManifest, fixtureKeys)
	requireFinding(t, report, "carrier field Layer.Name: no key binds it")
}

func TestVerifyFailsOnStaleNames(t *testing.T) {
	stale := strings.NewReplacer(
		"func: schema.merge", "func: schema.gone",
		"refused_by: schema.validate", "refused_by: schema.merge",
		"containers: [Layer.Box]", "containers: [Layer.Level, Layer.Box]",
		"go: [Box.Size]", "go: [Box.Size, Box.Width]",
		"plumbing:\n", "plumbing:\n  - func: schema.Box\n    role: reads nothing\n",
	).Replace(fixtureManifest)
	report := verifyFixture(t, writeFixture(t), stale, fixtureKeys)
	requireFinding(t, report, "plumbing schema.gone: no such declaration")
	requireFinding(t, report, "key mode: refused_by schema.merge is not a plumbing function that reads Layer.Mode")
	requireFinding(t, report, "container Layer.Level: schema declares no struct-valued field")
	requireFinding(t, report, "key box.size: schema declares no field Box.Width")
	requireFinding(t, report, "key tools (unconsumed): schema.merge reads Layer.Tools")
	requireFinding(t, report, "plumbing schema.Box: reads no bound field")
}

// Boundary: a selector that is only assigned to is a write, not a read, so a function that
// clears a key does not consume it; a test file and a testdata directory are never read.
func TestVerifyIgnoresWritesTestsAndTestdata(t *testing.T) {
	root := writeFixture(t, "use/reset.go", "package use\n\nimport \"example.com/fixture/schema\"\n\nfunc Reset(l *schema.Layer) { l.Tools = nil }\n")
	report := verifyFixture(t, root, fixtureManifest, fixtureKeys)
	if !report.Passed() {
		t.Fatalf("a write or a test file counted as a read: %q", report.Findings)
	}
}

// A package that does not type-check fails the scan rather than reporting its keys unread.
func TestVerifyFailsClosedOnATypeError(t *testing.T) {
	root := writeFixture(t, "use/broken.go", "package use\n\nvar _ int = \"text\"\n")
	parsed, err := Parse([]byte(fixtureManifest))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Verify(t.Context(), Options{Root: root, Manifest: parsed, Keys: fixtureKeys}); err == nil ||
		!strings.Contains(err.Error(), "type-check use") {
		t.Fatalf("type error accepted: %v", err)
	}
	if _, err := Verify(t.Context(), Options{Root: root, Manifest: parsed}); err == nil {
		t.Fatal("verification without schema keys accepted")
	}
	missing := strings.Replace(fixtureManifest, "schema: schema", "schema: absent", 1)
	absent, err := Parse([]byte(missing))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Verify(t.Context(), Options{Root: writeFixture(t), Manifest: absent, Keys: fixtureKeys}); err == nil {
		t.Fatal("a schema package without source accepted")
	}
}
