// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package managedasset

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
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
	if len(families) != 2 || families[0].Name != "Markdown" || families[0].RefuseForeign || len(families[0].Attributes) != 0 {
		t.Fatalf("documentation families = %+v, want the Markdown family first, with refuse-on-first-adopt off and no attribute rule", families)
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

// Positive: the figure engine family is registered under the documentation facet, which
// selects it after the Markdown family, with its nested inventory, refuse-on-first-adopt on, no
// hosted workflow, earlier texts of its managed files only, the attribute rules that keep its
// hashed files LF and its vendored files unconverted, and its vendored MIT tree.
func TestFigureFamilyDeclaration(t *testing.T) {
	families := ForFacet(DocumentationFacet)
	index := slices.IndexFunc(families, func(f Family) bool { return f.Name == "Figure engine" })
	if index != 1 {
		t.Fatalf("the documentation facet does not select the figure engine family second: %+v", families)
	}
	figures := families[index]
	if figures.Facet != DocumentationFacet || figures.Directory != "tools/figures" || figures.Source != "tools/figures/assets.go" ||
		!figures.RefuseForeign || figures.WorkflowFile != "" || len(figures.Prior) == 0 {
		t.Fatalf("figure engine family = %+v", figures)
	}
	for digest, rel := range figures.Prior {
		if !slices.Contains(figures.AssetPaths(), rel) {
			t.Fatalf("figure engine prior text %s names %s, which is not one of its assets", digest, rel)
		}
	}
	paths := figures.ManagedPaths()
	for _, want := range []string{"tools/figures/core.mjs", "tools/figures/dist/player.js", "tools/figures/third_party/interfig/upstream/LICENSE", "tools/figures/README.md"} {
		if !slices.Contains(paths, want) {
			t.Fatalf("figure engine managed paths %v lack %s", paths, want)
		}
	}
	wantRules := []string{
		"tools/figures/** text eol=lf",
		"docs/figures/*.ts text eol=lf",
		"docs/assets/figures/* text eol=lf",
		"tools/figures/third_party/interfig/upstream/** -text",
	}
	if !slices.Equal(figures.Attributes, wantRules) || !slices.Equal(AttributesOf(families), wantRules) {
		t.Fatalf("figure attribute rules = %q, documentation rules = %q, want %q", figures.Attributes, AttributesOf(families), wantRules)
	}
	if figures.VendoredGlob() != "tools/figures/third_party/interfig/upstream/**" || figures.VendoredLicense != "MIT" {
		t.Fatalf("vendored tree = %q under %q", figures.VendoredGlob(), figures.VendoredLicense)
	}
}

// Positive, negative and boundary: selectFacet keeps every family of the facet in order, skips
// one of another facet, and reads no further than MaxFamilies.
func TestSelectFacetKeepsItsFacet(t *testing.T) {
	first, second, other := fixtureFamily(), fixtureFamily(), fixtureFamily()
	first.Name, second.Name, other.Name = "first", "second", "other"
	other.Facet = "custom:other"
	names := func(families []Family) []string {
		var out []string
		for _, family := range families {
			out = append(out, family.Name)
		}
		return out
	}
	if got := names(selectFacet([]Family{first, other, second}, DocumentationFacet)); !slices.Equal(got, []string{"first", "second"}) {
		t.Fatalf("selected %v, want both documentation families in order", got)
	}
	if got := selectFacet([]Family{other}, DocumentationFacet); len(got) != 0 {
		t.Fatalf("a family of another facet was selected: %v", names(got))
	}
	many := make([]Family, MaxFamilies+1)
	for index := range many {
		many[index] = first
	}
	if got := selectFacet(many, DocumentationFacet); len(got) != MaxFamilies {
		t.Fatalf("selected %d of %d families, want the MaxFamilies bound %d", len(got), len(many), MaxFamilies)
	}
}

// Positive: attribute rules and a vendored tree with its license validate, and AttributesOf
// joins the rules of several families in order.
func TestFamilyAttributesPositive(t *testing.T) {
	family := fixtureFamily()
	family.Attributes = []string{"tools/fixture/** text eol=lf", "tools/fixture/third_party/** -text"}
	family.VendoredTree, family.VendoredLicense = "third_party/**", "MIT"
	if err := family.Validate(); err != nil {
		t.Fatal(err)
	}
	if family.VendoredGlob() != "tools/fixture/third_party/**" {
		t.Fatalf("vendored glob = %q", family.VendoredGlob())
	}
	other := fixtureFamily()
	other.Attributes = []string{"docs/fixture/* text eol=lf"}
	got := AttributesOf([]Family{family, fixtureFamily(), other})
	if !slices.Equal(got, append(slices.Clone(family.Attributes), other.Attributes...)) {
		t.Fatalf("joined rules = %q", got)
	}
}

// Negative: a blank, padded, commented or multi-line rule, a vendored tree without its license
// or the reverse, and an escaping tree fail validation.
func TestFamilyAttributesNegative(t *testing.T) {
	for name, mutate := range map[string]func(*Family){
		"blank rule":       func(f *Family) { f.Attributes = []string{""} },
		"padded rule":      func(f *Family) { f.Attributes = []string{" a text"} },
		"comment rule":     func(f *Family) { f.Attributes = []string{"# a text"} },
		"two lines":        func(f *Family) { f.Attributes = []string{"a text\nb text"} },
		"tree alone":       func(f *Family) { f.VendoredTree = "third_party/**" },
		"license alone":    func(f *Family) { f.VendoredLicense = "MIT" },
		"escaping tree":    func(f *Family) { f.VendoredTree, f.VendoredLicense = "../vendor/**", "MIT" },
		"license with gap": func(f *Family) { f.VendoredTree, f.VendoredLicense = "third_party/**", "MIT OR"+"\n" },
	} {
		broken := fixtureFamily()
		mutate(&broken)
		if err := broken.Validate(); err == nil {
			t.Fatalf("%s: malformed family validated", name)
		}
	}
	if fixtureFamily().VendoredGlob() != "" {
		t.Fatal("a family without a vendored tree names a glob")
	}
}

// Boundary: MaxAttributes rules validate and one more does not; AttributesOf reads no more than
// MaxAttributes rules of one family and nothing of a family without rules.
func TestFamilyAttributesBoundary(t *testing.T) {
	family := fixtureFamily()
	family.Attributes = slices.Repeat([]string{"x text"}, MaxAttributes)
	if err := family.Validate(); err != nil {
		t.Fatalf("rules at the bound failed validation: %v", err)
	}
	family.Attributes = append(family.Attributes, "y text")
	if err := family.Validate(); err == nil {
		t.Fatal("rules above the bound validated")
	}
	if got := AttributesOf([]Family{family}); len(got) != MaxAttributes {
		t.Fatalf("AttributesOf read %d rules, want the bound %d", len(got), MaxAttributes)
	}
	if got := AttributesOf([]Family{fixtureFamily()}); len(got) != 0 {
		t.Fatalf("a family without rules yielded %q", got)
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

const (
	checkoutSHA  = "3d3c42e5aac5ba805825da76410c181273ba90b1"
	pinnedStep   = "      - uses: actions/checkout@" + checkoutSHA + "  # v7.0.1\n"
	workflowHead = "name: Fixture Gate\njobs:\n  gate:\n    runs-on: ubuntu-26.04\n    steps:\n"
)

// Positive: the Markdown workflow pins every one of its actions by full commit SHA with its
// release as a trailing comment, and a fixture workflow in the same form validates.
func TestWorkflowPinsEveryActionPositive(t *testing.T) {
	markdown := ForFacet(DocumentationFacet)[0]
	unpinned, err := unpinnedActions(markdown.Workflow)
	if err != nil || len(unpinned) != 0 {
		t.Fatalf("the Markdown workflow leaves %q unpinned: %v", unpinned, err)
	}
	if uses := strings.Count(markdown.Workflow, " uses: "); uses < 2 {
		t.Fatalf("the Markdown workflow names %d actions; the pin check saw nothing to hold", uses)
	}
	family := fixtureFamily()
	family.Workflow = workflowHead + pinnedStep +
		"      - name: Setup\n        uses: actions/setup-node@820762786026740c76f36085b0efc47a31fe5020   # 7.0.0\n"
	if err := family.Validate(); err != nil {
		t.Fatal(err)
	}
}

// Negative: a tag, a branch, a short or uppercase SHA, a SHA without its release comment, a
// comment one space from it, and a Docker reference each fail validation.
func TestWorkflowPinsEveryActionNegative(t *testing.T) {
	for name, step := range map[string]string{
		"tag":             "      - uses: actions/checkout@v7\n",
		"tag and comment": "      - uses: actions/checkout@v7.0.1  # v7.0.1\n",
		"branch":          "      - uses: owner/action/path@main\n",
		"short SHA":       "      - uses: actions/checkout@" + checkoutSHA[:39] + "  # v7.0.1\n",
		"uppercase SHA":   "      - uses: actions/checkout@" + strings.ToUpper(checkoutSHA) + "  # v7.0.1\n",
		"no comment":      "      - uses: actions/checkout@" + checkoutSHA + "\n",
		"one space":       "      - uses: actions/checkout@" + checkoutSHA + " # v7.0.1\n",
		"docker":          "      - uses: docker://alpine:3\n",
	} {
		family := fixtureFamily()
		family.Workflow = workflowHead + pinnedStep + step
		err := family.Validate()
		if err == nil || !strings.Contains(err.Error(), "pin every action by full commit SHA") {
			t.Fatalf("%s: %v", name, err)
		}
	}
}

// Boundary: a workflow without actions, a local action and a commented-out tag reference
// pass; a workflow at MaxWorkflowLines is scanned, one line more is refused.
func TestWorkflowPinsEveryActionBoundary(t *testing.T) {
	for _, workflow := range []string{
		"name: Fixture Gate\n",
		workflowHead + "      - uses: ./.github/actions/local\n",
		workflowHead + "      # uses: actions/checkout@v7\n" + pinnedStep,
	} {
		if unpinned, err := unpinnedActions(workflow); err != nil || len(unpinned) != 0 {
			t.Fatalf("%q: unpinned=%q err=%v", workflow, unpinned, err)
		}
	}
	atBound := strings.Repeat("\n", MaxWorkflowLines-1)
	if _, err := unpinnedActions(atBound); err != nil {
		t.Fatalf("a workflow at the line bound was refused: %v", err)
	}
	if _, err := unpinnedActions(atBound + "\n"); err == nil {
		t.Fatal("a workflow above the line bound was scanned")
	}
	family := fixtureFamily()
	family.Workflow = atBound + "\n"
	if err := family.Validate(); err == nil {
		t.Fatal("an unscannable workflow validated")
	}
}

// priorWorkflow is an earlier text of the fixture family's workflow.
const priorWorkflow = "name: Fixture Gate\non: push\n"

// sha256Hex spells a digest the way Family.Prior keys are spelled, computed here rather than
// through the code under test.
func sha256Hex(text string) string {
	sum := sha256.Sum256([]byte(text))
	return hex.EncodeToString(sum[:])
}

func priorFixtureFamily() Family {
	family := fixtureFamily()
	family.Prior = map[string]string{sha256Hex(priorWorkflow): family.WorkflowFile}
	return family
}

// Positive: an exact earlier text of a managed path is recognised in either consistent
// line-ending style, and the Markdown family declares earlier texts of its own.
func TestFamilyPriorTextPositive(t *testing.T) {
	family := priorFixtureFamily()
	if err := family.Validate(); err != nil {
		t.Fatal(err)
	}
	for _, text := range []string{priorWorkflow, strings.ReplaceAll(priorWorkflow, "\n", "\r\n")} {
		if !family.PriorText(family.WorkflowFile, []byte(text)) {
			t.Fatalf("earlier text %q is not recognised", text)
		}
	}
	if markdown := ForFacet(DocumentationFacet)[0]; len(markdown.Prior) == 0 {
		t.Fatal("the Markdown family recognises no earlier text of its workflow")
	}
}

// Negative: the current text, an edited earlier text, mixed endings and an earlier text at a
// path its digest does not name are not prior texts; a malformed Prior fails validation.
func TestFamilyPriorTextNegative(t *testing.T) {
	family := priorFixtureFamily()
	for _, probe := range []struct{ rel, text string }{
		{family.WorkflowFile, family.Workflow},
		{family.WorkflowFile, priorWorkflow + "# edited\n"},
		{family.WorkflowFile, "name: Fixture Gate\r\non: push\n"},
		{"tools/fixture/core.mjs", priorWorkflow},
	} {
		if family.PriorText(probe.rel, []byte(probe.text)) {
			t.Fatalf("%s holding %q was taken for an earlier Praetor text", probe.rel, probe.text)
		}
	}
	digest := sha256Hex(priorWorkflow)
	for name, prior := range map[string]map[string]string{
		"uppercase digest": {strings.ToUpper(digest): family.WorkflowFile},
		"short digest":     {digest[:63]: family.WorkflowFile},
		"unmanaged path":   {digest: "tools/fixture/not-in-the-inventory.json"},
		"current text":     {sha256Hex(family.Workflow): family.WorkflowFile},
	} {
		broken := fixtureFamily()
		broken.Prior = prior
		if err := broken.Validate(); err == nil {
			t.Fatalf("%s: malformed Prior validated", name)
		}
	}
}

// Boundary: a family without Prior recognises nothing, an empty earlier text is recognised
// like any other, and Prior validates at MaxPriorTexts entries but not one more.
func TestFamilyPriorTextBoundary(t *testing.T) {
	family := fixtureFamily()
	if family.PriorText(family.WorkflowFile, []byte(priorWorkflow)) {
		t.Fatal("a family without Prior recognised an earlier text")
	}
	family.Prior = map[string]string{sha256Hex(""): "tools/fixture/core.mjs"}
	if err := family.Validate(); err != nil || !family.PriorText("tools/fixture/core.mjs", nil) {
		t.Fatalf("an empty earlier text: err=%v", err)
	}
	family.Prior = map[string]string{}
	for index := 0; index < MaxPriorTexts; index++ {
		family.Prior[sha256Hex(fmt.Sprintf("text %d\n", index))] = family.WorkflowFile
	}
	if err := family.Validate(); err != nil {
		t.Fatalf("Prior at its bound failed validation: %v", err)
	}
	family.Prior[sha256Hex("one more\n")] = family.WorkflowFile
	if err := family.Validate(); err == nil {
		t.Fatal("Prior above its bound validated")
	}
}

// Positive: PriorRendering reports the line-ending style of a recognised earlier text, so a
// refresh writes the current text back in the file's own style.
func TestFamilyPriorRenderingPositiveReportsStyle(t *testing.T) {
	family := priorFixtureFamily()
	if known, crlf := family.PriorRendering(family.WorkflowFile, []byte(priorWorkflow)); !known || crlf {
		t.Fatalf("LF earlier text: known=%v crlf=%v", known, crlf)
	}
	crlfText := strings.ReplaceAll(priorWorkflow, "\n", "\r\n")
	if known, crlf := family.PriorRendering(family.WorkflowFile, []byte(crlfText)); !known || !crlf {
		t.Fatalf("CRLF checkout of an earlier text: known=%v crlf=%v", known, crlf)
	}
}

// Negative: a CRLF edit, and a CRLF earlier text at a path its digest does not name, are not
// earlier texts, so no refresh style is reported for them.
func TestFamilyPriorRenderingNegativeRefusesEditsAndOtherPaths(t *testing.T) {
	family := priorFixtureFamily()
	edited := strings.ReplaceAll(priorWorkflow+"# edited\n", "\n", "\r\n")
	if known, _ := family.PriorRendering(family.WorkflowFile, []byte(edited)); known {
		t.Fatal("an edited CRLF copy was taken for an earlier Praetor text")
	}
	crlfText := strings.ReplaceAll(priorWorkflow, "\n", "\r\n")
	if known, _ := family.PriorRendering("tools/fixture/core.mjs", []byte(crlfText)); known {
		t.Fatal("an earlier text was recognised at a path its digest does not name")
	}
}

// Boundary: mixed endings and a lone carriage return match nothing and report no CRLF style.
func TestFamilyPriorRenderingBoundaryMixedEndings(t *testing.T) {
	family := priorFixtureFamily()
	for _, text := range []string{"name: Fixture Gate\r\non: push\n", "name: Fixture Gate\ron: push\n"} {
		if known, crlf := family.PriorRendering(family.WorkflowFile, []byte(text)); known || crlf {
			t.Fatalf("%q: known=%v crlf=%v, want neither", text, known, crlf)
		}
	}
}
