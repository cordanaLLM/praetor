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

	"github.com/cordanaLLM/praetor/internal/compiler"
)

const (
	// upstreamSkill is the declaring skill of the fixtures.
	upstreamSkill = ".agents/skills/shout/SKILL.md"
	// upstreamURL is the upstream it declares.
	upstreamURL = "https://example.test/upstream"
	// adaptedCredits answers the declaration with one adapted row, after an unrelated table.
	adaptedCredits = "# Credits\n\n## Adapted work\n\nThe rows.\n\n" +
		"| Project | Praetor artifact | Relation | What changed | License |\n| :-- | :-- | :-- | :-- | :-- |\n" +
		"| [Upstream](" + upstreamURL + ") | `shout` skill, `" + upstreamSkill + "` | adapted | Rules rewritten. | MIT, © 2026 Example Author |\n" +
		"\n## Shipped in the binaries\n\n| Project | Use | License |\n| :-- | :-- | :-- |\n| [Other](https://example.test/other) | `" + upstreamSkill + "` | MIT |\n"
	// copiedOverride labels the declaring skill MIT after the whole-tree table.
	copiedOverride = "\n[[annotations]]\npath = [\"" + upstreamSkill + "\"]\nprecedence = \"override\"\nSPDX-FileCopyrightText = \"2026 Example Author\"\nSPDX-License-Identifier = \"MIT\"\n"
)

// upstreamSources is a set of sources in which the skill declares the upstream under MIT and
// the credits page is credits.
func upstreamSources(credits string) UpstreamCreditSources {
	return UpstreamCreditSources{
		Credits:      credits,
		Reuse:        reuseWholeTree,
		Upstreams:    []compiler.AssetUpstream{{Rel: upstreamSkill, DerivedFrom: upstreamURL + " (MIT)"}},
		LicenseTexts: map[string]bool{"MIT": true},
	}
}

// Positive: an adapted, an inspired and a copied row answer the declaration, the copied one with
// REUSE.toml labelling the file and the license text present; a page with no declarations and
// no Adapted work section passes; and a row naming other artifacts beside the skill passes.
func TestCheckUpstreamCreditsPositive(t *testing.T) {
	if err := CheckUpstreamCredits(upstreamSources(adaptedCredits)); err != nil {
		t.Fatal(err)
	}
	inspired := upstreamSources(strings.Replace(adaptedCredits, "| adapted |", "| inspired |", 1))
	if err := CheckUpstreamCredits(inspired); err != nil {
		t.Fatalf("inspired: %v", err)
	}
	copied := upstreamSources(strings.Replace(adaptedCredits, "| adapted |", "| copied |", 1))
	copied.Reuse += copiedOverride
	if err := CheckUpstreamCredits(copied); err != nil {
		t.Fatalf("copied: %v", err)
	}
	if err := CheckUpstreamCredits(UpstreamCreditSources{Credits: "# Credits\n"}); err != nil {
		t.Fatalf("nothing declared: %v", err)
	}
	both := upstreamSources(strings.Replace(adaptedCredits, "`shout` skill,", "`internal/shout`, `shout` skill,", 1))
	if err := CheckUpstreamCredits(both); err != nil {
		t.Fatalf("a row naming two artifacts: %v", err)
	}
}

// Negative: each way a declaration and the page can disagree is a finding naming the file.
func TestCheckUpstreamCreditsNegative(t *testing.T) {
	row := "| [Upstream](" + upstreamURL + ") | `shout` skill, `" + upstreamSkill + "` | adapted | Rules rewritten. | MIT, © 2026 Example Author |"
	copiedCredits := strings.Replace(adaptedCredits, "| adapted |", "| copied |", 1)
	for name, test := range map[string]struct {
		sources UpstreamCreditSources
		want    string
	}{
		"no row":               {upstreamSources(strings.Replace(adaptedCredits, row+"\n", "", 1)), "has no row linking " + upstreamURL},
		"row in another table": {upstreamSources(strings.Replace(adaptedCredits, row+"\n", "", 1) + row + "\n"), "has no row linking"},
		"other URL":            {upstreamSources(strings.Replace(adaptedCredits, "]("+upstreamURL+")", "](https://example.test/fork)", 1)), "has no row linking"},
		"file not named":       {upstreamSources(strings.Replace(adaptedCredits, ", `"+upstreamSkill+"`", "", 1)), "naming `" + upstreamSkill + "`"},
		"other license":        {upstreamSources(strings.Replace(adaptedCredits, "| MIT, ©", "| Apache-2.0, ©", 1)), `under "Apache-2.0", and the file declares "MIT"`},
		"unknown relation":     {upstreamSources(strings.Replace(adaptedCredits, "| adapted |", "| ported |", 1)), `the relation "ported"`},
		"copied, unlabelled":   {upstreamSources(copiedCredits), "REUSE.toml does not label it MIT"},
		"short row":            {upstreamSources(strings.Replace(adaptedCredits, " Rules rewritten. |", "", 1)), "holds 4 cells, want 5"},
		"malformed value": {func() UpstreamCreditSources {
			sources := upstreamSources(adaptedCredits)
			sources.Upstreams[0].DerivedFrom = upstreamURL + " MIT"
			return sources
		}(), "is not \"<https URL> (<SPDX license>)\""},
		"undeclared asset": {func() UpstreamCreditSources {
			sources := upstreamSources(adaptedCredits)
			sources.Upstreams = nil
			return sources
		}(), "that file declares no metadata.derived_from"},
		"declares another upstream": {upstreamSources(strings.Replace(adaptedCredits, row+"\n",
			row+"\n| [Fork](https://example.test/fork) | `"+upstreamSkill+"` | adapted | x | MIT |\n", 1)),
			"credits `" + upstreamSkill + "` to https://example.test/fork, and that file declares " + upstreamURL},
	} {
		err := CheckUpstreamCredits(test.sources)
		if err == nil || !strings.Contains(err.Error(), test.want) {
			t.Errorf("%s: err = %v, want %q", name, err, test.want)
		}
	}
	copied := upstreamSources(copiedCredits)
	copied.Reuse += copiedOverride
	copied.LicenseTexts = map[string]bool{}
	if err := CheckUpstreamCredits(copied); err == nil || !strings.Contains(err.Error(), "LICENSES/MIT.txt does not exist") {
		t.Errorf("copied without the license text: %v", err)
	}
}

// Boundary: a license expression is compared as written and each of its terms must be carried
// when copied; a declaration must hold exactly one space before the license; a heading that only
// starts like the section's does not open it; a REUSE.toml past its bound is an error.
func TestCheckUpstreamCreditsBoundary(t *testing.T) {
	expression := upstreamSources(strings.Replace(strings.Replace(adaptedCredits, "| MIT, ©", "| MIT OR Apache-2.0, ©", 1), "| adapted |", "| copied |", 1))
	expression.Upstreams[0].DerivedFrom = upstreamURL + " (MIT OR Apache-2.0)"
	expression.Reuse += copiedOverride
	expression.LicenseTexts = map[string]bool{"MIT": true, "Apache-2.0": true}
	err := CheckUpstreamCredits(expression)
	if err == nil || !strings.Contains(err.Error(), "does not label it Apache-2.0") || strings.Contains(err.Error(), "does not label it MIT") {
		t.Fatalf("an expression with one term unlabelled: %v", err)
	}
	for _, value := range []string{upstreamURL + "  (MIT)", upstreamURL + " ( MIT)", upstreamURL + " ()", "http://example.test/u (MIT)", upstreamURL + " (AND)"} {
		sources := upstreamSources(adaptedCredits)
		sources.Upstreams[0].DerivedFrom = value
		if err := CheckUpstreamCredits(sources); err == nil || !strings.Contains(err.Error(), "is not") {
			t.Errorf("declaration %q: %v", value, err)
		}
	}
	renamed := upstreamSources(strings.Replace(adaptedCredits, "## Adapted work", "## Adapted work and more", 1))
	if err := CheckUpstreamCredits(renamed); err == nil || !strings.Contains(err.Error(), "has no row linking") {
		t.Fatalf("a longer heading opened the section: %v", err)
	}
	past := upstreamSources(adaptedCredits)
	past.Reuse = strings.Repeat("\n", MaxReuseLines)
	if err := CheckUpstreamCredits(past); err == nil || !strings.Contains(err.Error(), "more than the 4096") {
		t.Fatalf("a REUSE.toml past its bound: %v", err)
	}
}

// ReadUpstreamCreditSources reads the page, REUSE.toml, the declarations and the license texts
// they name; a repository without REUSE.toml or without the license text reads as one.
func TestReadUpstreamCreditSources(t *testing.T) {
	root := t.TempDir()
	write := func(rel, content string) {
		path := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write(AcknowledgementsFile, adaptedCredits)
	write(upstreamSkill, "---\nname: shout\nmetadata:\n  derived_from: \""+upstreamURL+" (MIT AND Apache-2.0)\"\n---\n")
	write(licensesDir+"/MIT.txt", "MIT License\n")
	sources, err := ReadUpstreamCreditSources(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	if sources.Credits != adaptedCredits || sources.Reuse != "" || len(sources.Upstreams) != 1 ||
		!sources.LicenseTexts["MIT"] || sources.LicenseTexts["Apache-2.0"] || len(sources.LicenseTexts) != 2 {
		t.Fatalf("sources = %+v", sources)
	}
	if _, err := ReadUpstreamCreditSources(context.Background(), t.TempDir()); err == nil || !strings.Contains(err.Error(), AcknowledgementsFile) {
		t.Fatalf("a repository without the credits page: %v", err)
	}
}

// The checkout: every persona and skill that declares an upstream is credited with its license,
// and the credits page names no canonical persona or skill that declares none.
func TestShippedUpstreamsAreCredited(t *testing.T) {
	sources, err := ReadUpstreamCreditSources(context.Background(), filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	if len(sources.Upstreams) == 0 {
		t.Fatal("no canonical persona or skill declares an upstream; the read found nothing to check")
	}
	if err := CheckUpstreamCredits(sources); err != nil {
		t.Fatal(err)
	}
}
