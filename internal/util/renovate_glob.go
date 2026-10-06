// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package util

import "strings"

// renovateGlobUnsupported are the characters that take a matchFileNames pattern outside the
// glob subset RenovatePatternsCover evaluates: classes, braces, extglob groups and escapes.
const renovateGlobUnsupported = "[]{}()\\"

// RenovateGlobSupported reports whether pattern is in the glob subset RenovatePatternsCover
// evaluates: literal text, "*", "?" and "**" in non-empty segments other than "." and "..". A
// leading "!" (negation), "#" (a minimatch comment) or "/" (a regular expression, or an
// absolute path) and any character of renovateGlobUnsupported take it outside.
func RenovateGlobSupported(pattern string) bool {
	if pattern == "" || strings.ContainsAny(pattern[:1], "!#/") || strings.ContainsAny(pattern, renovateGlobUnsupported) {
		return false
	}
	segments := strings.Split(pattern, "/")
	for index := 0; index < len(segments); index++ {
		if segments[index] == "" || segments[index] == "." || segments[index] == ".." {
			return false
		}
	}
	return true
}

// RenovatePatternsCover reports whether one of patterns matches the slash-separated file the
// way Renovate's matchFileNames does (lib/util/string-match.ts: minimatch with dot set, so "*"
// also matches a leading "."). Renovate matches "*" against every file, and a trailing "**"
// spans one or more segments, as in minimatch, so a file path with "/**" appended does not
// cover the file itself. Each pattern must pass RenovateGlobSupported; outside that subset the
// answer is not Renovate's. Renovate also folds case (minimatch nocase); this compares case
// exactly, so a pattern that differs from file only in case does not cover it. The work is
// bounded by the pattern count and lengths, which callers bound.
func RenovatePatternsCover(patterns []string, file string) bool {
	segments := strings.Split(file, "/")
	for index := 0; index < len(patterns); index++ {
		glob := strings.Split(patterns[index], "/")
		if last := len(glob) - 1; glob[last] == "**" {
			glob = append(glob[:last:last], "*", "**")
		}
		if patterns[index] == "*" || MatchGlobSegments(glob, segments) {
			return true
		}
	}
	return false
}
