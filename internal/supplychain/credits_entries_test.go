// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package supplychain

import (
	"fmt"
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
    packages: [left-pad]
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
		entry.License != "MIT OR Apache-2.0" || entry.Packages[0] != "left-pad" || entry.Match[0] != "leftpad" {
		t.Fatalf("credits = %+v", credits)
	}
	for _, license := range []string{"MIT", "(MIT OR CC0-1.0)", "Apache-2.0 WITH LLVM-exception", "LicenseRef-OASIS-IPR", "GPL-3.0-or-later",
		licenseUnknown, licenseProprietary, licenseNone} {
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
		"unknown key":          {"use: Pads strings.", "use: Pads strings.\n    owner: x", "field owner not found"},
		"repeated key":         {"use: Pads strings.", "use: Pads strings.\n    use: again", "already defined"},
		"http url":             {"https://example.test/left-pad", "http://example.test/left-pad", "must be an https URL"},
		"unknown section":      {"section: shipped", "section: misc", `section "misc" is not a table`},
		"unknown kind":         {"kind: dependency", "kind: library", `kind "library" is not one of`},
		"unknown relation":     {"relation: shipped", "relation: copied", `relation "copied" is not one of`},
		"artifact outside":     {"use: Pads strings.", "use: Pads strings.\n    artifact: x", "artifact is required in the adapted section and refused elsewhere"},
		"adapted, no artifact": {"section: shipped", "section: adapted", "artifact is required in the adapted section"},
		"free-text license":    {"license: MIT OR Apache-2.0", "license: CC BY 3.0", `license "CC BY 3.0" is not an SPDX expression`},
		"dangling operator":    {"license: MIT OR Apache-2.0", "license: MIT AND", `license "MIT AND" is not an SPDX expression`},
		"operator as term":     {"license: MIT OR Apache-2.0", "license: OR", `license "OR" is not an SPDX expression`},
		"no paths":             {"paths: [package.json]", "paths: []", "must name 1..128 paths"},
		"escaping path":        {"paths: [package.json]", "paths: [../package.json]", `path "../package.json" is not one clean`},
		"glob path":            {"paths: [package.json]", "paths: [\"*.json\"]", `path "*.json" is not one clean`},
		"pipe in use":          {"use: Pads strings.", "use: Pads | strings.", "use must be one line without control characters or |"},
		"bracket in name":      {"name: Left Pad", "name: Left [Pad]", "name must be one line"},
		"empty use":            {"use: Pads strings.", "use: \"\"", "use is required"},
		"empty package":        {"packages: [left-pad]", "packages: [\"\"]", `package or match term "" is required`},
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
// of either is refused; a package answers an identifier it equals or that continues it after a
// slash, case aside, and no other.
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
	entry := CreditEntry{Packages: []string{"github.com/Example/tool", "@scope/pkg"}}
	for id, want := range map[string]bool{
		"github.com/example/tool": true, "github.com/Example/tool/cmd/tool": true, "github.com/Example/toolkit": false,
		"@scope/pkg": true, "@scope/pkg-extra": false, "@scope": false,
	} {
		if got := entry.answers(id); got != want {
			t.Errorf("answers(%q) = %v, want %v", id, got, want)
		}
	}
}
