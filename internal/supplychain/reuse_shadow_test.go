// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package supplychain

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// reuseGlobIncludes decides whether a later glob relabels every file of an earlier one, under the
// REUSE 3.3 glob dialect. Positive: the whole tree, a directory below a directory, a suffix in any
// directory, an identical path. Negative: a single star stops at "/", a narrower glob does not
// hold a wider one, and two sibling files differ. Boundary: an escaped star is a literal, which a
// star includes and which does not include a star.
func TestReuseGlobIncludes_3D(t *testing.T) {
	for _, tc := range []struct {
		outer, inner string
		want         bool
	}{
		{"**", "tools/vendor/upstream/**", true},
		{"docs/**", "docs/a/*.md", true},
		{"**/*.md", "docs/*.md", true},
		{"a/b.txt", "a/b.txt", true},
		{"**", "**", true},
		{"*.md", "docs/x.md", false},
		{"docs/*.md", "**/*.md", false},
		{"tools/vendor/upstream/**", "**", false},
		{"a/b.txt", "a/c.txt", false},
		{"**/*.md", "docs/**", false},
		{"a*", `a\*`, true},
		{`a\*`, "a*", false},
	} {
		if got := reuseGlobIncludes(tc.outer, tc.inner); got != tc.want {
			t.Errorf("reuseGlobIncludes(%q, %q) = %v, want %v", tc.outer, tc.inner, got, tc.want)
		}
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
	want := []ReuseShadow{{Table: 1, Path: "vendor/upstream/**", By: 2, ByPath: "**"}, {Table: 1, Path: "NOTICE", By: 2, ByPath: "**"}}
	if len(shadows) != len(want) || shadows[0] != want[0] || shadows[1] != want[1] {
		t.Fatalf("default after override: %+v, want %+v", shadows, want)
	}
	if text := shadows[0].String(); !strings.Contains(text, `annotation 1 path "vendor/upstream/**" never takes effect`) ||
		!strings.Contains(text, "whatever its precedence") || !strings.Contains(text, "move annotation 1 after annotation 2") {
		t.Errorf("the finding does not say what to do: %s", text)
	}
}

// Boundary: a later glob matching only some of an annotation's files leaves the rest in effect
// and is no shadow; only the covered path of a table with several is reported; an identical later
// path shadows the earlier one; past the comparison bound the check refuses instead of answering
// in part.
func TestReuseShadowedPaths_Boundary(t *testing.T) {
	partial := overrideAfterDefault + "\n[[annotations]]\npath = \"**/*.md\"\nSPDX-License-Identifier = \"CC-BY-4.0\"\n"
	if shadows := shadowsOf(t, partial); len(shadows) != 0 {
		t.Fatalf("a later glob covering part of the files shadows %v", shadows)
	}
	narrower := overrideAfterDefault + "\n[[annotations]]\npath = \"NOTICE\"\nSPDX-License-Identifier = \"Apache-2.0\"\n"
	shadows := shadowsOf(t, narrower)
	if len(shadows) != 1 || shadows[0] != (ReuseShadow{Table: 2, Path: "NOTICE", By: 3, ByPath: "NOTICE"}) {
		t.Fatalf("only NOTICE of table 2 is shadowed, by table 3: %+v", shadows)
	}
	// One table of n paths followed by one of m paths makes n*m pairs: exactly the bound passes,
	// one pair more is refused.
	wide := func(prefix string, count int) ReuseAnnotation {
		paths := make([]string, 0, count)
		for index := 0; index < count; index++ {
			paths = append(paths, prefix+strconv.Itoa(index)+"/**")
		}
		return ReuseAnnotation{Paths: paths}
	}
	atBound := []ReuseAnnotation{wide("a", maxReuseShadowChecks/256), wide("b", 256)}
	if shadows, err := ReuseShadowedPaths(atBound); err != nil || len(shadows) != 0 {
		t.Fatalf("at the comparison bound: %v, %v", shadows, err)
	}
	pastBound := []ReuseAnnotation{wide("a", maxReuseShadowChecks/256), wide("b", 256), wide("c", 1)}
	if _, err := ReuseShadowedPaths(pastBound); err == nil || !strings.Contains(err.Error(), "path pairs") {
		t.Fatalf("past the comparison bound: %v", err)
	}
}
