// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package util

import (
	"path"
	"strings"
)

// NormalizeSlashes rewrites a path to its slash form on every platform.
//
// filepath.ToSlash is the obvious choice and the wrong one: it replaces the *host's* separator,
// so on Linux it leaves a Windows-shaped path untouched. Two consequences follow, and the
// second is what keeps catching this repository out. A path recorded on Windows still compares
// unequal to its slash form when the comparison runs on Linux (BUG-948), and a fix written with
// ToSlash cannot be tested anywhere except Windows, because on every other host it does nothing
// at all.
//
// Replacing the backslash unconditionally is correct on Windows and observable on Linux, which
// is what makes the behaviour testable from any machine rather than only from the one that
// breaks.
func NormalizeSlashes(path string) string {
	return strings.ReplaceAll(path, "\\", "/")
}

// MatchGlobSegments reports whether the glob segments of a slash-separated pattern match the
// segments of a slash-separated path in full. A segment that is exactly "**" spans any number
// of path segments, zero included; every other segment is a path.Match pattern for exactly one
// path segment, so "*" and "?" never cross a separator. The evaluation is iterative (HISS-01)
// and bounded by the two slice lengths, which callers bound.
func MatchGlobSegments(pattern, segments []string) bool {
	reached := make([]bool, len(pattern)+1)
	reached[0] = true
	for j := 0; j < len(pattern) && pattern[j] == "**"; j++ {
		reached[j+1] = true
	}
	for i := 0; i < len(segments); i++ {
		next := make([]bool, len(pattern)+1)
		for j := 1; j <= len(pattern); j++ {
			if pattern[j-1] == "**" {
				next[j] = next[j-1] || reached[j]
				continue
			}
			matched, err := path.Match(pattern[j-1], segments[i])
			next[j] = reached[j-1] && err == nil && matched
		}
		reached = next
	}
	return reached[len(pattern)]
}
