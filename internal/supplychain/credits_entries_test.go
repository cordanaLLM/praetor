// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package supplychain

import (
	"fmt"
	"slices"
	"strings"
	"testing"
)

// creditsEntryYAML is one valid entry of the shipped section, written as the list writes it.
const creditsEntryYAML = `  - name: Left Pad
    url: https://example.test/left-pad
    detail: 1.3.0
    section: shipped
    kind: dependency
    relation: shipped
    license: MIT OR Apache-2.0
    notice: © 2026 Example Author
    use: Pads strings.
    paths: [package.json]
    packages: ["npm:left-pad"]
    match: [leftpad]
`

// creditsDocument wraps entries into a list with one original and one download.
func creditsDocument(entries string) string {
	return "originals:\n  - .agents/skills/plain/SKILL.md\ndownloads:\n  - id: example.test/lint\n    path: ci.yml\nentries:\n" + entries
}

// Positive: a list with every field decodes as written, and every license form the list may
// state is accepted.
func TestDecodeCreditsPositive(t *testing.T) {
	credits, err := DecodeCredits([]byte(creditsDocument(creditsEntryYAML)))
	if err != nil {
		t.Fatal(err)
	}
	entry := credits.Entries[0]
	if len(credits.Originals) != 1 || credits.Downloads[0].ID != "example.test/lint" || entry.Name != "Left Pad" || entry.Detail != "1.3.0" ||
		entry.License != "MIT OR Apache-2.0" || entry.Packages[0] != "npm:left-pad" || entry.Match[0] != "leftpad" {
		t.Fatalf("credits = %+v", credits)
	}
	for _, license := range []string{"MIT", "(MIT OR CC0-1.0)", "Apache-2.0 WITH LLVM-exception", "LicenseRef-OASIS-IPR", "GPL-3.0-or-later",
		"GPL-3.0-only", "LGPL-2.1-or-later", "Apache-2.0 AND Vim", licenseUnknown, licenseProprietary, licenseNone} {
		document := creditsDocument(strings.Replace(creditsEntryYAML, "license: MIT OR Apache-2.0", "license: "+license, 1))
		if _, err := DecodeCredits([]byte(document)); err != nil {
			t.Errorf("license %q: %v", license, err)
		}
	}
	adapted := strings.Replace(creditsEntryYAML, "section: shipped", "section: adapted\n    artifact: \"`pad` skill\"", 1)
	if _, err := DecodeCredits([]byte(creditsDocument(adapted))); err != nil {
		t.Fatalf("an adapted entry with its artifact: %v", err)
	}
}

// Negative: every malformed list is refused, naming the entry or the field.
func TestDecodeCreditsNegative(t *testing.T) {
	cases := map[string]struct{ old, new, want string }{
		"unknown key":                 {"use: Pads strings.", "use: Pads strings.\n    owner: x", "field owner not found"},
		"repeated key":                {"use: Pads strings.", "use: Pads strings.\n    use: again", "already defined"},
		"http url":                    {"https://example.test/left-pad", "http://example.test/left-pad", "must be an https URL"},
		"unknown section":             {"section: shipped", "section: misc", `section "misc" is not a table`},
		"unknown kind":                {"kind: dependency", "kind: library", `kind "library" is not one of`},
		"unknown relation":            {"relation: shipped", "relation: copied", `relation "copied" is not one of`},
		"artifact outside":            {"use: Pads strings.", "use: Pads strings.\n    artifact: x", "artifact is required in the adapted section and refused elsewhere"},
		"adapted, no artifact":        {"section: shipped", "section: adapted", "artifact is required in the adapted section"},
		"free-text license":           {"license: MIT OR Apache-2.0", "license: CC BY 3.0", `license "CC BY 3.0" is not an SPDX expression`},
		"dangling operator":           {"license: MIT OR Apache-2.0", "license: MIT AND", `license "MIT AND" is not an SPDX expression`},
		"operator as term":            {"license: MIT OR Apache-2.0", "license: OR", `license "OR" is not an SPDX expression`},
		"no paths":                    {"paths: [package.json]", "paths: []", "must name 1..128 paths"},
		"escaping path":               {"paths: [package.json]", "paths: [../package.json]", `path "../package.json" is not one clean`},
		"glob path":                   {"paths: [package.json]", "paths: [\"*.json\"]", `path "*.json" is not one clean`},
		"pipe in use":                 {"use: Pads strings.", "use: Pads | strings.", "use must be one line without control characters or |"},
		"bracket in name":             {"name: Left Pad", "name: Left [Pad]", "name must be one line"},
		"empty use":                   {"use: Pads strings.", "use: \"\"", "use is required"},
		"empty package":               {`packages: ["npm:left-pad"]`, "packages: [\"\"]", `package or match term "" is required`},
		"package, no ecosystem":       {`packages: ["npm:left-pad"]`, `packages: ["left-pad"]`, `package "left-pad" is not <ecosystem>:<identifier>`},
		"unknown ecosystem":           {`packages: ["npm:left-pad"]`, `packages: ["cargo:left-pad"]`, `package "cargo:left-pad" is not <ecosystem>:<identifier>`},
		"ecosystem, no id":            {`packages: ["npm:left-pad"]`, `packages: ["npm:"]`, `package "npm:" is not <ecosystem>:<identifier>`},
		"deprecated GNU id":           {"license: MIT OR Apache-2.0", "license: GPL-3.0", "names GPL-3.0, which the SPDX License List deprecates"},
		"deprecated in an expression": {"license: MIT OR Apache-2.0", "license: MIT AND (LGPL-2.1 OR GPL-2.0+)", "names LGPL-2.1, which the SPDX License List deprecates"},
		"deprecated exception":        {"license: MIT OR Apache-2.0", "license: LGPL-2.1-only WITH Nokia-Qt-exception-1.1", "names Nokia-Qt-exception-1.1"},
	}
	for name, tc := range cases {
		document := creditsDocument(strings.Replace(creditsEntryYAML, tc.old, tc.new, 1))
		if _, err := DecodeCredits([]byte(document)); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: err = %v, want %q", name, err, tc.want)
		}
	}
	for name, document := range map[string]string{
		"second document":   creditsDocument(creditsEntryYAML) + "---\nentries: []\n",
		"repeated original": strings.Replace(creditsDocument(creditsEntryYAML), "originals:\n", "originals:\n  - .agents/skills/plain/SKILL.md\n", 1),
		"download, no id":   strings.Replace(creditsDocument(creditsEntryYAML), "id: example.test/lint", "id: \"\"", 1),
		"empty":             "",
	} {
		if _, err := DecodeCredits([]byte(document)); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}
}

// Boundary: exactly maxCreditValues paths and a text of exactly maxCreditText bytes pass, one more
// of either is refused; a package answers an item of its own ecosystem whose identifier it equals
// or that continues it after a slash, case aside, and no other: an npm package, a download and an
// image of the same name are three items.
func TestDecodeCreditsBoundary(t *testing.T) {
	paths := make([]string, maxCreditValues+1)
	for index := range paths {
		paths[index] = fmt.Sprintf("p%d.txt", index)
	}
	atBound := strings.Replace(creditsEntryYAML, "paths: [package.json]", "paths: ["+strings.Join(paths[:maxCreditValues], ", ")+"]", 1)
	if _, err := DecodeCredits([]byte(creditsDocument(atBound))); err != nil {
		t.Fatalf("%d paths: %v", maxCreditValues, err)
	}
	past := strings.Replace(creditsEntryYAML, "paths: [package.json]", "paths: ["+strings.Join(paths, ", ")+"]", 1)
	if _, err := DecodeCredits([]byte(creditsDocument(past))); err == nil {
		t.Fatalf("%d paths were accepted", maxCreditValues+1)
	}
	for size, want := range map[int]bool{maxCreditText: true, maxCreditText + 1: false} {
		document := creditsDocument(strings.Replace(creditsEntryYAML, "use: Pads strings.", "use: "+strings.Repeat("u", size), 1))
		if _, err := DecodeCredits([]byte(document)); (err == nil) != want {
			t.Errorf("a use of %d bytes: %v", size, err)
		}
	}
	entry := CreditEntry{Packages: []string{"go:github.com/Example/tool", "npm:@scope/pkg", "download:helm"}}
	for item, want := range map[InventoryItem]bool{
		{Kind: inventoryGoModule, ID: "github.com/example/tool"}: true, {Kind: inventoryGoTool, ID: "github.com/Example/tool/cmd/tool"}: true,
		{Kind: inventoryGoModule, ID: "github.com/Example/toolkit"}: false, {Kind: inventoryNPM, ID: "github.com/example/tool"}: false,
		{Kind: inventoryNPM, ID: "@scope/pkg"}: true, {Kind: inventoryNPM, ID: "@scope/pkg-extra"}: false, {Kind: inventoryNPM, ID: "@scope"}: false,
		{Kind: inventoryDownload, ID: "helm"}: true, {Kind: inventoryNPM, ID: "helm"}: false, {Kind: inventoryImage, ID: "helm"}: false,
		{Kind: inventoryPyPI, ID: "@scope/pkg"}: false,
	} {
		if got := entry.answers(item); got != want {
			t.Errorf("answers(%+v) = %v, want %v", item, got, want)
		}
	}
}

// The path terms: an entry with packages or match terms is looked for by those, as whole words,
// and never by its name; an entry with neither by its name. A generic name (the Go keyword
// continue) is no use, and a term inside a longer word ("go" in "golang") is none.
func TestCreditEntryTerms(t *testing.T) {
	named := CreditEntry{Name: "Continue"}
	specific := CreditEntry{Name: "Continue", Match: []string{`"continue"`, ".continue/"}}
	pkg := CreditEntry{Name: "golangci-lint", Packages: []string{"go:github.com/golangci/golangci-lint"}}
	for _, tc := range []struct {
		entry CreditEntry
		text  string
		want  bool
	}{
		// Positive: the name alone, a specific match term, a package path a slash continues.
		{named, "client continue setup", true},
		{specific, `continue id = "continue"`, true},
		{specific, "path: .continue/mcpservers/praetor.yaml", true},
		{pkg, "go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@latest", true},
		// Negative: a keyword when specific terms exist, a term inside a longer word.
		{specific, "for range items { continue }", false},
		{CreditEntry{Name: "Go"}, "golang toolchain", false},
		{CreditEntry{Name: "Git"}, "digit github gitignore", false},
		{pkg, "github.com/golangci/golangci-linter", false},
		// Boundary: the term at the start and the end of the text, and an empty text.
		{CreditEntry{Name: "Go"}, "go", true},
		{CreditEntry{Name: "Go"}, "toolchain go", true},
		{CreditEntry{Name: "Go"}, "", false},
	} {
		found := slices.ContainsFunc(tc.entry.terms(), func(term string) bool { return namesWord(tc.text, term) })
		if found != tc.want {
			t.Errorf("%s in %q: %v, want %v (terms %q)", tc.entry.Name, tc.text, found, tc.want, tc.entry.terms())
		}
	}
}
