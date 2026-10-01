// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/adopt"
	"github.com/cordanaLLM/praetor/internal/managedasset"
	"github.com/cordanaLLM/praetor/internal/supplychain"
)

// reuseWarnings runs the vendored license check of the documentation families over a repository
// whose REUSE.toml is text; a nil text writes no REUSE.toml.
func reuseWarnings(t *testing.T, text *string) []string {
	t.Helper()
	root := t.TempDir()
	if text != nil {
		writeFixtureFile(t, root, supplychain.ReuseFile, *text)
	}
	return vendoredLicenseWarnings(t.Context(), root, adopt.DocumentationFamilies())
}

// reuseOverrideTable is the override annotation of reuseWithOverride on its own.
const reuseOverrideTable = "\n[[annotations]]\npath = [\"tools/figures/third_party/interfig/upstream/**\"]\n" +
	"precedence = \"override\"\nSPDX-License-Identifier = \"MIT\"\n"

// Positive: an override annotation naming the vendored tree MIT, alone or in an expression, in
// double or single quotes, on one line or in a multi-line array, satisfies the check, as it does
// followed by a table for paths that do not cover the vendored tree; a repository without
// REUSE.toml gets no warning.
func TestVendoredLicenseWarningsPositive(t *testing.T) {
	variants := []string{
		reuseWithOverride,
		reuseWithOverride + "\n[[annotations]]\npath = [\"docs/**\", \"tools/figures/dist/**\"]\nSPDX-License-Identifier = \"CC-BY-4.0\"\n",
		strings.Replace(reuseWithOverride, `SPDX-License-Identifier = "MIT"`, `SPDX-License-Identifier = "EUPL-1.2 AND (MIT)"`, 1),
		strings.Replace(reuseWithOverride, `"tools/figures/third_party/interfig/upstream/**"`, `'tools/figures/third_party/interfig/upstream/**'`, 1),
		strings.Replace(reuseWithOverride, `path = ["tools/figures/third_party/interfig/upstream/**"]`,
			"path = [\n  \"tools/figures/dist/**\",\n  \"tools/figures/third_party/interfig/upstream/**\",\n]", 1),
		strings.ReplaceAll(reuseWithOverride, "\n", "\r\n"),
	}
	for _, text := range variants {
		if warnings := reuseWarnings(t, &text); len(warnings) != 0 {
			t.Fatalf("REUSE.toml %q warned: %v", text, warnings)
		}
	}
	if warnings := reuseWarnings(t, nil); len(warnings) != 0 {
		t.Fatalf("a repository without REUSE.toml warned: %v", warnings)
	}
}

// Negative: the whole-tree table alone, an annotation naming the tree under another license, a
// license in another table than the path, and the path only in a comment each warn, naming the
// glob, the license and the remedy.
func TestVendoredLicenseWarningsNegative(t *testing.T) {
	variants := map[string]string{
		"whole tree only": reuseWithoutOverride,
		"other license":   strings.Replace(reuseWithOverride, `SPDX-License-Identifier = "MIT"`, `SPDX-License-Identifier = "EUPL-1.2"`, 1),
		"license elsewhere": reuseWithoutOverride + "\n[[annotations]]\npath = [\"tools/figures/third_party/interfig/upstream/**\"]\n" +
			"\n[[annotations]]\npath = [\"other/**\"]\nSPDX-License-Identifier = \"MIT\"\n",
		"path in a comment":  reuseWithoutOverride + "# \"tools/figures/third_party/interfig/upstream/**\" is MIT\n",
		"MIT as a substring": strings.Replace(reuseWithOverride, `SPDX-License-Identifier = "MIT"`, `SPDX-License-Identifier = "MIT-0"`, 1),
		// REUSE 3.3 applies only the last matching table, so a later covering table relabels.
		"override before the whole tree":   "version = 1\n" + reuseOverrideTable + strings.TrimPrefix(reuseWithoutOverride, "version = 1\n"),
		"override before tools/figures/**": reuseWithOverride + "\n[[annotations]]\npath = ['tools/figures/**']\nSPDX-License-Identifier = \"Apache-2.0\"\n",
	}
	for name, text := range variants {
		warnings := reuseWarnings(t, &text)
		if len(warnings) != 1 || !strings.Contains(warnings[0], "labelling tools/figures/third_party/interfig/upstream/** MIT") ||
			!strings.Contains(warnings[0], "tools/figures/README.md") {
			t.Fatalf("%s: warnings = %v", name, warnings)
		}
	}
}

// Boundary: the nearest ancestor glob of the vendored tree covers it and relabels it from a
// later table, while a later table quoting the vendored glob again with MIT is the one applied;
// a REUSE.toml at the line bound is read and one past it warns that it was not; a REUSE.toml
// that is not a regular file warns that it cannot be read; families that vendor nothing read no
// REUSE.toml at all.
func TestVendoredLicenseWarningsBoundary(t *testing.T) {
	nearest := reuseWithOverride + "\n[[annotations]]\npath = [\"tools/figures/third_party/interfig/**\"]\nSPDX-License-Identifier = \"Apache-2.0\"\n"
	if warnings := reuseWarnings(t, &nearest); len(warnings) != 1 {
		t.Fatalf("a later table for the nearest ancestor did not warn: %v", warnings)
	}
	again := nearest + reuseOverrideTable
	if warnings := reuseWarnings(t, &again); len(warnings) != 0 {
		t.Fatalf("the override repeated after the ancestor table warned: %v", warnings)
	}
	atBound := reuseWithOverride + strings.Repeat("\n", supplychain.MaxReuseLines-strings.Count(reuseWithOverride, "\n")-1)
	if warnings := reuseWarnings(t, &atBound); len(warnings) != 0 {
		t.Fatalf("a REUSE.toml at the line bound warned: %v", warnings)
	}
	past := atBound + "\n"
	if warnings := reuseWarnings(t, &past); len(warnings) != 1 || !strings.Contains(warnings[0], "more than the 4096") {
		t.Fatalf("a REUSE.toml past the line bound: %v", warnings)
	}
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, supplychain.ReuseFile), 0o755); err != nil {
		t.Fatal(err)
	}
	families := adopt.DocumentationFamilies()
	if warnings := vendoredLicenseWarnings(t.Context(), root, families); len(warnings) != 1 || !strings.Contains(warnings[0], "cannot be read") {
		t.Fatalf("an unreadable REUSE.toml: %v", warnings)
	}
	if warnings := vendoredLicenseWarnings(t.Context(), root, []managedasset.Family{families[0]}); len(warnings) != 0 {
		t.Fatalf("a family that vendors nothing read REUSE.toml: %v", warnings)
	}
}
