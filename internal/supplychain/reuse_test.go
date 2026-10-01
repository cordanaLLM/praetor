// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package supplychain

import (
	"strings"
	"testing"
)

// reuseWholeTree labels every file EUPL-1.2, as this repository's REUSE.toml does.
const reuseWholeTree = "version = 1\n\n[[annotations]]\npath = [\"**\"]\nSPDX-License-Identifier = \"EUPL-1.2\"\n"

// reuseTables parses text or fails the test.
func reuseTables(t *testing.T, text string) []string {
	t.Helper()
	tables, err := ReuseAnnotationTables(text)
	if err != nil {
		t.Fatal(err)
	}
	return tables
}

// Positive: an override after the whole-tree table labels one file or its directory with the
// upstream license, alone or in an expression, and leaves the whole-tree license on the rest.
func TestReuseLabelsPositive(t *testing.T) {
	for _, override := range []string{
		"\n[[annotations]]\npath = [\".agents/skills/x/SKILL.md\"]\nSPDX-License-Identifier = \"MIT\"\n",
		"\n[[annotations]]\npath = ['.agents/skills/x/**']\nSPDX-License-Identifier = \"EUPL-1.2 AND MIT\"\n",
	} {
		tables := reuseTables(t, reuseWholeTree+override)
		if !ReuseLabels(tables, ".agents/skills/x/SKILL.md", "MIT") {
			t.Errorf("override %q does not label the file MIT", override)
		}
		if !ReuseLabels(tables, "docs/index.md", "EUPL-1.2") {
			t.Errorf("override %q relabels an unrelated file", override)
		}
	}
}

// Negative: the whole-tree table alone, an override placed before it, and an override naming
// another license or a license that only contains the term leave the file unlabelled.
func TestReuseLabelsNegative(t *testing.T) {
	override := "\n[[annotations]]\npath = [\".agents/skills/x/SKILL.md\"]\nSPDX-License-Identifier = \"MIT\"\n"
	for name, text := range map[string]string{
		"whole tree only":     reuseWholeTree,
		"override first":      "version = 1\n" + override + strings.TrimPrefix(reuseWholeTree, "version = 1\n"),
		"other license":       reuseWholeTree + strings.Replace(override, `"MIT"`, `"Apache-2.0"`, 1),
		"license as a prefix": reuseWholeTree + strings.Replace(override, `"MIT"`, `"MIT-0"`, 1),
	} {
		if ReuseLabels(reuseTables(t, text), ".agents/skills/x/SKILL.md", "MIT") {
			t.Errorf("%s: the file is labelled MIT", name)
		}
	}
}

// Boundary: the covering globs of a directory glob and of a file, an empty REUSE.toml, and the
// line bound.
func TestReuseAnnotationTablesBoundary(t *testing.T) {
	if got := reuseCoveringGlobs("tools/figures/third_party/interfig/upstream/**"); len(got) != 5 ||
		got[0] != "**" || got[4] != "tools/figures/third_party/interfig/**" {
		t.Fatalf("covering globs = %v", got)
	}
	if got := reuseCoveringGlobs(".agents/skills/x/SKILL.md"); strings.Join(got, " ") != "** .agents/** .agents/skills/** .agents/skills/x/**" {
		t.Fatalf("covering globs of a file = %v", got)
	}
	if tables := reuseTables(t, ""); len(tables) != 0 || ReuseLabels(tables, "a.md", "MIT") {
		t.Fatalf("an empty REUSE.toml labels: %v", tables)
	}
	atBound := strings.Repeat("\n", MaxReuseLines-1)
	if _, err := ReuseAnnotationTables(atBound); err != nil {
		t.Fatalf("a REUSE.toml at the bound: %v", err)
	}
	if _, err := ReuseAnnotationTables(atBound + "\n"); err == nil || !strings.Contains(err.Error(), "more than the 4096") {
		t.Fatalf("a REUSE.toml past the bound: %v", err)
	}
}
