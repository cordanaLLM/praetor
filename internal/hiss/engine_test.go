// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package hiss

import (
	"math"
	"strings"
	"testing"
)

// Positive (#589): every JavaScript, TypeScript and Svelte extension has a scanner, in any
// case, and SupportsExtension answers from the dispatch table Scan uses.
func TestSupportsExtension_ScriptLanguages(t *testing.T) {
	for _, ext := range []string{".js", ".jsx", ".mjs", ".cjs", ".ts", ".tsx", ".mts", ".cts", ".svelte", ".TS", ".Svelte"} {
		if !SupportsExtension(ext) {
			t.Errorf("%q is script source but SupportsExtension rejected it", ext)
		}
	}
}

// Boundary: no two language scanners claim one extension, so the order of the dispatch table
// can never decide which rules a file gets.
func TestLanguageScanners_ClaimDisjointExtensions(t *testing.T) {
	exts := []string{".go", ".py", ".rs", ".c", ".h", ".cc", ".cpp", ".cxx", ".hpp", ".hh", ".cu", ".hip",
		".js", ".jsx", ".mjs", ".cjs", ".ts", ".tsx", ".mts", ".cts", ".svelte", ".vue", ".sh", ".java"}
	for _, ext := range exts {
		claimed := 0
		for i := 0; i < len(languageScanners); i++ {
			if languageScanners[i].handles(ext) {
				claimed++
			}
		}
		if claimed > 1 {
			t.Errorf("%s is claimed by %d scanners", ext, claimed)
		}
	}
}

// Positive: the coverage record names the languages it read and the source languages it could
// not, and the evidence line says so; documentation and configuration are not source.
func TestScanCoverage_RecordsLanguages(t *testing.T) {
	root := t.TempDir()
	for path, data := range map[string]string{
		"main.go": "package main\n", "ui/app.ts": "export const a = 1;\n", "ui/App.svelte": "<script>let a = 1;</script>\n",
		"deploy.sh": "echo hi\n", "Tool.java": "class Tool {}\n", "README.md": "docs\n",
	} {
		writeFixture(t, root, path, data)
	}
	rep := scanFixture(t, root, ScanOptions{})
	c := rep.Coverage
	if c.FilesRead != 3 || c.LanguagesRead["go"] != 1 || c.LanguagesRead["typescript"] != 1 || c.LanguagesRead["svelte"] != 1 {
		t.Errorf("languages read differ: %+v", c)
	}
	if c.UnscannedFiles != 3 || c.UnscannedLanguages["shell"] != 1 || c.UnscannedLanguages["java"] != 1 || len(c.UnscannedLanguages) != 2 {
		t.Errorf("unscanned source languages differ: %+v", c)
	}
	if got := c.UnscannedSourceSummary(); got != "java (1 file), shell (1 file)" {
		t.Errorf("summary = %q", got)
	}
	if got := strings.Join(c.ScannedLanguageNames(), ","); got != "go,svelte,typescript" {
		t.Errorf("scanned names = %q", got)
	}
	if !strings.Contains(rep.CoverageEvidence(), "Source no HISS scanner examined: java (1 file), shell (1 file).") {
		t.Errorf("evidence hides the unscanned source: %s", rep.CoverageEvidence())
	}
	if err := c.Validate(); err != nil {
		t.Errorf("recorded coverage fails its own validation: %v", err)
	}
}

// Negative: an all-source-scanned tree reports no unscanned language, and nil coverage (a
// retained report from before languages were recorded) reports none rather than guessing.
func TestScanCoverage_NoUnscannedSource(t *testing.T) {
	rep := scanFixtureFile(t, "a.py", "x = 1\n")
	if rep.Coverage.UnscannedSourceSummary() != "" || strings.Contains(rep.CoverageEvidence(), "Source no HISS") {
		t.Errorf("fully scanned tree reports unscanned source: %s", rep.CoverageEvidence())
	}
	var unknown *ScanCoverage
	if unknown.UnscannedSourceSummary() != "" || unknown.ScannedLanguageNames() != nil {
		t.Error("nil coverage must report no languages")
	}
}

// Negative and boundary: per-language counts may cover fewer files than their total but never
// more, and each map is bounded.
func TestScanCoverageValidate_LanguageCounts(t *testing.T) {
	for name, c := range map[string]ScanCoverage{
		"read exceeds total":      {FilesRead: 1, LanguagesRead: map[string]int{"go": 2}},
		"unscanned exceeds total": {UnscannedFiles: 1, UnscannedByExtension: map[string]int{".sh": 1}, UnscannedLanguages: map[string]int{"shell": 2}},
		"negative count":          {FilesRead: 1, LanguagesRead: map[string]int{"go": -1}},
		"empty name":              {FilesRead: 1, LanguagesRead: map[string]int{"": 1}},
		"sum overflow":            {FilesRead: 1, LanguagesRead: map[string]int{"go": math.MaxInt, "rust": math.MaxInt}},
	} {
		if err := c.Validate(); err == nil {
			t.Errorf("%s: contradictory language counts accepted: %+v", name, c)
		}
	}
	languages := make(map[string]int)
	for i := 0; i < maxCoverageLanguages; i++ {
		languages["l"+strings.Repeat("x", i%8)+string(rune('a'+i%26))+string(rune('a'+i/26))] = 1
	}
	at := ScanCoverage{FilesRead: len(languages), LanguagesRead: languages}
	if err := at.Validate(); err != nil {
		t.Errorf("exactly %d languages rejected: %v", maxCoverageLanguages, err)
	}
	languages["overflow"] = 1
	at.FilesRead++
	if err := at.Validate(); err == nil {
		t.Error("language map above its bound accepted")
	}
	under := ScanCoverage{FilesRead: 5, LanguagesRead: map[string]int{"go": 2}}
	if err := under.Validate(); err != nil {
		t.Errorf("a partial language breakdown is valid: %v", err)
	}
}

// Boundary: addLanguage lists no language past maxCoverageLanguages, yet keeps counting the
// languages already listed.
func TestAddLanguage_Bound(t *testing.T) {
	var counts map[string]int
	for i := 0; i < maxCoverageLanguages+3; i++ {
		counts = addLanguage(counts, "l"+string(rune('a'+i%26))+string(rune('a'+i/26)))
	}
	counts = addLanguage(counts, "laa")
	if len(counts) != maxCoverageLanguages || counts["laa"] != 2 {
		t.Errorf("bound not kept: %d entries, laa=%d", len(counts), counts["laa"])
	}
}
