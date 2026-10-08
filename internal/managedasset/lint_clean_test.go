// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package managedasset

import (
	"path"
	"slices"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/testsupport"
)

// A locked asset is byte-compared by audit, so an adopter that runs a linter over every tracked
// file cannot fix a finding in one (#842, #845, #578). The linters run in one harness,
// scripts/test_emitted_hook_lint.py, over the files Assets enumerates through
// internal/managedasset/export; this file keeps the checks that need no external tool.

// Boundary: the registry's enumeration reaches the files the issues name and every language the
// harness lints, so a registry change that drops one fails here instead of shrinking the lint
// pass.
func TestAssetsReachTheNamedAssets(t *testing.T) {
	assets, err := Assets()
	if err != nil {
		t.Fatal(err)
	}
	paths := make([]string, 0, len(assets))
	extensions := map[string]bool{}
	for _, asset := range assets {
		paths = append(paths, asset.Path)
		extensions[path.Ext(asset.Path)] = true
		if len(asset.Data) == 0 {
			t.Errorf("%s is enumerated empty", asset.Path)
		}
	}
	for _, rel := range []string{
		"tools/apicompat/gate/main.go",
		"tools/figures/mkdocs_hook.py",
		"tools/figures/third_party/interfig/VENDOR.md",
		".github/workflows/praetor-docs.yml",
	} {
		if !slices.Contains(paths, rel) {
			t.Errorf("the enumeration does not reach %s", rel)
		}
	}
	for _, ext := range []string{".go", ".py", ".yml", ".md"} {
		if !extensions[ext] {
			t.Errorf("the enumeration holds no %s asset", ext)
		}
	}
}

// Negative: the enumeration refuses a path no family owns, and lists each path once.
func TestAssetsListEachManagedPathOnceAndOwnedByItsFamily(t *testing.T) {
	assets, err := Assets()
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, asset := range assets {
		if seen[asset.Path] {
			t.Errorf("%s is enumerated twice", asset.Path)
		}
		seen[asset.Path] = true
	}
	for _, family := range Families() {
		if _, owned, err := family.Canonical("planted/not-managed.txt"); owned || err != nil {
			t.Errorf("%s owns a path outside its directory: owned=%v err=%v", family.Name, owned, err)
		}
	}
}

// Positive: every Markdown asset holds markdownlint's default rules, as far as the in-process
// subset the generators' tests use reaches. The real markdownlint library lints the same bytes
// in make docs-lint, which the repository's own copies of these files go through.
func TestManagedMarkdownHoldsTheDefaultRules(t *testing.T) {
	assets, err := Assets()
	if err != nil {
		t.Fatal(err)
	}
	checked := 0
	for _, asset := range assets {
		if path.Ext(asset.Path) != ".md" {
			continue
		}
		checked++
		for _, finding := range testsupport.MarkdownFindings(string(asset.Data)) {
			t.Errorf("%s: %s", asset.Path, finding)
		}
	}
	if checked == 0 {
		t.Fatal("the registry lists no Markdown asset")
	}
}

// Negative: the subset used above refuses a planted long line, so a clean result is not a
// check that finds nothing (rule 13).
func TestManagedMarkdownCheckRefusesAPlantedDefect(t *testing.T) {
	planted := "# Notes\n\n" + strings.Repeat("word ", 30) + "\n"
	if len(testsupport.MarkdownFindings(planted)) == 0 {
		t.Fatal("the planted long line passed the Markdown default-rule subset")
	}
}
