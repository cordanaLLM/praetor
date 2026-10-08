// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package supplychain

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
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

// labelled is ReuseLabels, failing the test on an error.
func labelled(t *testing.T, tables []ReuseAnnotation, subject, license string) bool {
	t.Helper()
	labels, err := ReuseLabels(tables, subject, license)
	if err != nil {
		t.Fatalf("ReuseLabels(%q, %q): %v", subject, license, err)
	}
	return labels
}

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
		if !labelled(t, tables, ".agents/skills/x/SKILL.md", "MIT") {
			t.Errorf("override %q does not label the file MIT", override)
		}
		if !labelled(t, tables, "docs/index.md", "EUPL-1.2") {
			t.Errorf("override %q relabels an unrelated file", override)
		}
	}
	// One override whose globs cover a directory only together, its files and its
	// subdirectories, labels the directory: coverage is the union of the table's globs.
	split := reuseTables(t, reuseWholeTree+"\n[[annotations]]\npath = [\"vendor/*\", \"vendor/*/**\"]\nSPDX-License-Identifier = \"MIT\"\n")
	if !labelled(t, split, "vendor/**", "MIT") || labelled(t, split, "vendor/**", "EUPL-1.2") {
		t.Error("an override covering vendor/ with two globs together does not label it MIT")
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
		if labelled(t, reuseTables(t, text), ".agents/skills/x/SKILL.md", "MIT") {
			t.Errorf("%s: the file is labelled MIT", name)
		}
	}
}

// Positive: the read keeps only the path and license values of each annotations table, from a
// string or an array spanning lines, and skips comments and the lines of a multi-line array of a
// stepped-over key. It steps over the values of version, precedence and SPDX-FileCopyrightText,
// whatever they hold: an array of strings with escape sequences, a multi-line copyright string,
// and a multi-line literal string whose lines look like a table and a path. A basic string's
// escape sequences decode, so a path written with the escaped star of the REUSE specification
// keeps its backslash for the glob dialect, as a literal string does.
func TestReuseAnnotationTablesPositive(t *testing.T) {
	checkAnnotationTablesBasic(t)
	checkAnnotationTablesStepped(t)
	checkAnnotationTablesEscaped(t)
}

func checkAnnotationTablesBasic(t *testing.T) {
	t.Helper()
	text := "version = 1\r\n# path = [\"comment/**\"]\n" +
		"[[annotations]]\npath = 'a/**' # \"**\"\nprecedence = \"override\"\nSPDX-FileCopyrightText = [\n  \"2026 A = B\",\n  # path = \"x\"\n]\n" +
		"SPDX-License-Identifier = [\"MIT\", 'EUPL-1.2 AND MIT']\n\n[[ annotations ]]\npath = [\n  \"b/*.md\", # x\n  \"c\"\n]\n"
	tables := reuseTables(t, text)
	if len(tables) != 2 || strings.Join(tables[0].Paths, " ") != "a/**" || strings.Join(tables[0].Licenses, "|") != "MIT|EUPL-1.2 AND MIT" ||
		strings.Join(tables[1].Paths, " ") != "b/*.md c" || len(tables[1].Licenses) != 0 {
		t.Fatalf("tables = %+v", tables)
	}
}

func checkAnnotationTablesStepped(t *testing.T) {
	t.Helper()
	stepped := `version = 1
[[annotations]]
path = "a/**"
SPDX-FileCopyrightText = ["2024 A \"B\" C", "x"]
SPDX-FileCopyrightText = """
2024 A
2025 B \"C\"
"""
precedence = '''
[[annotations]]
path = "ghost/**"
'''
SPDX-License-Identifier = "MIT"
`
	tables := reuseTables(t, stepped)
	if len(tables) != 1 || strings.Join(tables[0].Paths, " ") != "a/**" || strings.Join(tables[0].Licenses, " ") != "MIT" {
		t.Fatalf("values of other keys stepped over: tables = %+v", tables)
	}
}

func checkAnnotationTablesEscaped(t *testing.T) {
	t.Helper()
	escaped := reuseTables(t, "[[annotations]]\npath = [\"a\\\\*\", 'b\\*', \"caf\\u00e9.md\"]\nSPDX-License-Identifier = \"MIT\"\n")
	if want := []string{`a\*`, `b\*`, "café.md"}; len(escaped) != 1 || !slices.Equal(escaped[0].Paths, want) {
		t.Fatalf("escaped paths: %+v, want %q", escaped, want)
	}
}

// assertSingleTable checks that text parses to one table covering "**" with "MIT".
func assertSingleTable(t *testing.T, name, text string) {
	t.Helper()
	tables := reuseTables(t, text)
	if len(tables) != 1 {
		t.Fatalf("%s: len(tables) = %d, want 1", name, len(tables))
	}
	if got := strings.Join(tables[0].Paths, " "); got != "**" {
		t.Fatalf("%s: paths = %q, want **", name, got)
	}
	if got := strings.Join(tables[0].Licenses, " "); got != "MIT" {
		t.Fatalf("%s: licenses = %q, want MIT", name, got)
	}
}

// checkReuseToolLint runs reuse lint on fixture where installed.
func checkReuseToolLint(t *testing.T, fixture string) {
	t.Helper()
	reusePath, err := exec.LookPath("reuse")
	if err != nil {
		return
	}
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "LICENSES"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "LICENSES", "MIT.txt"), []byte("MIT License\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ReuseFile), []byte(fixture), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("# Fixture\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), reusePath, "--root", root, "lint")
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("reuse lint failed on fixture: %v\n%s", err, string(output))
	}
}

// Positive: REUSE 3.3 allows other keys and tables. The read steps over top-level keys other than
// annotations, keys inside an [[annotations]] table other than path and SPDX-License-Identifier,
// multi-line arrays of unknown keys, and unknown tables and arrays of tables. Where the reuse
// tool is installed, reuse lint confirms compliance on the fixture.
func TestReuseOtherKeysAndTablesPositive(t *testing.T) {
	assertSingleTable(t, "issue #896", "version = 1\n"+
		"SPDX-PackageName = \"my-package\"\n"+
		"SPDX-PackageSupplier = \"Supplier <supplier@example.com>\"\n"+
		"SPDX-PackageDownloadLocation = \"https://github.com/example/repo\"\n\n"+
		"[[annotations]]\npath = [\"**\"]\nSPDX-License-Identifier = \"MIT\"\n")

	assertSingleTable(t, "SPDX-FileComment / Contributor", "version = 1\n\n[[annotations]]\npath = [\"**\"]\n"+
		"SPDX-FileComment = \"a file comment\"\n"+
		"SPDX-FileContributor = \"Contributor <contrib@example.com>\"\n"+
		"SPDX-License-Identifier = \"MIT\"\n")

	assertSingleTable(t, "multi-line array value", "version = 1\n"+
		"custom_top_array = [\n  \"top1\",\n  \"top2\",\n]\n\n"+
		"[[annotations]]\npath = [\"**\"]\nSPDX-License-Identifier = \"MIT\"\n"+
		"table_unknown_array = [\n  \"sub1\",\n  \"sub2\",\n]\n")

	assertSingleTable(t, "unknown tables", "version = 1\n\n"+
		"[metadata]\nname = \"package-name\"\nversion = \"2.0.0\"\n\n"+
		"[[annotations]]\npath = [\"**\"]\nSPDX-License-Identifier = \"MIT\"\n\n"+
		"[[contributors]]\nname = \"Alice\"\n[[contributors]]\nname = \"Bob\"\n")

	assertSingleTable(t, "quoted annotations header", "version = 1\n\n[[\"annotations\"]]\npath = [\"**\"]\nSPDX-License-Identifier = \"MIT\"\n")
	assertSingleTable(t, "literal quoted annotations header", "version = 1\n\n[['annotations']]\npath = [\"**\"]\nSPDX-License-Identifier = \"MIT\"\n")

	ignoredAfterAnnotation := `version = 1
[[annotations]]
path = "a"
SPDX-License-Identifier = "MIT"

[other]
path = "**"
SPDX-License-Identifier = "EUPL-1.2"
`
	tables := reuseTables(t, ignoredAfterAnnotation)
	if len(tables) != 1 || strings.Join(tables[0].Paths, " ") != "a" || strings.Join(tables[0].Licenses, " ") != "MIT" {
		t.Fatalf("ignored table keys recorded on previous annotation: %+v", tables)
	}

	ignoredWithAnnotationsKey := `version = 1
[[annotations]]
path = "a"
SPDX-License-Identifier = "MIT"

[other]
annotations = "stepped over"
annotations.x = "also stepped over"
`
	tables = reuseTables(t, ignoredWithAnnotationsKey)
	if len(tables) != 1 || strings.Join(tables[0].Paths, " ") != "a" || strings.Join(tables[0].Licenses, " ") != "MIT" {
		t.Fatalf("annotations key in ignored table not stepped over: %+v", tables)
	}

	checkReuseToolLint(t, "version = 1\n"+
		"SPDX-PackageName = \"fixture-pkg\"\n"+
		"SPDX-PackageSupplier = \"Supplier <supplier@example.com>\"\n"+
		"SPDX-PackageDownloadLocation = \"https://example.com\"\n"+
		"custom_tags = [\n  \"tag1\",\n  \"tag2\",\n]\n\n"+
		"[metadata]\nowner = \"team\"\n\n"+
		"[[annotations]]\npath = [\"**\"]\n"+
		"SPDX-FileComment = \"comment\"\n"+
		"SPDX-FileContributor = \"contributor\"\n"+
		"SPDX-FileCopyrightText = \"2026 Test Author\"\n"+
		"SPDX-License-Identifier = \"MIT\"\n"+
		"extra = [\n  \"a\",\n  \"b\",\n]\n\n"+
		"[[contributors]]\nname = \"Alice\"\n")
}

// Negative: a line the read cannot follow is an error naming its line, not a table read past. A
// path or license value the read cannot take as single-line strings fails, and so does another
// key's value whose end it cannot find. The read is an allow-list: annotations written as an
// inline array of tables, on one line or several, a dotted key whose first segment is annotations
// at top level, [annotations], [annotations.x], [[annotations.x]], and a dotted key whose first
// segment is path, precedence, SPDX-FileCopyrightText or SPDX-License-Identifier inside an
// [[annotations]] table fail closed, naming the key or header and the form to write it in.
func TestReuseAnnotationTablesNegative(t *testing.T) {
	for name, test := range map[string]struct{ text, want string }{
		"not a key":            {"[[annotations]]\npath\n", "REUSE.toml:2: \"path\" is not a table header"},
		"open array":           {"[[annotations]]\npath = [\n  \"a/**\",\n", "the path array is not closed"},
		"number in array":      {"[[annotations]]\npath = [\"a\", 1]\n", "the path array holds something other than single-line strings"},
		"undefined escape":     {"[[annotations]]\npath = \"a\\qb\"\n", "a literal string ('...') keeps every backslash as written"},
		"undefined escape arr": {"[[annotations]]\npath = [\"a\\qb\", \"c\"]\n", "the path array holds something other than single-line strings"},
		"multi-line path":      {"[[annotations]]\npath = \"\"\"\na/**\n\"\"\"\n", "path holds a multi-line string"},
		"multi-line license":   {"[[annotations]]\nSPDX-License-Identifier = '''\nMIT\n'''\n", "SPDX-License-Identifier holds a multi-line string"},
		"path as a number":     {"[[annotations]]\npath = 1\n", "path = 1 is not a single-line string"},
		"open other value":     {"[[annotations]]\nSPDX-FileCopyrightText = \"\"\"\n2026 A\n", "the value of SPDX-FileCopyrightText is not closed"},
		"open other string":    {"[[annotations]]\nSPDX-FileCopyrightText = \"2026 A\n", "the value of SPDX-FileCopyrightText is not one this read can find the end of"},
		"inline tables, one line": {
			"version = 1\nannotations = [ { path = \"vendor/**\", SPDX-License-Identifier = \"MIT\" }, { path = \"**\", SPDX-License-Identifier = \"EUPL-1.2\" } ]\n",
			"REUSE.toml:2: the top-level key annotations is not one this read follows: write each annotation as a table of its own, opened by a [[annotations]] line",
		},
		"inline tables, lines": {
			"version = 1\nannotations = [\n  { path = \"vendor/**\", SPDX-License-Identifier = \"MIT\" },\n  { path = \"**\", SPDX-License-Identifier = \"EUPL-1.2\" },\n]\n",
			"REUSE.toml:2: the top-level key annotations is not one this read follows",
		},
		"dotted top-level key":   {"version = 1\nannotations.path = \"**\"\n", "the top-level key annotations.path is not one this read follows: write each annotation"},
		"plain table header":     {"version = 1\n[annotations]\npath = \"**\"\n", "the table header [annotations] is not one this read follows"},
		"plain sub-table header": {"version = 1\n[annotations.x]\npath = \"**\"\n", "the table header [annotations.x] is not one this read follows"},
		"array sub-table header": {"[[annotations]]\npath = \"a\"\n[[annotations.more]]\n", "the table header [[annotations.more]] is not one this read follows"},
		"dotted table key":       {"[[annotations]]\npath.glob = \"a\"\n", "the key path.glob of an [[annotations]] table is not one this read follows"},
		"dotted table key x":     {"[[annotations]]\npath.x = \"a\"\n", "the key path.x of an [[annotations]] table is not one this read follows"},
		"dotted table precedence": {
			"[[annotations]]\npath = \"a\"\nprecedence.x = \"override\"\n",
			"the key precedence.x of an [[annotations]] table is not one this read follows",
		},
		"dotted table copyright": {
			"[[annotations]]\npath = \"a\"\nSPDX-FileCopyrightText.x = \"2026 Author\"\n",
			"the key SPDX-FileCopyrightText.x of an [[annotations]] table is not one this read follows",
		},
		"dotted table license": {
			"[[annotations]]\npath = \"a\"\nSPDX-License-Identifier.x = \"MIT\"\n",
			"the key SPDX-License-Identifier.x of an [[annotations]] table is not one this read follows",
		},
		"quoted annotations table header": {
			"version = 1\n[\"annotations\"]\npath = \"**\"\n",
			"the table header [\"annotations\"] is not one this read follows",
		},
		"literal quoted annotations table header": {
			"version = 1\n['annotations']\npath = \"**\"\n",
			"the table header ['annotations'] is not one this read follows",
		},
		"quoted annotations dotted top-level key": {
			"version = 1\n\"annotations\".path = \"**\"\n",
			"the top-level key \"annotations\".path is not one this read follows",
		},
		"empty table header": {
			"[]\n",
			"the table header [] is not one this read follows",
		},
		"empty quoted table header": {
			"[\"\"]\n",
			"the table header [\"\"] is not one this read follows",
		},
		"escaped annotations header": {
			"[[\"annot\\u0061tions\"]]\npath = \"**\"\nSPDX-License-Identifier = \"MIT\"\n",
			"the table header [[\"annot\\u0061tions\"]] is not one this read follows",
		},
		"escaped top-level annotations key": {
			"version = 1\n\"annot\\u0061tions\" = [{ path = \"**\", SPDX-License-Identifier = \"MIT\" }]\n",
			"the top-level key \"annot\\u0061tions\" is not one this read follows",
		},
		"escaped path key": {
			"[[annotations]]\n\"p\\u0061th\" = \"**\"\nSPDX-License-Identifier = \"MIT\"\n",
			"the key p\\u0061th of an [[annotations]] table is not one this read follows",
		},
	} {
		if _, err := ReuseAnnotationTables(test.text); err == nil || !strings.Contains(err.Error(), test.want) {
			t.Errorf("%s: err = %v, want %q", name, err, test.want)
		}
	}
}

// Boundary: the REUSE 3.3 glob dialect against a file and a directory subject, whether one glob
// matches every file of the subject (the coverage decision, includes over reuseSubjectGlob) and
// at least one (reuseGlobOverlaps), an empty REUSE.toml, and the line bound.
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
		subject := reuseSubjectGlob(test.subject)
		covers, err := newReuseGlobCheck([]string{test.glob, subject}, maxReuseGlobSteps).includes([]string{test.glob}, subject)
		if overlaps := reuseGlobOverlaps(test.glob, test.subject); err != nil || covers != test.covers || overlaps != test.overlaps {
			t.Errorf("glob %q, subject %q: covers %v (%v), overlaps %v", test.glob, test.subject, covers, err, overlaps)
		}
	}
	if tables := reuseTables(t, ""); len(tables) != 0 || labelled(t, tables, "a.md", "MIT") {
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
		if labelled(t, tables, rel, "MIT") || !labelled(t, tables, rel, "EUPL-1.2") {
			t.Errorf("%s is labelled MIT or not EUPL-1.2", rel)
		}
	}
	for _, subject := range []string{"tools/figures/third_party/interfig/upstream/**", "tools/figures/dist/player.js"} {
		if !labelled(t, tables, subject, "MIT") {
			t.Errorf("%s is not labelled MIT", subject)
		}
	}
}
