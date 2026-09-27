// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package util

import (
	"go/ast"
	"path/filepath"
	"strconv"
	"strings"
)

// maxGoImportSpecs bounds the import specs GoImportPaths reads from one file (HISS-02).
const maxGoImportSpecs = 4096

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

// GoImportPaths returns the import paths a parsed Go file declares, unquoted, in source
// order. A spec whose path does not unquote, or unquotes to nothing, comes from a partial
// AST and contributes no path. Blank, dot and renamed imports count like plain ones: each
// makes go build read the imported package.
//
// The needs import scan and the DevContainer bootstrap closure both read imports through
// this one rule, whatever parser mode produced the file.
func GoImportPaths(file *ast.File) []string {
	if file == nil {
		return nil
	}
	paths := make([]string, 0, min(len(file.Imports), maxGoImportSpecs))
	for i := 0; i < len(file.Imports) && i < maxGoImportSpecs; i++ {
		spec := file.Imports[i]
		if spec == nil || spec.Path == nil {
			continue
		}
		importPath, err := strconv.Unquote(spec.Path.Value)
		if err != nil || importPath == "" {
			continue
		}
		paths = append(paths, importPath)
	}
	return paths
}

// ModuleImportDir reports whether importPath names a package of the module modulePath and,
// if so, that package's directory relative to the module root, slash-separated: "." for the
// module path itself. The comparison is boundary-aware, so a sibling module that only shares
// a textual prefix (example.com/foo-plugins beside example.com/foo) is outside. An empty
// module path owns no package.
func ModuleImportDir(importPath, modulePath string) (string, bool) {
	if modulePath == "" {
		return "", false
	}
	if importPath == modulePath {
		return ".", true
	}
	dir, inside := strings.CutPrefix(importPath, modulePath+"/")
	if !inside || dir == "" {
		return "", false
	}
	return dir, true
}
