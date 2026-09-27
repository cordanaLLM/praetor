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
)

// The Markdown gate's audit was moved onto the managed asset family registry as a pure
// refactor. This golden was recorded from the audit before that change and pins its report
// line and every failure text for the family's assets and workflow.

type auditGoldenCase struct {
	name    string
	enabled bool
	mutate  func(*testing.T, string)
}

func auditFamilyGoldenCases() []auditGoldenCase {
	asset := "tools/markdownlint/package.json"
	return []auditGoldenCase{
		{name: "canonical", enabled: true},
		{name: "canonical CRLF", enabled: true, mutate: func(t *testing.T, root string) {
			for _, rel := range []string{asset, adopt.DocumentationWorkflowFile, "tools/markdownlint/verify.mjs"} {
				rewriteFixture(t, root, rel, func(text string) string { return strings.ReplaceAll(text, "\n", "\r\n") })
			}
		}},
		{name: "asset drift", enabled: true, mutate: func(t *testing.T, root string) {
			writeFixtureFile(t, root, asset, "{}\n")
		}},
		{name: "asset missing", enabled: true, mutate: func(t *testing.T, root string) {
			if err := os.Remove(filepath.Join(root, filepath.FromSlash(asset))); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "asset mixed endings", enabled: true, mutate: func(t *testing.T, root string) {
			rewriteFixture(t, root, asset, func(text string) string { return strings.Replace(text, "\n", "\r\n", 1) })
		}},
		{name: "last asset drift", enabled: true, mutate: func(t *testing.T, root string) {
			writeFixtureFile(t, root, "tools/markdownlint/no-private-scratch-links.mjs", "// operator\n")
		}},
		{name: "workflow drift", enabled: true, mutate: func(t *testing.T, root string) {
			writeFixtureFile(t, root, adopt.DocumentationWorkflowFile, "name: incomplete\n")
		}},
		{name: "workflow missing", enabled: true, mutate: func(t *testing.T, root string) {
			if err := os.Remove(filepath.Join(root, filepath.FromSlash(adopt.DocumentationWorkflowFile))); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "ruleset without context", enabled: true, mutate: func(t *testing.T, root string) {
			rewriteFixture(t, root, ".github/rulesets/main.json", func(text string) string {
				return strings.Replace(text, adopt.DocumentationStatusContext, "Different Context", 1)
			})
		}},
		{name: "disabled clean"},
		{name: "disabled retains workflow", mutate: func(t *testing.T, root string) {
			writeFixtureFile(t, root, adopt.DocumentationWorkflowFile, adopt.DocumentationWorkflow())
		}},
		{name: "disabled retains CRLF asset", mutate: func(t *testing.T, root string) {
			data := mustReadFixtureAsset(t, "verify.mjs")
			writeFixtureFile(t, root, "tools/markdownlint/verify.mjs", strings.ReplaceAll(data, "\n", "\r\n"))
		}},
		{name: "disabled mixed-ending lookalike", mutate: func(t *testing.T, root string) {
			writeFixtureFile(t, root, asset, "{\r\n}\n")
		}},
		{name: "disabled retains ruleset context", mutate: func(t *testing.T, root string) {
			writeFixtureFile(t, root, ".github/rulesets/main.json",
				`{"rules":[{"type":"required_status_checks","parameters":{"required_status_checks":[{"context":"`+
					adopt.DocumentationStatusContext+`"}]}}]}`)
		}},
	}
}

func TestAuditMarkdownFamilyGolden(t *testing.T) {
	var sb strings.Builder
	for _, test := range auditFamilyGoldenCases() {
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
	testsupport.AssertGolden(t, filepath.Join("testdata", "managed-family", "markdown-audit.golden"), sb.String())
}

// portableAuditError drops the operating system's wording of a failed read, which differs on
// Windows, and keeps the audit's own text in front of it (HISS-21).
func portableAuditError(message string) string {
	const unreadable = "is missing or unreadable:"
	if before, _, found := strings.Cut(message, unreadable); found {
		return before + unreadable + " <os error>"
	}
	return message
}

func rewriteFixture(t *testing.T, root, rel string, edit func(string) string) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
	if err != nil {
		t.Fatal(err)
	}
	writeFixtureFile(t, root, rel, edit(string(data)))
}

func mustReadFixtureAsset(t *testing.T, name string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", "tools", "markdownlint", name))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}
