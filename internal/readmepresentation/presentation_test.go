// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package readmepresentation

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPresentation_Positive_ExemplarPasses(t *testing.T) {
	testDir := filepath.Join("testdata")
	exemplarPath := filepath.Join(testDir, "README-vmafx-2929.md")
	data, err := os.ReadFile(exemplarPath)
	if err != nil {
		t.Fatalf("failed to read exemplar: %v", err)
	}

	findings, err := CheckContent(testDir, "README.md", string(data))
	if err != nil {
		t.Fatalf("CheckContent error: %v", err)
	}
	if len(findings) > 0 {
		for _, f := range findings {
			t.Errorf("unexpected finding: %s", f.String())
		}
		t.Fatalf("exemplar had %d findings, expected 0", len(findings))
	}
}

func readTestExemplar(t *testing.T, testDir string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(testDir, "README-vmafx-2929.md"))
	if err != nil {
		t.Fatalf("failed to read exemplar: %v", err)
	}
	return string(data)
}

func hasRuleID(findings []Finding, id string) bool {
	for _, f := range findings {
		if f.RuleID == id {
			return true
		}
	}
	return false
}

type plantedCase struct {
	name       string
	mutate     func(string) string
	wantRuleID string
}

var plantedTestCases = []plantedCase{
	{
		name: "planted badge block mid-page",
		mutate: func(content string) string {
			return strings.Replace(content, "## 🚀 Get started",
				"## 🚀 Get started\n\n[![Bad](https://img.shields.io/badge/bad-midpage-red?style=for-the-badge)](https://example.com)\n", 1)
		},
		wantRuleID: RuleIDBadgeBlock,
	},
	{
		name: "wrong group order",
		mutate: func(content string) string {
			return strings.Replace(content, "<!-- 2 CI/build -->", "<!-- 4 standards -->", 1)
		},
		wantRuleID: RuleIDBadgeBlock,
	},
	{
		name: "600 px hero",
		mutate: func(content string) string {
			return strings.Replace(content, `height="120"`, `height="600"`, 1)
		},
		wantRuleID: RuleIDHero,
	},
	{
		name: "width 100 percent hero",
		mutate: func(content string) string {
			return strings.Replace(content, `height="120"`, `height="120" width="100%"`, 1)
		},
		wantRuleID: RuleIDHero,
	},
	{
		name: "duplicate H2",
		mutate: func(content string) string {
			return strings.Replace(content, "## 🚀 Get started", "## 🚀 Get started\n\n## 🚀 Get started", 1)
		},
		wantRuleID: RuleIDSections,
	},
	{
		name: "unthemed mermaid",
		mutate: func(content string) string {
			return strings.Replace(content, "%%{init: {\"theme\": \"base\"", "%%{init: {\"theme\": \"default\"", 1)
		},
		wantRuleID: RuleIDDiagrams,
	},
	{
		name: "wide svg figure",
		mutate: func(content string) string {
			return strings.Replace(content, "## 🚀 Get started",
				"## 🚀 Get started\n\n![Wide](docs/assets/wide-figure.svg)\n", 1)
		},
		wantRuleID: RuleIDDiagrams,
	},
	{
		name: "sections out of order",
		mutate: func(content string) string {
			return strings.Replace(content, "## 🤝 Contribute",
				"## 🤝 Contribute\n\n## 🚀 Get started later", 1)
		},
		wantRuleID: RuleIDSections,
	},
	{
		name: "non bold tagline",
		mutate: func(content string) string {
			return strings.Replace(content,
				"**Perceptual video quality assessment — GPU-accelerated, SIMD-tuned, numerically exact**",
				"Plain unbolded tagline message here", 1)
		},
		wantRuleID: RuleIDTagline,
	},
	{
		name: "no badge block",
		mutate: func(_ string) string {
			return "# widgets\n"
		},
		wantRuleID: RuleIDBadgeBlock,
	},
}

func TestPresentation_Negative_PlantedCasesRefused(t *testing.T) {
	testDir := filepath.Join("testdata")
	exemplar := readTestExemplar(t, testDir)

	for _, tc := range plantedTestCases {
		t.Run(tc.name, func(t *testing.T) {
			mutated := tc.mutate(exemplar)
			findings, err := CheckContent(testDir, "README.md", mutated)
			if err != nil {
				t.Fatalf("CheckContent unexpected error: %v", err)
			}
			if !hasRuleID(findings, tc.wantRuleID) {
				t.Errorf("expected finding with RuleID %q, got: %v", tc.wantRuleID, findings)
			}
		})
	}
	t.Run("governance block not last section emits info line and zero counted findings", func(t *testing.T) {
		mutated := exemplar + "\n\n## Trailing Section After Governance\n\nContent here.\n\n## Second Trailing\n"
		findings, infos, err := CheckContentWithInfo(testDir, "README.md", mutated)
		if err != nil {
			t.Fatalf("CheckContentWithInfo unexpected error: %v", err)
		}
		if len(findings) != 0 {
			t.Errorf("expected 0 counted findings for governance block position, got: %v", findings)
		}
		if len(infos) != 1 || infos[0].RuleID != RuleIDSections {
			t.Errorf("expected 1 README-05 info line for governance block position, got: %v", infos)
		}
	})
}

func TestPresentation_Negative_AbsentReadme(t *testing.T) {
	tempDir := t.TempDir()
	findings, err := CheckFile(t.Context(), tempDir, "README.md")
	if err != nil {
		t.Fatalf("unexpected CheckFile error: %v", err)
	}
	if len(findings) != 1 || findings[0].RuleID != RuleIDAbsent {
		t.Fatalf("expected 1 finding with RuleID %q, got: %v", RuleIDAbsent, findings)
	}
}

func TestPresentation_Boundary_HeroHeightLimits(t *testing.T) {
	testDir := filepath.Join("testdata")
	exemplar := readTestExemplar(t, testDir)

	t.Run("height 120 passes", func(t *testing.T) {
		content := strings.Replace(exemplar, `height="120"`, `height="120"`, 1)
		findings, err := CheckContent(testDir, "README.md", content)
		if err != nil {
			t.Fatalf("CheckContent error: %v", err)
		}
		if hasRuleID(findings, RuleIDHero) {
			t.Fatalf("unexpected hero finding for height 120")
		}
	})

	t.Run("height 121 refused", func(t *testing.T) {
		content := strings.Replace(exemplar, `height="120"`, `height="121"`, 1)
		findings, err := CheckContent(testDir, "README.md", content)
		if err != nil {
			t.Fatalf("CheckContent error: %v", err)
		}
		if !hasRuleID(findings, RuleIDHero) {
			t.Fatalf("expected README-03 finding for height 121, got: %v", findings)
		}
	})
}

func TestPresentation_Boundary_SvgWidthLimits(t *testing.T) {
	testDir := filepath.Join("testdata")
	exemplar := readTestExemplar(t, testDir)

	t.Run("width 830 passes", func(t *testing.T) {
		content := strings.Replace(exemplar, "## 🚀 Get started",
			"## 🚀 Get started\n\n![Valid](docs/assets/valid-figure.svg)\n", 1)
		findings, err := CheckContent(testDir, "README.md", content)
		if err != nil {
			t.Fatalf("CheckContent error: %v", err)
		}
		if hasRuleID(findings, RuleIDDiagrams) {
			t.Fatalf("unexpected diagram finding for SVG width 830")
		}
	})

	t.Run("width 831 refused", func(t *testing.T) {
		tmp := t.TempDir()
		svg831 := `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 831 200"><text>test</text></svg>`
		if err := os.WriteFile(filepath.Join(tmp, "b831.svg"), []byte(svg831), 0o600); err != nil {
			t.Fatal(err)
		}
		content := strings.Replace(exemplar, "## 🚀 Get started",
			"## 🚀 Get started\n\n![Boundary](b831.svg)\n", 1)
		findings, err := CheckContent(tmp, "README.md", content)
		if err != nil {
			t.Fatalf("CheckContent error: %v", err)
		}
		if !hasRuleID(findings, RuleIDDiagrams) {
			t.Fatalf("expected README-04 finding for SVG width 831, got: %v", findings)
		}
	})

	t.Run("width 1000 refused", func(t *testing.T) {
		content := strings.Replace(exemplar, "## 🚀 Get started",
			"## 🚀 Get started\n\n![Wide](docs/assets/wide-figure.svg)\n", 1)
		findings, err := CheckContent(testDir, "README.md", content)
		if err != nil {
			t.Fatalf("CheckContent error: %v", err)
		}
		if !hasRuleID(findings, RuleIDDiagrams) {
			t.Fatalf("expected README-04 finding for wide SVG, got: %v", findings)
		}
	})
}

func TestPresentation_Positive_EdgeCasesPass(t *testing.T) {
	testDir := filepath.Join("testdata")
	exemplar := readTestExemplar(t, testDir)

	cases := map[string]string{
		"badge in code fence and comment": exemplar + "\n```md\n[![x](https://img.shields.io/badge/a-b-red?style=for-the-badge)](https://x)\n<!-- praetor:readme-governance:start -->\n```\n<!-- style=for-the-badge -->\n",
		"heading in multiline comment and indented code": strings.Replace(exemplar, "## 🚀 Get started",
			"<!--\n## Support\n-->\n\n    ## Support\n\n## 🚀 Get started", 1),
		"whole-word heading phrases": strings.Replace(exemplar, "## 🚀 Get started",
			"## 🚀 Get started\n\n## Supported platforms\n\n## Release notes\n\n## Standards compliance", 1),
		"h1 header ignores body img":              "# proj\n\n[![a](https://img.shields.io/badge/a-b-blue?style=for-the-badge)](https://x)\n\n**Tagline**\n\n## 🚀 Get started\n\n<img src=\"a.png\" width=\"100%\" height=\"600\">\n",
		"nested div and p wrappers":               "<div align=\"center\">\n<div><img src=\"a.png\" data-height=\"999\" height=\"120\"></div>\n<p align=\"center\">\n<!-- 2026 refresh -->\n<!-- circleci -->\n[![a](https://img.shields.io/badge/a-b-blue?style=for-the-badge)](https://x)\n</p>\n</div>\n\n---\n\n<p align=\"center\"><b>Tagline</b></p>\n",
		"mermaid blank line and yaml frontmatter": exemplar + "\n```mermaid\n\n%%{init: {'theme': 'base', 'themeVariables': {'primaryColor': '#111'}}}%%\ngraph LR\nA-->B\n```\n```mermaid\n---\nconfig:\n  theme: base\n  themeVariables:\n    primaryColor: \"#111\"\n---\ngraph LR\nA-->B\n```\n",
		"svg with title and remote svg skipped": strings.Replace(exemplar, "## 🚀 Get started",
			"## 🚀 Get started\n\n![Valid](docs/assets/valid-figure.svg \"title\")\n![Remote](https://example.com/x.svg)\n![CDN](//cdn.example.com/x.svg)\n", 1),
		"crlf exemplar": strings.ReplaceAll(exemplar+strings.Repeat("\n", 2600), "\n", "\r\n"),
	}
	for name, content := range cases {
		t.Run(name, func(t *testing.T) {
			findings, err := CheckContent(testDir, "README.md", content)
			if err != nil {
				t.Fatalf("CheckContent error: %v", err)
			}
			if len(findings) != 0 {
				t.Fatalf("expected 0 findings for %s, got: %v", name, findings)
			}
		})
	}
}

var plantedEdgeDefectCases = []plantedCase{
	{"multiline hero img", func(_ string) string {
		return "<div align=\"center\">\n<img\n  src=\"a.png\"\n  height=\"600\"\n>\n[![a](https://img.shields.io/badge/a-b-blue?style=for-the-badge)](https://x)\n</div>\n\n**Tag**\n"
	}, RuleIDHero},
	{"second img on same line", func(_ string) string {
		return "<div align=\"center\">\n<img src=\"a.png\" height=\"80\"> <img src=\"b.png\" height=\"600\">\n[![a](https://img.shields.io/badge/a-b-blue?style=for-the-badge)](https://x)\n</div>\n\n**Tag**\n"
	}, RuleIDHero},
	{"style height 121px", func(c string) string {
		return strings.Replace(c, `height="120"`, `style="height:121px"`, 1)
	}, RuleIDHero},
	{"named badge groups out of order", func(_ string) string {
		return "<div align=\"center\">\n<!-- security -->\n[![a](https://img.shields.io/badge/a-b-blue?style=for-the-badge)](https://x)\n<!-- release -->\n[![b](https://img.shields.io/badge/c-d-blue?style=for-the-badge)](https://x)\n</div>\n\n**Tag**\n"
	}, RuleIDBadgeBlock},
	{"mermaid dark theme with database word", func(c string) string {
		return c + "\n```mermaid\n%%{init: {'theme': 'dark', 'themeVariables': {'note': 'database'}}}%%\ngraph LR\nA-->B\n```\n"
	}, RuleIDDiagrams},
	{"mermaid missing themeVariables", func(c string) string {
		return c + "\n```mermaid\n%%{init: {'theme': 'base'}}%%\ngraph LR\nA-->B\n```\n"
	}, RuleIDDiagrams},
	{"mermaid missing init directive with theme keywords", func(c string) string {
		return c + "\n```mermaid\ngraph LR %% 'theme': 'base' themeVariables\nA-->B\n```\n"
	}, RuleIDDiagrams},
	{"tilde mermaid fence", func(c string) string {
		return c + "\n~~~mermaid\ngraph LR\nA-->B\n~~~\n"
	}, RuleIDDiagrams},
	{"brace mermaid fence", func(c string) string {
		return c + "\n```{mermaid}\ngraph LR\nA-->B\n```\n"
	}, RuleIDDiagrams},
	{"blockquote mermaid fence", func(c string) string {
		return c + "\n> ```mermaid\n> graph LR\n> A-->B\n> ```\n"
	}, RuleIDDiagrams},
	{"unterminated mermaid fence", func(c string) string {
		return c + "\n```mermaid\ngraph LR\nA-->B\n"
	}, RuleIDDiagrams},
	{"html img wide svg", func(c string) string {
		return c + "\n<img src=\"docs/assets/wide-figure.svg\">\n"
	}, RuleIDDiagrams},
	{"unreadable svg link", func(c string) string {
		return c + "\n![Missing](docs/assets/missing.svg)\n"
	}, RuleIDDiagrams},
	{"parent traversal svg link", func(c string) string {
		return c + "\n![Escape](../outside.svg)\n"
	}, RuleIDDiagrams},
	{"tagline beyond 50 lines", func(_ string) string {
		return "# proj\n\n[![a](https://img.shields.io/badge/a-b-blue?style=for-the-badge)](https://x)\n" + strings.Repeat("\n", 55) + "**Too far**\n"
	}, RuleIDTagline},
}

func TestPresentation_Negative_PlantedEdgeDefectsRefused(t *testing.T) {
	testDir := filepath.Join("testdata")
	exemplar := readTestExemplar(t, testDir)
	for _, tc := range plantedEdgeDefectCases {
		t.Run(tc.name, func(t *testing.T) {
			findings, err := CheckContent(testDir, "README.md", tc.mutate(exemplar))
			if err != nil {
				t.Fatalf("CheckContent error: %v", err)
			}
			if !hasRuleID(findings, tc.wantRuleID) {
				t.Fatalf("expected %s finding for %s, got: %v", tc.wantRuleID, tc.name, findings)
			}
		})
	}
}

func TestPresentation_Boundary_LineCapAndControlCharScrub(t *testing.T) {
	testDir := filepath.Join("testdata")
	base := "# proj\n[![a](https://img.shields.io/badge/a-b-blue?style=for-the-badge)](https://x)\n**Tag**\n"
	lines5000 := base + strings.Repeat("line\n", 5000-4)
	if findings, err := CheckContent(testDir, "README.md", lines5000); err != nil || hasRuleID(findings, RuleIDSections) {
		t.Fatalf("5000 lines should pass, got findings=%v err=%v", findings, err)
	}
	lines5001 := base + strings.Repeat("line\n", 5001-3)
	if findings, err := CheckContent(testDir, "README.md", lines5001); err != nil || !hasRuleID(findings, RuleIDSections) {
		t.Fatalf("5001 lines should report README-05, got findings=%v err=%v", findings, err)
	}
	escFindings, err := CheckContent(testDir, "README.md", base+"\n![esc](\x1b[31mred.svg)\n")
	if err != nil || len(escFindings) == 0 || strings.Contains(escFindings[0].Message, "\x1b") {
		t.Fatalf("expected scrubbed ESC in finding message, got: %v (err=%v)", escFindings, err)
	}
}

func TestPresentation_Smoke_ExemplarAndRepoReadme(t *testing.T) {
	testDir := filepath.Join("testdata")
	exemplar := readTestExemplar(t, testDir)
	exFindings, exInfos, err := CheckContentWithInfo(testDir, "README.md", exemplar)
	if err != nil || len(exFindings) != 0 || len(exInfos) != 0 {
		t.Fatalf("exemplar smoke: findings=%d infos=%d err=%v", len(exFindings), len(exInfos), err)
	}
	repoRoot := filepath.Join("..", "..")
	repoFindings, repoInfos, err := CheckFileWithInfo(t.Context(), repoRoot, "README.md")
	if err != nil {
		t.Fatalf("repo README smoke error: %v", err)
	}
	t.Logf("smoke counts: exemplar findings=%d infos=%d; repo README.md findings=%d infos=%d",
		len(exFindings), len(exInfos), len(repoFindings), len(repoInfos))
}
