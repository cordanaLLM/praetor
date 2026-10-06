// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package util_test

import (
	"testing"

	"github.com/cordanaLLM/praetor/internal/util"
)

// renovateCoverCases were checked against minimatch 10.2.6 with dot and nocase set, the
// matcher Renovate 44.115.10 applies to matchFileNames, except where a case says otherwise.
var renovateCoverCases = []struct {
	name, pattern, file string
	want                bool
}{
	// Positive: a literal path, "*" and "?" inside one segment, "**" below a directory and as
	// a prefix, and "*" alone, which Renovate matches against every file.
	{"literal", "tools/markdownlint/package.json", "tools/markdownlint/package.json", true},
	{"star in segment", "tools/markdownlint/*.json", "tools/markdownlint/package.json", true},
	{"question mark", "tools/markdownlint/package.jso?", "tools/markdownlint/package.json", true},
	{"directory globstar", "tools/markdownlint/**", "tools/markdownlint/package.json", true},
	{"leading globstar", "**/package.json", "tools/markdownlint/package.json", true},
	{"star is every file", "*", "tools/markdownlint/package.json", true},
	{"star matches a dot file", "tools/markdownlint/*", "tools/markdownlint/.npmrc", true},
	// Negative: "*" stays in its segment, a directory does not cover its files, and a
	// trailing "**" needs at least one segment below the path it follows.
	{"star stays in its segment", "tools/*", "tools/markdownlint/package.json", false},
	{"directory without globstar", "tools/markdownlint", "tools/markdownlint/package.json", false},
	{"trailing globstar below a file", "tools/markdownlint/package.json/**", "tools/markdownlint/package.json", false},
	{"trailing globstar below a directory", "tools/markdownlint/**", "tools/markdownlint", false},
	{"other file", "tools/markdownlint/package-lock.json", "tools/markdownlint/package.json", false},
	// Boundary: a lone "**" covers a top-level file, a trailing "**" one segment down, an inner
	// or leading "**" zero segments, and case is compared exactly where Renovate folds it.
	{"lone globstar", "**", "package.json", true},
	{"trailing globstar one segment down", "tools/markdownlint/package.json/**", "tools/markdownlint/package.json/x", true},
	{"inner globstar spans zero", "tools/**/package.json", "tools/package.json", true},
	{"leading globstar spans zero", "**/package.json", "package.json", true},
	{"case compared exactly, unlike Renovate", "TOOLS/**", "tools/markdownlint/package.json", false},
}

// Positive, negative and boundary cases of RenovatePatternsCover, one pattern at a time.
func TestRenovatePatternsCover(t *testing.T) {
	for _, tc := range renovateCoverCases {
		t.Run(tc.name, func(t *testing.T) {
			if !util.RenovateGlobSupported(tc.pattern) {
				t.Fatalf("pattern %q is outside the evaluated subset", tc.pattern)
			}
			if got := util.RenovatePatternsCover([]string{tc.pattern}, tc.file); got != tc.want {
				t.Errorf("RenovatePatternsCover(%q, %q) = %v, want %v", tc.pattern, tc.file, got, tc.want)
			}
		})
	}
}

// Boundary: a list covers a file when any pattern does, and an empty list covers nothing.
func TestRenovatePatternsCoverList(t *testing.T) {
	const file = "tools/markdownlint/package.json"
	if util.RenovatePatternsCover(nil, file) {
		t.Error("an empty pattern list covers a file")
	}
	if util.RenovatePatternsCover([]string{"tools/*", file + "/**"}, file) {
		t.Error("a list of patterns that each miss covers the file")
	}
	if !util.RenovatePatternsCover([]string{"tools/*", "docs/**", "tools/**"}, file) {
		t.Error("a list whose last pattern matches does not cover the file")
	}
}

// Positive, negative and boundary cases of RenovateGlobSupported.
func TestRenovateGlobSupported(t *testing.T) {
	for pattern, want := range map[string]bool{
		// Positive: literal text, "*", "?" and "**".
		"tools/markdownlint/package.json": true, "*": true, "**/package.*": true, "a/b?c/**": true,
		// Negative: negation, a comment, a regular expression or absolute path, classes,
		// braces, extglob groups and escapes.
		"!tools/**": false, "#tools": false, "/praetor/": false, "tools/[ab]": false,
		"{tools,docs}/**": false, "tools/@(a|b)": false, `tools\/a`: false,
		// Boundary: the empty pattern, an empty segment, and "." and ".." segments.
		"": false, "tools//a": false, "tools/": false, "./tools": false, "tools/../a": false,
	} {
		if got := util.RenovateGlobSupported(pattern); got != want {
			t.Errorf("RenovateGlobSupported(%q) = %v, want %v", pattern, got, want)
		}
	}
}
