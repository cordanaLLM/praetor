// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package supplychain

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
)

// includes decides whether later globs together relabel every file of an earlier one, under the
// REUSE 3.3 glob dialect. Positive: the whole tree, a directory below a directory, a suffix in any
// directory, an identical path, and unions no one of whose globs covers alone: the whole tree
// spelled as "*", ".*", "*/**" and ".*/**" (a star matches a leading dot, so "*" and "*/**" are
// enough) and a directory as its files plus its subdirectories. Negative: a single star stops at
// "/", a narrower glob does not hold a wider one, two sibling files differ, and a union that leaves
// subdirectories out is no whole tree. Boundary: an escaped star is a literal, which a star
// includes and which does not include a star, and no globs include nothing.
func TestReuseGlobIncludes_3D(t *testing.T) {
	for _, tc := range []struct {
		outers []string
		inner  string
		want   bool
	}{
		{[]string{"**"}, "tools/vendor/upstream/**", true},
		{[]string{"docs/**"}, "docs/a/*.md", true},
		{[]string{"**/*.md"}, "docs/*.md", true},
		{[]string{"a/b.txt"}, "a/b.txt", true},
		{[]string{"**"}, "**", true},
		{[]string{"*", ".*", "*/**", ".*/**"}, "**", true},
		{[]string{"*", "*/**"}, "**", true},
		{[]string{"docs/*", "docs/*/**"}, "docs/**", true},
		{[]string{"*", ".*", "*/**", ".*/**"}, "**/*.patch", true},
		{[]string{"*.md"}, "docs/x.md", false},
		{[]string{"docs/*.md"}, "**/*.md", false},
		{[]string{"tools/vendor/upstream/**"}, "**", false},
		{[]string{"a/b.txt"}, "a/c.txt", false},
		{[]string{"**/*.md"}, "docs/**", false},
		{[]string{"*", ".*", ".*/**"}, "**", false},
		{[]string{"*", ".*", ".*/**"}, "*/**", false},
		{[]string{"a*"}, `a\*`, true},
		{[]string{`a\*`}, "a*", false},
		{nil, "a.txt", false},
	} {
		got, err := newReuseGlobCheck(append(tc.outers, tc.inner), maxReuseGlobSteps).includes(tc.outers, tc.inner)
		if err != nil || got != tc.want {
			t.Errorf("includes(%q, %q) = %v, %v; want %v", tc.outers, tc.inner, got, err, tc.want)
		}
	}
}

// The step budget bounds the real cost of one comparison. Boundary: a comparison given exactly
// the steps it spends answers, and one step fewer is ErrReuseGlobBound naming the bound, never
// "not included". Negative: a glob the check was not prepared for is refused, since the alphabet
// would not name its bytes.
func TestReuseGlobCheck_Budget(t *testing.T) {
	outers, inner := []string{"*", ".*", "*/**", ".*/**"}, "vendor/upstream/**"
	globs := append(append([]string{}, outers...), inner)
	probe := newReuseGlobCheck(globs, maxReuseGlobSteps)
	if covered, err := probe.includes(outers, inner); err != nil || !covered {
		t.Fatalf("the union covers the directory: %v, %v", covered, err)
	}
	spent := maxReuseGlobSteps - probe.left
	if covered, err := newReuseGlobCheck(globs, spent).includes(outers, inner); err != nil || !covered {
		t.Fatalf("with exactly the %d steps it spends: %v, %v", spent, covered, err)
	}
	covered, err := newReuseGlobCheck(globs, spent-1).includes(outers, inner)
	if !errors.Is(err, ErrReuseGlobBound) || covered || !strings.Contains(err.Error(), fmt.Sprintf("of %d", spent-1)) {
		t.Fatalf("one step short: %v, %v; want ErrReuseGlobBound naming the bound", covered, err)
	}
	if _, err := newReuseGlobCheck(outers, maxReuseGlobSteps).includes(outers, inner); err == nil || errors.Is(err, ErrReuseGlobBound) {
		t.Fatalf("an unprepared glob: %v, want a refusal", err)
	}
}

// overrideAfterDefault is the order REUSE resolves as written: the whole-tree default first, the
// vendored override after it.
const overrideAfterDefault = `version = 1

[[annotations]]
path = ["**"]
precedence = "aggregate"
SPDX-License-Identifier = "EUPL-1.2"

[[annotations]]
path = ["vendor/upstream/**", "NOTICE"]
precedence = "override"
SPDX-License-Identifier = "MIT"
`

// defaultAfterOverride holds the same tables with the default placed after the override, which
// relabels every vendored file EUPL-1.2 while reuse lint still passes.
const defaultAfterOverride = `version = 1

[[annotations]]
path = ["vendor/upstream/**", "NOTICE"]
precedence = "override"
SPDX-License-Identifier = "MIT"

[[annotations]]
path = ["**"]
precedence = "aggregate"
SPDX-License-Identifier = "EUPL-1.2"
`

// shadowsOf parses text and returns its shadowed paths, failing on any error.
func shadowsOf(t *testing.T, text string) []ReuseShadow {
	t.Helper()
	tables, err := ReuseAnnotationTables(text)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	shadows, err := ReuseShadowedPaths(tables)
	if err != nil {
		t.Fatalf("ReuseShadowedPaths: %v", err)
	}
	return shadows
}

// Positive: the default first and the override after it shadows nothing, and neither does
// praetor's own REUSE.toml. Negative: the default after the override shadows each override path,
// naming the default, and precedence = "override" does not save it.
func TestReuseShadowedPaths_DefaultOrder(t *testing.T) {
	if shadows := shadowsOf(t, overrideAfterDefault); len(shadows) != 0 {
		t.Fatalf("the correct order shadows %v", shadows)
	}
	own, err := os.ReadFile(filepath.Join("..", "..", ReuseFile))
	if err != nil {
		t.Fatalf("read praetor's own %s: %v", ReuseFile, err)
	}
	if shadows := shadowsOf(t, string(own)); len(shadows) != 0 {
		t.Fatalf("praetor's own %s shadows %v", ReuseFile, shadows)
	}
	shadows := shadowsOf(t, defaultAfterOverride)
	want := []ReuseShadow{{Table: 1, Path: "vendor/upstream/**", By: 2, ByPaths: []string{"**"}}, {Table: 1, Path: "NOTICE", By: 2, ByPaths: []string{"**"}}}
	if !reflect.DeepEqual(shadows, want) {
		t.Fatalf("default after override: %+v, want %+v", shadows, want)
	}
	if text := shadows[0].String(); !strings.Contains(text, `annotation 1 path "vendor/upstream/**" never takes effect`) ||
		!strings.Contains(text, "whatever its precedence") || !strings.Contains(text, "move annotation 1 after annotation 2") {
		t.Errorf("the finding does not say what to do: %s", text)
	}
}

// Boundary: a later glob matching only some of an annotation's files leaves the rest in effect
// and is no shadow; only the covered path of a table with several is reported; an identical later
// path shadows the earlier one; a file of hundreds of single-file tables is checked in full.
func TestReuseShadowedPaths_Boundary(t *testing.T) {
	partial := overrideAfterDefault + "\n[[annotations]]\npath = \"**/*.md\"\nSPDX-License-Identifier = \"CC-BY-4.0\"\n"
	if shadows := shadowsOf(t, partial); len(shadows) != 0 {
		t.Fatalf("a later glob covering part of the files shadows %v", shadows)
	}
	narrower := overrideAfterDefault + "\n[[annotations]]\npath = \"NOTICE\"\nSPDX-License-Identifier = \"Apache-2.0\"\n"
	shadows := shadowsOf(t, narrower)
	if !reflect.DeepEqual(shadows, []ReuseShadow{{Table: 2, Path: "NOTICE", By: 3, ByPaths: []string{"NOTICE"}}}) {
		t.Fatalf("only NOTICE of table 2 is shadowed, by table 3: %+v", shadows)
	}
	// A file listing every vendored file in a table of its own, after a whole-tree default, holds
	// far more tables than the 362 the earlier pair bound refused, and stays well within the step
	// budget: a glob dies in the comparison as soon as its path leaves the one compared.
	listed := []ReuseAnnotation{{Paths: []string{"**"}}}
	for index := 0; index < 600; index++ {
		listed = append(listed, ReuseAnnotation{Paths: []string{"vendor/upstream/lib/source-" + strconv.Itoa(index) + ".c"}})
	}
	if shadows, err := ReuseShadowedPaths(listed); err != nil || len(shadows) != 0 {
		t.Fatalf("600 single-file tables after the default: %v, %v", shadows, err)
	}
}

// Negative: past its step budget the order check refuses with ErrReuseGlobBound, naming the
// table, the path and the bound, rather than report no shadow for a file that holds one.
func TestReuseShadowedPaths_PastTheBound(t *testing.T) {
	tables, err := ReuseAnnotationTables(defaultAfterOverride)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	shadows, err := reuseShadowedPaths(tables, 8)
	if !errors.Is(err, ErrReuseGlobBound) || len(shadows) != 0 || !strings.Contains(err.Error(), `annotation 1 path "vendor/upstream/**"`) ||
		!strings.Contains(err.Error(), "of 8") {
		t.Fatalf("past the bound: %+v, %v; want ErrReuseGlobBound naming the path and the bound", shadows, err)
	}
	if _, found, err := wholeTreeLicense(tables, 2); found || !errors.Is(err, ErrReuseGlobBound) {
		t.Fatalf("the whole-tree read past the bound: found %v, %v", found, err)
	}
	if labelled, err := reuseLabels(tables, "vendor/upstream/a.c", "EUPL-1.2", 2); labelled || !errors.Is(err, ErrReuseGlobBound) {
		t.Fatalf("the label read past the bound: %v, %v", labelled, err)
	}
}

// unionAfterOverride places a whole-tree default spelled as four globs after an override, the
// order that relabels every patch EUPL-1.2.
const unionAfterOverride = `version = 1

[[annotations]]
path = "**/*.patch"
precedence = "override"
SPDX-License-Identifier = "LGPL-2.1-or-later"

[[annotations]]
path = ["*", ".*", "*/**", ".*/**"]
precedence = "closest"
SPDX-License-Identifier = "EUPL-1.2"
`

// Later globs shadow a path together. Positive: the same union placed first shadows nothing.
// Negative: the union default after the override shadows it and is named with all its globs, and
// a directory whose files and subdirectories two later tables cover between them is shadowed by
// the second, with the tables named as a range and an unrelated table between them skipped.
// Boundary: a range that still leaves some files to the earlier path is no shadow.
func TestReuseShadowedPaths_Union(t *testing.T) {
	defaultFirst := "version = 1\n\n[[annotations]]\npath = [\"*\", \".*\", \"*/**\", \".*/**\"]\nSPDX-License-Identifier = \"EUPL-1.2\"\n\n" +
		"[[annotations]]\npath = \"**/*.patch\"\nSPDX-License-Identifier = \"LGPL-2.1-or-later\"\n"
	if shadows := shadowsOf(t, defaultFirst); len(shadows) != 0 {
		t.Fatalf("the union default placed first shadows %+v", shadows)
	}
	want := []ReuseShadow{{Table: 1, Path: "**/*.patch", By: 2, ByPaths: []string{"*", ".*", "*/**", ".*/**"}}}
	shadows := shadowsOf(t, unionAfterOverride)
	if !reflect.DeepEqual(shadows, want) {
		t.Fatalf("the union default after the override: %+v, want %+v", shadows, want)
	}
	if text := shadows[0].String(); !strings.Contains(text, `annotation 2 paths "*", ".*", "*/**", ".*/**", after it, matches every file`) {
		t.Errorf("the finding does not name the union: %s", text)
	}
	split := []ReuseAnnotation{{Paths: []string{"docs/**"}}, {Paths: []string{"src/**"}}, {Paths: []string{"docs/*"}},
		{Paths: []string{"docs/*/**"}}, {Paths: []string{"NOTICE"}}}
	shadows, err := ReuseShadowedPaths(split)
	if err != nil || !reflect.DeepEqual(shadows, []ReuseShadow{{Table: 1, Path: "docs/**", By: 4}}) {
		t.Fatalf("a directory two later tables cover between them: %+v, %v", shadows, err)
	}
	if text := shadows[0].String(); !strings.Contains(text, "annotations 2 to 4, after it, together match every file") ||
		!strings.Contains(text, "move annotation 1 after annotation 4") {
		t.Errorf("the finding does not name the range: %s", text)
	}
	if shadows, err := ReuseShadowedPaths(split[:3]); err != nil || len(shadows) != 0 {
		t.Fatalf("a range leaving the subdirectories in effect: %+v, %v", shadows, err)
	}
}
