// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package managedasset

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"testing/fstest"
)

// fixtureFamily is a second family shaped like the figure engine: nested asset paths, its own
// workflow, the documentation facet, and refuse-on-first-adopt on.
func fixtureFamily() Family {
	return Family{
		Name: "fixture engine", Kind: "documentation", AssetNoun: "fixture engine asset",
		WorkflowNoun: "fixture workflow", Facet: DocumentationFacet,
		Directory: "tools/fixture", Source: "tools/fixture/assets.go",
		FS: fstest.MapFS{
			"core.mjs":                  {Data: []byte("export const core = 1;\n")},
			"third_party/lib/LICENSE":   {Data: []byte("MIT\n")},
			"not-in-the-inventory.json": {Data: []byte("{}\n")},
		},
		Assets:        []string{"core.mjs", "third_party/lib/LICENSE"},
		MaxAssets:     2,
		WorkflowFile:  ".github/workflows/fixture.yml",
		StatusContext: "Fixture Gate",
		Workflow:      "name: Fixture Gate\n",
		RefuseForeign: true,
	}
}

// Positive: the registry is valid, bounded, has unique names, sources and disjoint
// directories, and every family's go:embed directive is exactly the one in its Source file.
func TestFamiliesRegistryIsValid(t *testing.T) {
	families := Families()
	if len(families) == 0 || len(families) > MaxFamilies {
		t.Fatalf("registry holds %d families, want 1..%d", len(families), MaxFamilies)
	}
	seen := map[string]bool{}
	for _, family := range families {
		if err := family.Validate(); err != nil {
			t.Fatal(err)
		}
		for _, key := range []string{"name:" + family.Name, "dir:" + family.Directory, "source:" + family.Source} {
			if seen[key] {
				t.Fatalf("registry repeats %s", key)
			}
			seen[key] = true
		}
		source, err := os.ReadFile(filepath.Join("..", "..", filepath.FromSlash(family.Source)))
		if err != nil {
			t.Fatal(err)
		}
		if !slices.Contains(strings.Split(string(source), "\n"), family.EmbedDirective()) {
			t.Fatalf("%s does not carry the directive %q", family.Source, family.EmbedDirective())
		}
		for _, name := range family.Names() {
			if _, err := family.Read(name); err != nil {
				t.Fatalf("%s: %v", family.Name, err)
			}
		}
	}
	for index, outer := range families {
		for _, inner := range families[index+1:] {
			if strings.HasPrefix(outer.Directory+"/", inner.Directory+"/") || strings.HasPrefix(inner.Directory+"/", outer.Directory+"/") {
				t.Fatalf("families %s and %s share a directory", outer.Name, inner.Name)
			}
		}
	}
}

// Positive: the Markdown family declares the documentation gate's paths in emission order.
func TestMarkdownFamilyDeclaration(t *testing.T) {
	families := ForFacet(DocumentationFacet)
	if len(families) != 1 || families[0].Name != "Markdown" || families[0].RefuseForeign {
		t.Fatalf("documentation families = %+v, want the Markdown family with refuse-on-first-adopt off", families)
	}
	want := []string{
		".github/workflows/praetor-docs.yml",
		"tools/markdownlint/package.json",
		"tools/markdownlint/package-lock.json",
		"tools/markdownlint/markdownlint-cli2.yaml",
		"tools/markdownlint/verify.mjs",
		"tools/markdownlint/no-private-scratch-links.mjs",
	}
	if got := families[0].ManagedPaths(); !slices.Equal(got, want) {
		t.Fatalf("managed paths = %v, want %v", got, want)
	}
	if !strings.Contains(families[0].Workflow, "name: "+families[0].StatusContext+"\n") {
		t.Fatal("the Markdown workflow does not report its status context")
	}
}

// Positive: a nested second family resolves paths, canonical bytes and its directive.
func TestFamilyAccessorsPositive(t *testing.T) {
	family := fixtureFamily()
	if err := family.Validate(); err != nil {
		t.Fatal(err)
	}
	if got := family.ManagedPaths(); !slices.Equal(got, []string{
		".github/workflows/fixture.yml", "tools/fixture/core.mjs", "tools/fixture/third_party/lib/LICENSE",
	}) {
		t.Fatalf("managed paths = %v", got)
	}
	data, owned, err := family.Canonical("tools/fixture/third_party/lib/LICENSE")
	if err != nil || !owned || string(data) != "MIT\n" {
		t.Fatalf("nested canonical = %q owned=%v err=%v", data, owned, err)
	}
	data, owned, err = family.Canonical(".github/workflows/fixture.yml")
	if err != nil || !owned || string(data) != family.Workflow {
		t.Fatalf("workflow canonical = %q owned=%v err=%v", data, owned, err)
	}
	if got := family.EmbedDirective(); got != "//go:embed core.mjs third_party/lib/LICENSE" {
		t.Fatalf("directive = %q", got)
	}
	names := family.Names()
	names[0] = "mutated"
	if family.Names()[0] != "core.mjs" {
		t.Fatal("Names exposed the inventory for mutation")
	}
	if selected := ForFacet("custom:none"); len(selected) != 0 {
		t.Fatalf("an unknown facet selected %d families", len(selected))
	}
}

// Negative: paths the family does not own, files outside the inventory, and malformed
// declarations are refused.
func TestFamilyAccessorsNegative(t *testing.T) {
	family := fixtureFamily()
	for _, rel := range []string{"tools/fixture/not-in-the-inventory.json", "tools/fixtureX/core.mjs", "core.mjs", ".github/workflows/other.yml"} {
		if _, owned, err := family.Canonical(rel); owned || err != nil {
			t.Fatalf("Canonical(%q) owned=%v err=%v", rel, owned, err)
		}
	}
	if _, err := family.Read("not-in-the-inventory.json"); err == nil || !strings.Contains(err.Error(), "unknown fixture asset") {
		t.Fatalf("an unlisted embedded file was read: %v", err)
	}
	for name, mutate := range map[string]func(*Family){
		"no kind":          func(f *Family) { f.Kind = " " },
		"no FS":            func(f *Family) { f.FS = nil },
		"source elsewhere": func(f *Family) { f.Source = "tools/other/assets.go" },
		"unclean dir":      func(f *Family) { f.Directory = "tools/../fixture" },
		"duplicate asset":  func(f *Family) { f.Assets = []string{"core.mjs", "core.mjs"} },
		"escaping asset":   func(f *Family) { f.Assets = []string{"../core.mjs"} },
		"partial gate":     func(f *Family) { f.StatusContext = "" },
		"workflow inside":  func(f *Family) { f.WorkflowFile = "tools/fixture/gate.yml" },
		"empty inventory":  func(f *Family) { f.Assets = nil },
	} {
		broken := fixtureFamily()
		mutate(&broken)
		if err := broken.Validate(); err == nil {
			t.Fatalf("%s: malformed family validated", name)
		}
	}
}

// Boundary: MaxAssets cuts the inventory, a family without a hosted gate owns only its
// assets, and a non-positive bound yields nothing.
func TestFamilyAccessorsBoundary(t *testing.T) {
	family := fixtureFamily()
	family.MaxAssets = 1
	if err := family.Validate(); err == nil {
		t.Fatal("an inventory above MaxAssets validated")
	}
	if got := family.Names(); !slices.Equal(got, []string{"core.mjs"}) {
		t.Fatalf("names at bound 1 = %v", got)
	}
	if _, err := family.Read("third_party/lib/LICENSE"); err == nil {
		t.Fatal("an asset past MaxAssets was read")
	}
	family = fixtureFamily()
	family.WorkflowFile, family.StatusContext, family.Workflow, family.WorkflowNoun = "", "", "", ""
	if err := family.Validate(); err != nil {
		t.Fatalf("a family without a hosted gate failed validation: %v", err)
	}
	if got := family.ManagedPaths(); !slices.Equal(got, family.AssetPaths()) {
		t.Fatalf("gate-less managed paths = %v", got)
	}
	if _, owned, err := family.Canonical(""); owned || err != nil {
		t.Fatalf("the empty path is owned by a gate-less family: owned=%v err=%v", owned, err)
	}
	family.MaxAssets = 0
	if family.Names() != nil || len(family.AssetPaths()) != 0 {
		t.Fatal("a non-positive bound yielded assets")
	}
}
