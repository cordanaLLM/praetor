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

// maxGoImportSpecs bounds the import specs GoImportSpecs reads from one file (HISS-02).
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

// GoImportSpec is one import declaration of a parsed Go file: the name it is written with
// ("" for an unrenamed import, "_" for a blank one, "." for a dot import, otherwise the
// alias) and its unquoted import path.
type GoImportSpec struct {
	Name string
	Path string
}

// GoImportSpecs returns the import declarations of a parsed Go file, in source order, reading
// at most maxGoImportSpecs specs. A nil spec, a spec without a path, and a path that does
// not unquote or unquotes to nothing come from a partial AST and are skipped.
//
// Every reader that skips such a spec reads imports through this one rule: the HISS import
// table (hiss.FileImports), GoImportPaths, the docs vocabulary tree and the caveman MCP
// source reader. A reader that must refuse an undecodable import, or needs the spec's
// position, walks file.Imports itself: the needs import rewrite does both.
func GoImportSpecs(file *ast.File) []GoImportSpec {
	if file == nil {
		return nil
	}
	specs := make([]GoImportSpec, 0, min(len(file.Imports), maxGoImportSpecs))
	for i := 0; i < len(file.Imports) && i < maxGoImportSpecs; i++ {
		spec := file.Imports[i]
		if spec == nil || spec.Path == nil {
			continue
		}
		importPath, err := strconv.Unquote(spec.Path.Value)
		if err != nil || importPath == "" {
			continue
		}
		name := ""
		if spec.Name != nil {
			name = spec.Name.Name
		}
		specs = append(specs, GoImportSpec{Name: name, Path: importPath})
	}
	return specs
}

// GoImportPaths returns the import paths GoImportSpecs reads from a parsed Go file, in source
// order. Blank, dot and renamed imports count like plain ones: each makes go build read the
// imported package. The needs import scan and replacement plan and the DevContainer
// bootstrap closure read paths through it.
func GoImportPaths(file *ast.File) []string {
	specs := GoImportSpecs(file)
	paths := make([]string, 0, len(specs))
	for _, spec := range specs {
		paths = append(paths, spec.Path)
	}
	return paths
}

// ModuleImportDir reports whether importPath names a package of the module modulePath and,
// if so, that package's directory relative to the module root, slash-separated: "." for the
// module path itself. The comparison is boundary-aware, so a sibling module that only shares
// a textual prefix (example.com/foo-plugins beside example.com/foo) is outside. An empty
// module path owns no package, and a path with a trailing slash names none: the go command
// rejects such an import path (golang.org/x/mod/module.CheckImportPath).
//
// It is the one module-boundary rule: the needs catalog match, module-root resolution,
// replacement plan and third-party test, the docs vocabulary tree and the DevContainer
// bootstrap closure all call it.
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
