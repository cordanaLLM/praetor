// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package adopt

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func prettierTabRepo(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	for name, text := range files {
		if err := os.WriteFile(filepath.Join(root, name), []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

// Positive (#1246): a declarative Prettier configuration with useTabs: true and no Markdown
// override gets a warning that names its file, MD010 and the override.
func TestPrettierTabWarningNamesTheOverride(t *testing.T) {
	for name, files := range map[string]map[string]string{
		".prettierrc":      {".prettierrc": `{"useTabs": true}`},
		".prettierrc.yaml": {".prettierrc.yaml": "useTabs: true\noverrides:\n  - files: '*.ts'\n    options:\n      useTabs: false\n"},
		"package.json":     {"package.json": `{"name": "x", "prettier": {"useTabs": true}}`},
	} {
		warning, err := prettierTabWarning(t.Context(), prettierTabRepo(t, files))
		if err != nil {
			t.Fatal(err)
		}
		for _, want := range []string{name + " sets Prettier's useTabs: true", "(MD010)", "*.md and *.mdx", "useTabs: false"} {
			if !strings.Contains(warning, want) {
				t.Errorf("%s: warning %q lacks %q", name, warning, want)
			}
		}
	}
}

// Negative: no configuration, spaces, a Markdown override that turns tabs off (string and list
// patterns), and package.json without a prettier key name nothing.
func TestPrettierTabWarningStaysQuiet(t *testing.T) {
	for name, files := range map[string]map[string]string{
		"none":          {},
		"spaces":        {".prettierrc.json": `{"useTabs": false}`},
		"string glob":   {".prettierrc.yml": "useTabs: true\noverrides:\n  - files: '*.md'\n    options:\n      useTabs: false\n"},
		"list glob":     {".prettierrc": `{"useTabs": true, "overrides": [{"files": ["*.ts", "**/*.{md,mdx}"], "options": {"useTabs": false}}]}`},
		"package other": {"package.json": `{"name": "x"}`},
	} {
		warning, err := prettierTabWarning(t.Context(), prettierTabRepo(t, files))
		if err != nil || warning != "" {
			t.Errorf("%s: warning %q, err %v", name, warning, err)
		}
	}
}

// Boundary: Prettier reads the first configuration it finds, package.json before .prettierrc, so a
// package.json key without useTabs wins over a tab-indenting .prettierrc; a configuration that
// does not parse names nothing, since Prettier reports it; an override that keeps tabs for
// Markdown is no override.
func TestPrettierTabWarningBoundaries(t *testing.T) {
	quiet := map[string]map[string]string{
		"first wins": {"package.json": `{"prettier": {"semi": false}}`, ".prettierrc": `{"useTabs": true}`},
		"malformed":  {".prettierrc": "{ useTabs: [true"},
	}
	for name, files := range quiet {
		if warning, err := prettierTabWarning(t.Context(), prettierTabRepo(t, files)); err != nil || warning != "" {
			t.Errorf("%s: warning %q, err %v", name, warning, err)
		}
	}
	kept := map[string]string{".prettierrc": `{"useTabs": true, "overrides": [{"files": "*.md", "options": {"useTabs": true}}]}`}
	if warning, err := prettierTabWarning(t.Context(), prettierTabRepo(t, kept)); err != nil || warning == "" {
		t.Errorf("an override that keeps tabs silenced the warning: %q, %v", warning, err)
	}
}

// Positive through the adoption step: the documentation gate's reconcile step reports the warning
// for a tab-indenting repository, and only there; a repository indenting with spaces gets none.
func TestReconcileDocumentationGateWarnsAboutPrettierTabs(t *testing.T) {
	tabs := docsSession(t, map[string]string{".prettierrc": `{"useTabs": true}`}, AdoptOptions{})
	if err := reconcileDocumentationGate(t.Context(), tabs); err != nil {
		t.Fatal(err)
	}
	if len(tabs.report.Warnings) != 1 || !strings.Contains(tabs.report.Warnings[0], "useTabs: false") {
		t.Fatalf("warnings %q", tabs.report.Warnings)
	}
	spaces := docsSession(t, map[string]string{".prettierrc": `{"useTabs": false}`}, AdoptOptions{})
	if err := reconcileDocumentationGate(t.Context(), spaces); err != nil {
		t.Fatal(err)
	}
	if len(spaces.report.Warnings) != 0 {
		t.Fatalf("a space-indenting repository got warnings %q", spaces.report.Warnings)
	}
}
