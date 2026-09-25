// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package util

import (
	"path/filepath"
	"strings"
)

// IsGoNonTestSource reports whether path names a Go source file outside Go's test surface:
// a .go file that is neither a _test.go file nor inside a directory named testdata, at any
// depth. go build never compiles either of those, so a file this rejects is test-only input.
//
// The dedupe scope and the DevContainer bootstrap capture both apply this one rule. It
// deliberately does not model the rest of go build's file selection (build constraints,
// names starting with "_" or "."): both callers need the test boundary and nothing more.
func IsGoNonTestSource(path string) bool {
	if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
		return false
	}
	slashed := filepath.ToSlash(path)
	return !strings.HasPrefix(slashed, "testdata/") && !strings.Contains(slashed, "/testdata/")
}
