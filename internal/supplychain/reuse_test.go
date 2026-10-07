// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package supplychain

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const (
	// reuseWholeTree labels every file EUPL-1.2, as this repository's REUSE.toml does.
	reuseWholeTree = "version = 1\n\n[[annotations]]\npath = [\"**\"]\nSPDX-License-Identifier = \"EUPL-1.2\"\n"
	// reuseVendorMIT labels a vendored tree MIT; its comment quotes the whole-tree glob, as the
	// interfig table of this repository's REUSE.toml does.
	reuseVendorMIT = "\n# The vendored tree keeps MIT; this table must stay after the \"**\" table above.\n" +
		"[[annotations]]\npath = [\"vendor/**\"] # not \"**\"\nSPDX-License-Identifier = \"MIT\"\n" +
		"# Like the table above, the next one must stay after the \"**\" table, ['**'] included.\n"
)

// reuseTables parses text or fails the test.
func reuseTables(t *testing.T, text string) []ReuseAnnotation {
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
		"\n[[annotations]]\npath = [\n  \"vendor/**\", # not this one\n  \".agents/skills/*/SKILL.md\",\n]\nSPDX-License-Identifier = [\"MIT\"]\n",
		"\n[[annotations]]\npath = \"**/SKILL.md\"\nSPDX-License-Identifier = \"MIT\"\n[[annotations]]\npath = \"docs/**\"\nSPDX-License-Identifier = \"EUPL-1.2\"\n",
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
		"comment quoting **":  reuseWholeTree + reuseVendorMIT,
		"later star glob":     reuseWholeTree + override + "\n[[annotations]]\npath = \"**/*.md\"\nSPDX-License-Identifier = \"EUPL-1.2\"\n",
		"star stops at /":     reuseWholeTree + strings.Replace(override, ".agents/skills/x/SKILL.md", ".agents/*/SKILL.md", 1),
	} {
		if ReuseLabels(reuseTables(t, text), ".agents/skills/x/SKILL.md", "MIT") {
			t.Errorf("%s: the file is labelled MIT", name)
		}
	}
}

// Positive: the read keeps only the path and license values of each annotations table, from a
// string or an array spanning lines, and skips comments, other keys, other tables and the lines
// of a multi-line array of another key. It steps over the values of other keys it does not read
// as strings: an array of strings with escape sequences, a multi-line copyright string, and a
// multi-line literal string whose lines look like a table and a path.
func TestReuseAnnotationTablesPositive(t *testing.T) {
	text := "version = 1\r\n[other]\npath = \"ignored/**\"\n# path = [\"comment/**\"]\n" +
		"[[annotations]]\npath = 'a/**' # \"**\"\nprecedence = \"override\"\nSPDX-FileCopyrightText = [\n  \"2026 A = B\",\n  # path = \"x\"\n]\n" +
		"SPDX-License-Identifier = [\"MIT\", 'EUPL-1.2 AND MIT']\n\n[[ annotations ]]\npath = [\n  \"b/*.md\", # x\n  \"c\"\n]\n"
	tables := reuseTables(t, text)
	if len(tables) != 2 || strings.Join(tables[0].Paths, " ") != "a/**" || strings.Join(tables[0].Licenses, "|") != "MIT|EUPL-1.2 AND MIT" ||
		strings.Join(tables[1].Paths, " ") != "b/*.md c" || len(tables[1].Licenses) != 0 {
		t.Fatalf("tables = %+v", tables)
	}
	stepped := `[[annotations]]
path = "a/**"
SPDX-FileCopyrightText = ["2024 A \"B\" C", "x"]
SPDX-FileCopyrightText = """
2024 A
2025 B \"C\"
"""
note = '''
[[annotations]]
path = "ghost/**"
'''
SPDX-License-Identifier = "MIT"
`
	tables = reuseTables(t, stepped)
	if len(tables) != 1 || strings.Join(tables[0].Paths, " ") != "a/**" || strings.Join(tables[0].Licenses, " ") != "MIT" {
		t.Fatalf("values of other keys stepped over: tables = %+v", tables)
	}
}

// Negative: a line the read cannot follow is an error naming its line, not a table read past. A
// path or license value the read cannot take as plain strings fails, and so does another key's
// value whose end it cannot find.
func TestReuseAnnotationTablesNegative(t *testing.T) {
	for name, test := range map[string]struct{ text, want string }{
		"not a key":          {"[[annotations]]\npath\n", "REUSE.toml:2: \"path\" is not a table header"},
		"open array":         {"[[annotations]]\npath = [\n  \"a/**\",\n", "the path array is not closed"},
		"number in array":    {"[[annotations]]\npath = [\"a\", 1]\n", "the path array holds something other than strings"},
		"escape sequence":    {"[[annotations]]\npath = \"a\\\\*\"\n", "is not a string without escape sequences"},
		"escaped path array": {"[[annotations]]\npath = [\"a\\\"b\", \"c\"]\n", "the path array holds something other than strings"},
		"multi-line path":    {"[[annotations]]\npath = \"\"\"\na/**\n\"\"\"\n", "path holds a multi-line string"},
		"multi-line license": {"[[annotations]]\nSPDX-License-Identifier = '''\nMIT\n'''\n", "SPDX-License-Identifier holds a multi-line string"},
		"path as a number":   {"[[annotations]]\npath = 1\n", "path = 1 is not a string"},
		"open other value":   {"[[annotations]]\nSPDX-FileCopyrightText = \"\"\"\n2026 A\n", "the value of SPDX-FileCopyrightText is not closed"},
		"open other string":  {"[[annotations]]\nSPDX-FileCopyrightText = \"2026 A\n", "the value of SPDX-FileCopyrightText is not one this read can find the end of"},
	} {
		if _, err := ReuseAnnotationTables(test.text); err == nil || !strings.Contains(err.Error(), test.want) {
			t.Errorf("%s: err = %v, want %q", name, err, test.want)
		}
	}
}

// Boundary: the REUSE 3.3 glob dialect against a file and a directory subject, an empty
// REUSE.toml, and the line bound.
func TestReuseAnnotationTablesBoundary(t *testing.T) {
	for _, test := range []struct {
		glob, subject    string
		covers, overlaps bool
	}{
		{"**", "a/b.md", true, true},
		{"docs/**", "docs/**", true, true},
		{"docs/**", "docs/a/**", true, true},
		{"*/**", "docs/**", true, true},
		{"docs/a/**", "docs/**", false, true},
		{"docs/*", "docs/**", false, true},
		{"**/*.md", "docs/**", false, true},
		{"**/*.md", "a.md", true, true},
		{"**.md", "x/y.md", true, true},
		{"*.md", "docs/a.md", false, false},
		{"other/**", "docs/**", false, false},
		{"docs/**", "docs", false, false},
		{`a\*b`, "a*b", true, true},
		{`a\*b`, "axb", false, false},
		{`a\\b`, `a\b`, true, true},
		{"file[1]?.txt", "file[1]?.txt", true, true},
		{"file[1]?.txt", "file1x.txt", false, false},
		{"", "a", false, false},
		{"", "**", false, false},
	} {
		covers, overlaps := reuseGlobRelation(test.glob, test.subject)
		if covers != test.covers || overlaps != test.overlaps {
			t.Errorf("glob %q, subject %q: covers %v, overlaps %v", test.glob, test.subject, covers, overlaps)
		}
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

// The checkout: this repository's REUSE.toml labels its own files EUPL-1.2 and not MIT, although
// the comments of its MIT tables quote the whole-tree glob, and labels the vendored interfig
// engine and the figure player bundle MIT.
func TestRepositoryReuseLabels(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", ReuseFile))
	if err != nil {
		t.Fatal(err)
	}
	tables := reuseTables(t, string(data))
	for _, rel := range []string{".agents/skills/caveman/SKILL.md", "cmd/standardsctl/main.go", "docs/index.md", "tools/figures/README.md"} {
		if ReuseLabels(tables, rel, "MIT") || !ReuseLabels(tables, rel, "EUPL-1.2") {
			t.Errorf("%s is labelled MIT or not EUPL-1.2", rel)
		}
	}
	for _, subject := range []string{"tools/figures/third_party/interfig/upstream/**", "tools/figures/dist/player.js"} {
		if !ReuseLabels(tables, subject, "MIT") {
			t.Errorf("%s is not labelled MIT", subject)
		}
	}
}
