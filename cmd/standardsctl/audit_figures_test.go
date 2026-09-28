// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/adopt"
	"github.com/cordanaLLM/praetor/internal/config"
	"github.com/cordanaLLM/praetor/internal/testsupport"
	figureassets "github.com/cordanaLLM/praetor/tools/figures"
)

// The figure engine joined docs:seo-portal (docs/adr/0016-figures-for-adopters.md, section 5):
// audit locks its bytes through the registry, requires the docs-figures target and the
// .gitattributes block, and warns about a REUSE.toml that relabels the vendored interfig files.
// This golden pins the report and every failure text of those checks.

// reuseWithoutOverride labels the whole tree under one license, as most REUSE.toml files do.
const reuseWithoutOverride = "version = 1\n\n[[annotations]]\npath = [\"**\"]\nSPDX-License-Identifier = \"Apache-2.0\"\n"

// reuseWithOverride adds the override annotation tools/figures/README.md asks for.
const reuseWithOverride = reuseWithoutOverride + "\n[[annotations]]\npath = [\"tools/figures/third_party/interfig/upstream/**\"]\n" +
	"precedence = \"override\"\nSPDX-FileCopyrightText = \"2025 Vectorize AI, Inc.\"\nSPDX-License-Identifier = \"MIT\"\n"

func figureAuditGoldenCases() []auditGoldenCase {
	block := adopt.ManagedGitAttributesBlock(adopt.DocumentationAttributes())
	return []auditGoldenCase{
		{name: "canonical without REUSE.toml", enabled: true},
		{name: "canonical, REUSE.toml without the override", enabled: true, mutate: func(t *testing.T, root string) {
			writeFixtureFile(t, root, "REUSE.toml", reuseWithoutOverride)
		}},
		{name: "canonical, REUSE.toml with the override", enabled: true, mutate: func(t *testing.T, root string) {
			writeFixtureFile(t, root, "REUSE.toml", reuseWithOverride)
		}},
		{name: "figure asset drift", enabled: true, mutate: func(t *testing.T, root string) {
			writeFixtureFile(t, root, "tools/figures/build.mjs", "// operator edit\n")
		}},
		{name: "vendored file missing", enabled: true, mutate: func(t *testing.T, root string) {
			if err := os.Remove(filepath.Join(root, "tools", "figures", "third_party", "interfig", "upstream", "LICENSE")); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "Makefile block without docs-figures", enabled: true, mutate: func(t *testing.T, root string) {
			writeFixtureFile(t, root, "Makefile", "# BEGIN praetor documentation gate\n.PHONY: docs-lint\nverify-all: docs-lint\n"+
				"docs-lint:\n\t@node tools/markdownlint/verify.mjs\n# END praetor documentation gate\n")
		}},
		{name: "attribute block missing", enabled: true, mutate: func(t *testing.T, root string) {
			writeFixtureFile(t, root, ".gitattributes", "* text=auto\n")
		}},
		{name: "attribute block not at the tail", enabled: true, mutate: func(t *testing.T, root string) {
			writeFixtureFile(t, root, ".gitattributes", block+"tools/** -text\n")
		}},
		{name: "attribute block twice", enabled: true, mutate: func(t *testing.T, root string) {
			writeFixtureFile(t, root, ".gitattributes", "* text=auto\n\n"+block+block)
		}},
		{name: "canonical, REUSE.toml override before the whole-tree table", enabled: true, mutate: func(t *testing.T, root string) {
			writeFixtureFile(t, root, "REUSE.toml", "version = 1\n"+strings.TrimPrefix(reuseWithOverride, reuseWithoutOverride)+
				strings.TrimPrefix(reuseWithoutOverride, "version = 1\n"))
		}},
		{name: "attribute block CRLF", enabled: true, mutate: func(t *testing.T, root string) {
			writeFixtureFile(t, root, ".gitattributes", strings.ReplaceAll("* text=auto\n\n"+block, "\n", "\r\n"))
		}},
		{name: "disabled retains figure asset", mutate: func(t *testing.T, root string) {
			data, err := figureassets.Read("dist/player.js")
			if err != nil {
				t.Fatal(err)
			}
			writeFixtureFile(t, root, "tools/figures/dist/player.js", string(data))
		}},
		{name: "disabled retains attribute block", mutate: func(t *testing.T, root string) {
			writeFixtureFile(t, root, ".gitattributes", "* text=auto\n\n"+block)
		}},
		{name: "disabled with ambiguous attribute markers", mutate: func(t *testing.T, root string) {
			writeFixtureFile(t, root, ".gitattributes", "# END praetor managed attributes\n")
		}},
		{name: "disabled with the operator's own figure rules", mutate: func(t *testing.T, root string) {
			writeFixtureFile(t, root, ".gitattributes", "tools/figures/** text eol=lf\n")
		}},
	}
}

// Positive, negative and boundary: the canonical engine passes with the warning only when a
// REUSE.toml lacks the override or places it before the whole-tree table; a drifted or missing
// engine file, the Markdown-only Makefile block and a missing, misplaced, doubled or
// disabled-but-retained attribute block fail with their remedy; CRLF attribute text and the
// operator's own rules pass.
func TestAuditFigureFamilyGolden(t *testing.T) {
	var sb strings.Builder
	for _, test := range figureAuditGoldenCases() {
		root := t.TempDir()
		manifest := &config.Manifest{}
		if test.enabled {
			root = documentationAuditFixture(t)
			manifest.Facets = []string{"docs:seo-portal"}
		}
		if test.mutate != nil {
			test.mutate(t, root)
		}
		stdout, err := captureStdout(t, func() error { return docGate(t.Context(), manifest, root) })
		fmt.Fprintf(&sb, "== %s\nstdout %q\n", test.name, stdout)
		if err != nil {
			fmt.Fprintf(&sb, "error %s\n", portableAuditError(err.Error()))
		}
	}
	testsupport.AssertGolden(t, filepath.Join("testdata", "managed-family", "figure-audit.golden"), sb.String())
}
