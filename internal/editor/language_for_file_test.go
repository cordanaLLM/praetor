// SPDX-License-Identifier: EUPL-1.2
// SPDX-FileCopyrightText: 2026 Lusoris <lusoris@proton.me>

package editor

import "testing"

// languageForFile reads programming languages from util.SourceLanguage, the table the dedupe
// scope shares, and keeps the editor's own mapping on top: JavaScript is served by the
// TypeScript tooling, and configuration, documentation and make files are languages here.
func TestLanguageForFile(t *testing.T) {
	cases := map[string]string{
		"main.go":      "go", // positive: a shared source language passes through
		"lib.rs":       "rust",
		"index.js":     "typescript", // positive: JavaScript maps onto the TypeScript tooling
		"view.jsx":     "typescript",
		"build.mjs":    "typescript", // positive: module JavaScript and TypeScript use the same tooling
		"worker.mts":   "typescript",
		"Main.java":    "java", // positive: a language with no editor tooling is still observed
		"values.yml":   "yaml",
		"README.mdx":   "markdown",
		"Makefile":     "make",
		"rules.mk":     "make",
		"LICENSE":      "", // negative: no language
		"image.png":    "",
		"MAIN.GO":      "go", // boundary: extension case is ignored, as before
		"notes.md.txt": "",
	}
	for name, want := range cases {
		if got := languageForFile(name); got != want {
			t.Errorf("languageForFile(%q) = %q, want %q", name, got, want)
		}
	}
}
