// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package supplychain

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/testsupport"
)

// pageCredits lists one entry in every section; the adapted one carries its artifact.
func pageCredits() Credits {
	var credits Credits
	for _, section := range creditSections {
		entry := CreditEntry{Name: "Item " + section.id, URL: "https://example.test/" + section.id, Section: section.id,
			Kind: kindTool, Relation: relationUsedByCI, License: "MIT", Use: "Used.", Paths: []string{"x"}}
		if section.id == sectionAdapted {
			entry.Artifact, entry.Relation, entry.Detail, entry.Notice = "`x` skill", relationAdapted, "1.0", "© 2026 Example"
		}
		credits.Entries = append(credits.Entries, entry)
	}
	return credits
}

// creditsPage returns a page with a stale table under every section heading, prose around
// each, and a fenced block shaped like a table under the first.
func creditsPage() string {
	var page strings.Builder
	page.WriteString("# Credits\n\nIntro prose.\n\n")
	for index, section := range creditSections {
		page.WriteString("## " + section.heading + "\n\nProse before.\n\n| Old | Table |\n| :-- | :-- |\n| stale | row |\n\nProse after.\n\n")
		if index == 0 {
			page.WriteString("```text\n| not | a table |\n```\n\n")
		}
	}
	return page.String()
}

// Positive: every table is replaced by its rendered rows and every other line is kept; a
// rendered page renders to itself.
func TestRenderCreditsPagePositive(t *testing.T) {
	rendered, err := RenderCreditsPage(creditsPage(), pageCredits())
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"Intro prose.", "Prose before.", "Prose after.", "| not | a table |",
		"| Project | Praetor artifact | Relation | What changed | License |",
		"| [Item adapted](https://example.test/adapted) 1.0 | `x` skill | adapted | Used. | MIT, © 2026 Example |",
		"| Project | Use | Kind and relation | License |",
		"| [Item tooling](https://example.test/tooling) | Used. | tool, used by CI | MIT |",
	} {
		if !strings.Contains(rendered, want) {
			t.Errorf("rendered page lacks %q:\n%s", want, rendered)
		}
	}
	if strings.Contains(rendered, "| stale | row |") {
		t.Fatalf("a stale row survived:\n%s", rendered)
	}
	again, err := RenderCreditsPage(rendered, pageCredits())
	if err != nil || again != rendered {
		t.Fatalf("a rendered page did not render to itself (%v)", err)
	}
}

// Negative: a missing section heading, a section without a table, a second table under one
// heading and a section no entry is listed in are errors naming the page or the list.
func TestRenderCreditsPageNegative(t *testing.T) {
	page := creditsPage()
	missingIntegrations := strings.Replace(page, "## Integrations\n", "## Elsewhere\n", 1)
	noTable := strings.Replace(page, "## Fonts and icons\n\nProse before.\n\n| Old | Table |\n| :-- | :-- |\n| stale | row |\n", "## Fonts and icons\n\nProse before.\n", 1)
	twice := strings.Replace(page, "## GitHub Actions\n\nProse before.\n\n", "## GitHub Actions\n\n| a | b |\n| :-- | :-- |\n\nProse before.\n\n", 1)
	for name, test := range map[string]struct {
		page    string
		credits Credits
		want    string
	}{
		"missing heading": {missingIntegrations, pageCredits(), `no table under a "## Integrations" heading`},
		"no table":        {noTable, pageCredits(), `no table under a "## Fonts and icons" heading`},
		"second table":    {twice, pageCredits(), `the "GitHub Actions" section holds a second table`},
		"empty section":   {page, Credits{Entries: pageCredits().Entries[1:]}, "lists no entry in section adapted"},
	} {
		_, err := RenderCreditsPage(test.page, test.credits)
		if err == nil || !strings.Contains(err.Error(), test.want) {
			t.Errorf("%s: err = %v, want %q", name, err, test.want)
		}
	}
}

// Boundary: a CRLF page renders with LF line ends and the same rows; a table whose delimiter is
// its last line is replaced in full; a level-three heading does not close a section.
func TestRenderCreditsPageBoundary(t *testing.T) {
	lf, err := RenderCreditsPage(creditsPage(), pageCredits())
	if err != nil {
		t.Fatal(err)
	}
	crlf, err := RenderCreditsPage(strings.ReplaceAll(creditsPage(), "\n", "\r\n"), pageCredits())
	if err != nil || crlf != lf {
		t.Fatalf("a CRLF page rendered differently (%v)", err)
	}
	headerOnly := strings.Replace(creditsPage(), "| :-- | :-- |\n| stale | row |\n\nProse after.\n\n## Shipped", "| :-- | :-- |\n\nProse after.\n\n## Shipped", 1)
	rendered, err := RenderCreditsPage(headerOnly, pageCredits())
	if err != nil || !strings.Contains(rendered, "| [Item adapted](https://example.test/adapted)") {
		t.Fatalf("a table without data rows was not filled (%v):\n%s", err, rendered)
	}
	nested := strings.Replace(creditsPage(), "## Integrations\n\nProse before.\n\n", "## Integrations\n\n### Detail\n\nProse before.\n\n", 1)
	if _, err := RenderCreditsPage(nested, pageCredits()); err != nil {
		t.Fatalf("a level-three heading closed its section: %v", err)
	}
}

// The checkout: docs/credits.md is exactly what RenderCreditsPage writes from docs/credits.yaml.
// After a change to the list, PRAETOR_UPDATE_GOLDEN=1 go test ./internal/supplychain -run
// TestCreditsPageIsRendered rewrites the page's tables.
func TestCreditsPageIsRendered(t *testing.T) {
	root := filepath.Join("..", "..")
	credits, err := ReadCredits(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	rendered, err := RenderCreditsPage(string(readRepoFile(t, AcknowledgementsFile)), credits)
	if err != nil {
		t.Fatal(err)
	}
	testsupport.AssertGoldenIn(t, root, AcknowledgementsFile, rendered)
}
