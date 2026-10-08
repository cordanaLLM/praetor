// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package supplychain

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cordanaLLM/praetor/internal/compiler"
	"github.com/cordanaLLM/praetor/internal/config"
	"github.com/cordanaLLM/praetor/internal/testsupport"
)

const (
	// upstreamSkill is the declaring skill of the fixtures.
	upstreamSkill = ".agents/skills/shout/SKILL.md"
	// originalSkill is the fixtures' skill written for praetor.
	originalSkill = ".agents/skills/plain/SKILL.md"
	// upstreamURL is the upstream the declaring skill names.
	upstreamURL = "https://example.test/upstream"
	// copiedOverride labels the declaring skill MIT after the whole-tree table.
	copiedOverride = "\n[[annotations]]\npath = [\"" + upstreamSkill + "\"]\nprecedence = \"override\"\nSPDX-FileCopyrightText = \"2026 Example Author\"\nSPDX-License-Identifier = \"MIT\"\n"
)

// creditsToday is the day the fixtures' exceptions expire against.
var creditsToday = time.Date(2026, time.October, 7, 9, 0, 0, 0, time.UTC)

// upstreamSources is a set of sources the gate passes: the skill declares the upstream under MIT
// and an adapted entry answers it; the plain skill is original; the one npm dependency and the
// one declared download each have an entry; every named path names its item.
func upstreamSources() UpstreamCreditSources {
	return UpstreamCreditSources{
		Credits: Credits{
			Originals: []string{originalSkill},
			Downloads: []CreditDownload{{ID: "example.test/lint", Path: "ci.yml"}},
			Entries: []CreditEntry{
				{Name: "Upstream", URL: upstreamURL, Section: sectionAdapted, Kind: kindAdaptedCode, Relation: relationAdapted,
					License: "MIT", Artifact: "`shout` skill", Use: "Rules rewritten.", Paths: []string{upstreamSkill}},
				{Name: "Left Pad", URL: "https://example.test/left-pad", Section: "shipped", Kind: kindDependency, Relation: relationShipped,
					License: "MIT", Use: "Pads.", Paths: []string{"package.json"}, Packages: []string{"npm:left-pad"}},
				{Name: "Lint", URL: "https://example.test/lint", Section: "tooling", Kind: kindTool, Relation: relationUsedByCI,
					License: "GPL-3.0-only", Use: "Lints.", Paths: []string{"ci.yml"}, Packages: []string{"download:example.test/lint", "go:example.test/lint"}},
			},
		},
		Reuse:        reuseWholeTree,
		Assets:       []compiler.AssetUpstream{{Rel: upstreamSkill, DerivedFrom: upstreamURL + " (MIT)"}, {Rel: originalSkill}},
		LicenseTexts: map[string]bool{"MIT": true},
		Inventory:    []InventoryItem{{Kind: inventoryNPM, ID: "left-pad", Path: "package.json"}},
		PathTexts: map[string]string{
			upstreamSkill: "# shout, adapted from upstream", originalSkill: "# plain",
			"package.json": `{"dependencies":{"left-pad":"1.3.0"}}`, "ci.yml": "curl -o lint https://example.test/lint/releases/v1",
		},
		Today: creditsToday,
	}
}

// unknownLicenseSources is upstreamSources with the dependency's license written unknown and
// excused by a credits exception that expires on expires.
func unknownLicenseSources(expires string) UpstreamCreditSources {
	sources := upstreamSources()
	sources.Credits.Entries[1].License = licenseUnknown
	sources.Exceptions = []config.Exception{{Rule: config.ExceptionRuleCredits, Path: "package.json", Reason: "registry entry names no license", Expires: expires}}
	return sources
}

// Positive: an adapted, an inspired and a vendored entry answer the declaration, the vendored one
// with REUSE.toml labelling the file and the license text present; an unknown license an
// unexpired exception excuses passes; a package matches an identifier that continues it after a
// slash; and an empty repository with an empty list passes.
func TestCheckUpstreamCreditsPositive(t *testing.T) {
	if err := CheckUpstreamCredits(upstreamSources()); err != nil {
		t.Fatal(err)
	}
	for _, relation := range []string{relationInspired, relationVendored} {
		sources := upstreamSources()
		sources.Credits.Entries[0].Relation = relation
		sources.Reuse += copiedOverride
		if err := CheckUpstreamCredits(sources); err != nil {
			t.Fatalf("%s: %v", relation, err)
		}
	}
	if err := CheckUpstreamCredits(unknownLicenseSources("2026-10-30")); err != nil {
		t.Fatalf("an excused unknown license: %v", err)
	}
	tool := upstreamSources()
	tool.Inventory = append(tool.Inventory, InventoryItem{Kind: inventoryGoTool, ID: "example.test/lint/cmd/lint", Path: "ci.yml"})
	if err := CheckUpstreamCredits(tool); err != nil {
		t.Fatalf("a tool path below a credited module: %v", err)
	}
	if err := CheckUpstreamCredits(UpstreamCreditSources{}); err != nil {
		t.Fatalf("nothing used, nothing declared: %v", err)
	}
}

// Negative: each way the list and the repository can disagree is a finding naming the file.
func TestCheckUpstreamCreditsNegative(t *testing.T) {
	for name, test := range map[string]struct {
		edit func(*UpstreamCreditSources)
		want string
	}{
		"inventory item without an entry": {func(s *UpstreamCreditSources) {
			s.Inventory = append(s.Inventory, InventoryItem{Kind: inventoryNPM, ID: "right-pad", Path: "package.json"})
		}, "package.json uses npm package right-pad, and no entry"},
		"download without an entry": {func(s *UpstreamCreditSources) {
			s.Credits.Entries[2].Packages = []string{"download:example.test/lint-other"}
		},
			"ci.yml uses download example.test/lint, and no entry"},
		"download its file no longer names": {func(s *UpstreamCreditSources) { s.PathTexts["ci.yml"] = "lint" },
			"downloads[0] says ci.yml fetches example.test/lint, and that file does not name it"},
		"path that is no file": {func(s *UpstreamCreditSources) { delete(s.PathTexts, "package.json") },
			"entries[1] (Left Pad) names package.json, which is not a file in the repository"},
		"path that no longer uses it": {func(s *UpstreamCreditSources) { s.PathTexts["package.json"] = `{"dependencies":{}}` },
			"entries[1] (Left Pad) names package.json, which no longer uses it"},
		"package no path names": {func(s *UpstreamCreditSources) {
			s.Credits.Entries[1].Packages = append(s.Credits.Entries[1].Packages, "npm:pad-utils")
		},
			"answers package npm:pad-utils, which none of its paths names"},
		"no entry for a derivation": {func(s *UpstreamCreditSources) { s.Credits.Entries[0].URL = "https://example.test/fork" },
			"has no entry with that url naming " + upstreamSkill},
		"other license": {func(s *UpstreamCreditSources) { s.Credits.Entries[0].License = "Apache-2.0" },
			`credits ` + upstreamSkill + ` under "Apache-2.0", and the file declares "MIT"`},
		"relation no derivation takes": {func(s *UpstreamCreditSources) { s.Credits.Entries[0].Relation = relationIntegrated },
			`the relation "integrated"; a derivation is vendored, adapted, inspired`},
		"vendored, unlabelled": {func(s *UpstreamCreditSources) { s.Credits.Entries[0].Relation = relationVendored },
			"REUSE.toml does not label it MIT"},
		"vendored, without the text": {func(s *UpstreamCreditSources) {
			s.Credits.Entries[0].Relation, s.Reuse, s.LicenseTexts = relationVendored, s.Reuse+copiedOverride, map[string]bool{}
		}, "LICENSES/MIT.txt does not exist"},
		"malformed declaration": {func(s *UpstreamCreditSources) { s.Assets[0].DerivedFrom = upstreamURL + " MIT" },
			"is not \"<https URL> (<SPDX license>)\""},
		"entry crediting an undeclared asset": {func(s *UpstreamCreditSources) { s.Assets[0].DerivedFrom = "" },
			"that file declares no metadata.derived_from; add \"" + upstreamURL + " (MIT)\""},
		"skill neither derived nor original": {func(s *UpstreamCreditSources) { s.Credits.Originals = nil },
			originalSkill + " declares no metadata.derived_from and docs/credits.yaml does not list it under originals"},
		"original that declares an upstream": {func(s *UpstreamCreditSources) { s.Credits.Originals = append(s.Credits.Originals, upstreamSkill) },
			"lists " + upstreamSkill + " under originals, and that file declares"},
		"original that is no skill": {func(s *UpstreamCreditSources) {
			s.Credits.Originals = append(s.Credits.Originals, ".agents/skills/gone/SKILL.md")
		},
			"lists .agents/skills/gone/SKILL.md under originals, which is no canonical persona or skill"},
		"unknown license without an exception": {func(s *UpstreamCreditSources) { *s = unknownLicenseSources("2026-10-30"); s.Exceptions = nil },
			"states license unknown, and no unexpired exceptions entry with rule credits names one of its paths"},
		"expired exception": {func(s *UpstreamCreditSources) { *s = unknownLicenseSources("2026-10-06") },
			"exceptions entry package.json (credits) expired on 2026-10-06"},
		"stale exception": {func(s *UpstreamCreditSources) {
			*s = unknownLicenseSources("2026-10-30")
			s.Credits.Entries[1].License = "MIT"
		},
			"exceptions entry package.json (credits) excuses no entry"},
	} {
		sources := upstreamSources()
		test.edit(&sources)
		err := CheckUpstreamCredits(sources)
		if err == nil || !strings.Contains(err.Error(), test.want) {
			t.Errorf("%s: err = %v, want %q", name, err, test.want)
		}
	}
}

// Boundary: an exception holds on its expires day and not the day after; a package matches only
// at a slash, so a longer name is not answered; a vendored license expression needs every term
// carried; a declaration needs exactly one space before the license; and a REUSE.toml past its
// bound is an error.
func TestCheckUpstreamCreditsBoundary(t *testing.T) {
	if err := CheckUpstreamCredits(unknownLicenseSources("2026-10-07")); err != nil {
		t.Fatalf("an exception on its expires day: %v", err)
	}
	if err := CheckUpstreamCredits(unknownLicenseSources("2026-10-06")); err == nil || !strings.Contains(err.Error(), "states license unknown") {
		t.Fatalf("an exception the day after it expired: %v", err)
	}
	longer := upstreamSources()
	longer.Inventory = append(longer.Inventory, InventoryItem{Kind: inventoryNPM, ID: "left-pad-extra", Path: "package.json"})
	if err := CheckUpstreamCredits(longer); err == nil || !strings.Contains(err.Error(), "npm package left-pad-extra") {
		t.Fatalf("a longer name was answered by a package it only starts with: %v", err)
	}
	expression := upstreamSources()
	expression.Assets[0].DerivedFrom = upstreamURL + " (MIT OR Apache-2.0)"
	expression.Credits.Entries[0].License, expression.Credits.Entries[0].Relation = "MIT OR Apache-2.0", relationVendored
	expression.Reuse += copiedOverride
	expression.LicenseTexts = map[string]bool{"MIT": true, "Apache-2.0": true}
	err := CheckUpstreamCredits(expression)
	if err == nil || !strings.Contains(err.Error(), "does not label it Apache-2.0") || strings.Contains(err.Error(), "does not label it MIT") {
		t.Fatalf("an expression with one term unlabelled: %v", err)
	}
	for _, value := range []string{upstreamURL + "  (MIT)", upstreamURL + " ( MIT)", upstreamURL + " ()", "http://example.test/u (MIT)", upstreamURL + " (AND)"} {
		sources := upstreamSources()
		sources.Assets[0].DerivedFrom = value
		if err := CheckUpstreamCredits(sources); err == nil || !strings.Contains(err.Error(), "is not") {
			t.Errorf("declaration %q: %v", value, err)
		}
	}
	past := upstreamSources()
	past.Reuse = strings.Repeat("\n", MaxReuseLines)
	if err := CheckUpstreamCredits(past); err == nil || !strings.Contains(err.Error(), "more than the 4096") {
		t.Fatalf("a REUSE.toml past its bound: %v", err)
	}
}

// creditsFixture is a docs/credits.yaml the read test writes: the skill's adapted entry, the
// left-pad entry and the original skill.
const creditsFixture = `originals:
  - .agents/skills/plain/SKILL.md
entries:
  - name: Upstream
    url: https://example.test/upstream
    section: adapted
    kind: adapted code
    relation: adapted
    license: MIT AND Apache-2.0
    artifact: "` + "`shout`" + ` skill"
    use: Rules rewritten.
    paths: [.agents/skills/shout/SKILL.md]
  - name: Left Pad
    url: https://example.test/left-pad
    section: shipped
    kind: dependency
    relation: shipped
    license: unknown
    use: Pads.
    paths: [package.json, gone.txt]
    packages: ["npm:left-pad"]
`

// writeRepoFile writes content to rel below root.
func writeRepoFile(t *testing.T, root, rel, content string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

// ReadUpstreamCreditSources reads the list, the assets, the license texts they name, the
// inventory, the manifest's credits exceptions and the named files; a named path that is missing
// is left out, and a repository without the list fails naming it.
func TestReadUpstreamCreditSources(t *testing.T) {
	root := t.TempDir()
	testsupport.InitGitRepoWithOrigin(t, root, "")
	writeRepoFile(t, root, AcknowledgementsList, creditsFixture)
	writeRepoFile(t, root, upstreamSkill, "---\nname: shout\nmetadata:\n  derived_from: \""+upstreamURL+" (MIT AND Apache-2.0)\"\n---\n")
	writeRepoFile(t, root, originalSkill, "---\nname: plain\n---\n")
	writeRepoFile(t, root, LicensesDir+"/MIT.txt", "MIT License\n")
	writeRepoFile(t, root, "package.json", `{"name":"x","dependencies":{"left-pad":"1.3.0"}}`)
	writeRepoFile(t, root, config.ManifestFileName, "version: 1\nexceptions:\n  - rule: credits\n    path: package.json\n    reason: no license in the registry entry\n    expires: \""+
		config.ExceptionDay(time.Now()).AddDate(0, 0, 30).Format(config.ExceptionDateLayout)+"\"\n  - rule: clang-tidy-coverage\n    path: a.c\n    reason: r\n    expires: \"2020-01-01\"\n")
	sources, err := ReadUpstreamCreditSources(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	if len(sources.Credits.Entries) != 2 || len(sources.Assets) != 2 || !sources.LicenseTexts["MIT"] || sources.LicenseTexts["Apache-2.0"] ||
		len(sources.Inventory) != 1 || len(sources.Exceptions) != 1 || sources.Exceptions[0].Rule != config.ExceptionRuleCredits {
		t.Fatalf("sources = %+v", sources)
	}
	if _, present := sources.PathTexts["gone.txt"]; present || !strings.Contains(sources.PathTexts["package.json"], "left-pad") {
		t.Fatalf("path texts = %v", sources.PathTexts)
	}
	if err := CheckUpstreamCredits(sources); err == nil || !strings.Contains(err.Error(), "names gone.txt, which is not a file") {
		t.Fatalf("the missing path is not a finding: %v", err)
	}
	if _, err := ReadUpstreamCreditSources(context.Background(), t.TempDir()); err == nil || !strings.Contains(err.Error(), AcknowledgementsList) {
		t.Fatalf("a repository without the credits list: %v", err)
	}
}

// The checkout: the credits list answers every inventory item and declared download, names only
// files that still use each item, credits every derived persona and skill with its license, marks
// every other one original, and is excused wherever it states an unknown license. A dependency
// planted without an entry fails it, so the pass is not a gate that reads nothing.
func TestShippedUpstreamsAreCredited(t *testing.T) {
	sources, err := ReadUpstreamCreditSources(context.Background(), filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	if len(sources.Inventory) < 50 || len(sources.Credits.Entries) < 50 || len(sources.Assets) < 10 {
		t.Fatalf("read %d inventory items, %d entries and %d personas and skills; the read found too little to check",
			len(sources.Inventory), len(sources.Credits.Entries), len(sources.Assets))
	}
	if err := CheckUpstreamCredits(sources); err != nil {
		t.Fatal(err)
	}
	sources.Inventory = append(sources.Inventory, InventoryItem{Kind: inventoryNPM, ID: "planted-uncredited-package", Path: "tools/markdownlint/package.json"})
	if err := CheckUpstreamCredits(sources); err == nil || !strings.Contains(err.Error(), "planted-uncredited-package") {
		t.Fatalf("a planted dependency without an entry passed: %v", err)
	}
}
