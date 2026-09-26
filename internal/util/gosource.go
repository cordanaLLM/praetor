// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package util

import (
	"path/filepath"
	"strings"
)

// IsGoNonTestSource reports whether path names a Go source file outside Go's test surface:
// a .go file that IsGoTestSurface does not claim. go build never compiles a file on the test
// surface, so a .go file this rejects is test-only input.
//
// The dedupe scope and the DevContainer bootstrap capture both apply this one rule. It
// deliberately does not model the rest of go build's file selection (build constraints,
// names starting with "_" or "."): both callers need the test boundary and nothing more.
func IsGoNonTestSource(path string) bool {
	return strings.HasSuffix(path, ".go") && !IsGoTestSurface(path)
}

// IsGoTestSurface reports whether path lies on Go's test surface, whatever its extension: a
// _test.go file, or any file inside a directory named testdata at any depth. The go tool
// reads neither when building, so the DevContainer bootstrap refuses every such path,
// including a declared non-Go asset.
func IsGoTestSurface(path string) bool {
	slashed := filepath.ToSlash(path)
	return strings.HasSuffix(slashed, "_test.go") || strings.HasPrefix(slashed, "testdata/") || strings.Contains(slashed, "/testdata/")
}

// IsGoMajorVersionElement reports whether s is a Go major-version path element such as v2 or
// v10: a "v" followed by one or more decimal digits. The HISS import-alias resolver and the
// needs module-path analyser both apply this one rule.
func IsGoMajorVersionElement(s string) bool {
	if len(s) < 2 || s[0] != 'v' {
		return false
	}
	for i := 1; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}
