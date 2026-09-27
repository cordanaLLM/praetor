// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package main

import (
	"strings"
	"testing"
	"testing/fstest"

	"github.com/cordanaLLM/praetor/internal/managedasset"
)

// auditFixtureFamily is a second documentation family with nested assets and its own gate,
// audited through the same functions as the Markdown family.
func auditFixtureFamily() managedasset.Family {
	return managedasset.Family{
		Name: "fixture engine", Kind: "documentation", AssetNoun: "fixture engine asset",
		WorkflowNoun: "fixture engine workflow", Facet: managedasset.DocumentationFacet,
		Directory: "tools/fixture", Source: "tools/fixture/assets.go",
		FS: fstest.MapFS{
			"core.mjs":                {Data: []byte("export const core = 1;\n")},
			"third_party/lib/LICENSE": {Data: []byte("MIT\n")},
		},
		Assets: []string{"core.mjs", "third_party/lib/LICENSE"}, MaxAssets: 2,
		WorkflowFile: ".github/workflows/fixture.yml", StatusContext: "Fixture Gate",
		Workflow: "name: Fixture Gate\n",
	}
}

func writeFamilyFixture(t *testing.T, root string, family managedasset.Family, edit func(string) string) {
	t.Helper()
	for _, rel := range family.ManagedPaths() {
		data, _, err := family.Canonical(rel)
		if err != nil {
			t.Fatal(err)
		}
		writeFixtureFile(t, root, rel, edit(string(data)))
	}
}

// Positive: a second family's assets and workflow verify, LF or consistently CRLF, and only
// its assets count.
func TestAuditManagedFamilyPositive(t *testing.T) {
	family := auditFixtureFamily()
	for name, edit := range map[string]func(string) string{
		"LF":   func(text string) string { return text },
		"CRLF": func(text string) string { return strings.ReplaceAll(text, "\n", "\r\n") },
	} {
		root := t.TempDir()
		writeFamilyFixture(t, root, family, edit)
		count, err := auditManagedFamily(t.Context(), root, family)
		if err != nil || count != 2 {
			t.Fatalf("%s: count=%d err=%v", name, count, err)
		}
	}
}

// Negative: a drifted nested asset or a missing workflow fails with the family's gate label,
// and a disabled family that still holds its canonical text fails.
func TestAuditManagedFamilyNegative(t *testing.T) {
	family := auditFixtureFamily()
	root := t.TempDir()
	writeFamilyFixture(t, root, family, func(text string) string { return text })
	writeFixtureFile(t, root, "tools/fixture/third_party/lib/LICENSE", "Apache-2.0\n")
	_, err := auditManagedFamily(t.Context(), root, family)
	want := "[FAIL] Documentation gate asset tools/fixture/third_party/lib/LICENSE differs from the locked Praetor asset"
	if err == nil || !strings.HasPrefix(err.Error(), want) {
		t.Fatalf("drift: %v", err)
	}
	missing := t.TempDir()
	writeFixtureFile(t, missing, "tools/fixture/core.mjs", "export const core = 1;\n")
	writeFixtureFile(t, missing, "tools/fixture/third_party/lib/LICENSE", "MIT\n")
	if _, err := auditManagedFamily(t.Context(), missing, family); err == nil ||
		!strings.Contains(err.Error(), ".github/workflows/fixture.yml is missing or unreadable") {
		t.Fatalf("missing workflow: %v", err)
	}
	disabled := t.TempDir()
	writeFixtureFile(t, disabled, "tools/fixture/core.mjs", "export const core = 1;\r\n")
	if err := auditDisabledManagedFamily(t.Context(), disabled, family); err == nil ||
		err.Error() != "[FAIL] Disabled documentation facet retains Praetor asset tools/fixture/core.mjs" {
		t.Fatalf("disabled family retained its asset: %v", err)
	}
}

// Boundary: a disabled family passes over operator lookalikes and mixed endings, a gate-less
// family audits its assets alone, and a family without a kind still names a gate.
func TestAuditManagedFamilyBoundary(t *testing.T) {
	family := auditFixtureFamily()
	root := t.TempDir()
	writeFixtureFile(t, root, "tools/fixture/core.mjs", "export const core = 2;\n")
	writeFixtureFile(t, root, "tools/fixture/third_party/lib/LICENSE", "MIT\r\n\n")
	writeFixtureFile(t, root, ".github/workflows/fixture.yml", "name: Operator\n")
	if err := auditDisabledManagedFamily(t.Context(), root, family); err != nil {
		t.Fatalf("disabled family claimed operator files: %v", err)
	}
	family.WorkflowFile, family.StatusContext, family.Workflow, family.WorkflowNoun = "", "", "", ""
	assets := t.TempDir()
	writeFamilyFixture(t, assets, family, func(text string) string { return text })
	if count, err := auditManagedFamily(t.Context(), assets, family); err != nil || count != 2 {
		t.Fatalf("gate-less family: count=%d err=%v", count, err)
	}
	if got := familyGate(managedasset.Family{}); got != "Managed gate" {
		t.Fatalf("familyGate without kind = %q", got)
	}
}
