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
		name: "governance block not last section",
		mutate: func(content string) string {
			return content + "\n\n## Trailing Section After Governance\n\nContent here.\n"
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
