// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package util_test

import (
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/util"
)

func TestNormalizeSlashesRewritesWindowsSeparators(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		// Positive: the shape a Windows filepath.Dir produces.
		{"windows path", `.config\archetypes`, ".config/archetypes"},
		{"nested windows path", `.config\archetypes\facets`, ".config/archetypes/facets"},
		// Negative: a slash path is already normal and must be returned unchanged.
		{"slash path", ".config/archetypes", ".config/archetypes"},
		// Boundary: empty, a bare separator, and a mixed form.
		{"empty", "", ""},
		{"bare separator", `\`, "/"},
		{"mixed", `a/b\c`, "a/b/c"},
		{"repeated", `a\\b`, "a//b"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := util.NormalizeSlashes(tc.in); got != tc.want {
				t.Errorf("NormalizeSlashes(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// This is the reason the helper exists rather than a call to filepath.ToSlash. On Linux ToSlash
// is the identity, so a Windows-shaped path survives it unchanged -- which is both the bug
// (BUG-948) and the reason a ToSlash fix cannot be tested off Windows.
func TestNormalizeSlashesSucceedsWhereToSlashCannot(t *testing.T) {
	const windowsShaped = `.config\archetypes`

	if got := util.NormalizeSlashes(windowsShaped); got != ".config/archetypes" {
		t.Fatalf("NormalizeSlashes must rewrite on every host, got %q", got)
	}
	if runtime.GOOS == "windows" {
		t.Skip("on Windows filepath.ToSlash also rewrites; the divergence this pins is off-Windows")
	}
	if filepath.ToSlash(windowsShaped) == ".config/archetypes" {
		t.Error("filepath.ToSlash rewrote a backslash off Windows: the premise of this helper no longer holds")
	}
}

func TestMatchGlobSegments(t *testing.T) {
	cases := []struct {
		name, pattern, path string
		want                bool
	}{
		// Positive: literal, one-segment wildcards, and "**" spanning segments.
		{"literal", "tools/markdownlint/verify.mjs", "tools/markdownlint/verify.mjs", true},
		{"star in segment", ".github/workflows/praetor-*.yml", ".github/workflows/praetor-docs.yml", true},
		{"question mark", "a/b?c", "a/bxc", true},
		{"globstar below", "tools/**", "tools/markdownlint/package.json", true},
		{"leading globstar", "**/package.json", "tools/markdownlint/package.json", true},
		// Negative: "*" and "?" never cross a separator, and a literal must match in full.
		{"star stops at separator", "tools/*", "tools/markdownlint/package.json", false},
		{"question stops at separator", "a?b", "a/b", false},
		{"literal mismatch", "tools/markdownlint/verify.mjs", "tools/markdownlint/verify.js", false},
		{"pattern longer than path", "a/b/c", "a/b", false},
		// Boundary: "**" spans zero segments, is special only as a whole segment, and a
		// malformed segment matches nothing.
		{"globstar spans zero", "a/**/b", "a/b", true},
		{"trailing globstar spans zero", "a/**", "a", true},
		{"globstar inside segment", "a**", "a/b", false},
		{"malformed class", "a/[", "a/[", false},
		{"empty pattern and path", "", "", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := util.MatchGlobSegments(strings.Split(tc.pattern, "/"), strings.Split(tc.path, "/"))
			if got != tc.want {
				t.Errorf("MatchGlobSegments(%q, %q) = %v, want %v", tc.pattern, tc.path, got, tc.want)
			}
		})
	}
}
