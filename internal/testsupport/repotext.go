// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package testsupport

import (
	"fmt"
	"go/ast"
	"go/token"
	"os"
	"strconv"
	"strings"
	"testing"
)

// ReadRepoText reads a checked-in text file and returns it with LF line endings.
//
// A Windows checkout with core.autocrlf=true writes text files with CRLF, so a test that
// splits or searches checked-in text on "\n" finds nothing there and passes on Linux and
// macOS only (#847). Tests that inspect repository text read it through this helper.
func ReadRepoText(t testing.TB, path string) string {
	t.Helper()
	// #nosec G304 -- path is a repository file the calling test names.
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read repository text %s: %v", path, err)
	}
	return strings.ReplaceAll(string(data), "\r\n", "\n")
}

// lineSearchFuncs are the strings and bytes functions whose literal "\n" argument frames a
// line: on CRLF text each of them misses or splits wrongly.
var lineSearchFuncs = map[string]bool{
	"Split": true, "SplitN": true, "SplitAfter": true, "Contains": true, "Index": true,
	"LastIndex": true, "Count": true, "HasPrefix": true, "HasSuffix": true, "Cut": true,
	"TrimSuffix": true, "TrimPrefix": true,
}

// lineEndingNormalisers are call-name prefixes that make a function's repository text
// LF-safe. A strings or bytes ReplaceAll of a "\r\n" literal counts as well.
var lineEndingNormalisers = []string{"NormalizeLineEndings", "ReadRepoText"}

// LFAssumptionFindings reports each function of a test file that reads a file above its
// package (os.ReadFile on a filepath.Join or literal path starting with "..") and then
// searches or splits text on a literal containing "\n" without normalising line endings
// first. The check is lexical per function: a normaliser call anywhere in the function
// clears it, so a deliberate exception reads through ReadRepoText or names its normaliser.
func LFAssumptionFindings(fset *token.FileSet, file *ast.File) []string {
	var findings []string
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Body == nil {
			continue
		}
		var readsAbove, normalises bool
		var search token.Pos
		ast.Inspect(fn.Body, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			pkg, name := callName(call)
			switch {
			case pkg == "os" && name == "ReadFile" && len(call.Args) == 1 && pathAbovePackage(call.Args[0]):
				readsAbove = true
			case isNormaliser(name), name == "ReplaceAll" && hasLiteral(call.Args, "\r\n"):
				normalises = true
			case (pkg == "strings" || pkg == "bytes") && lineSearchFuncs[name] && hasLiteral(call.Args, "\n"):
				if search == token.NoPos {
					search = call.Pos()
				}
			}
			return true
		})
		if readsAbove && search != token.NoPos && !normalises {
			findings = append(findings, fmt.Sprintf("%s: %s reads repository text and searches it on \"\\n\" without testsupport.ReadRepoText",
				fset.Position(search), fn.Name.Name))
		}
	}
	return findings
}

func isNormaliser(name string) bool {
	for _, prefix := range lineEndingNormalisers {
		if strings.HasPrefix(name, prefix) {
			return true
		}
	}
	return false
}

func callName(call *ast.CallExpr) (pkg, name string) {
	switch fun := call.Fun.(type) {
	case *ast.SelectorExpr:
		if ident, ok := fun.X.(*ast.Ident); ok {
			return ident.Name, fun.Sel.Name
		}
		return "", fun.Sel.Name
	case *ast.Ident:
		return "", fun.Name
	}
	return "", ""
}

// pathAbovePackage reports whether expr is a literal path or filepath.Join whose first
// element climbs out of the package directory.
func pathAbovePackage(expr ast.Expr) bool {
	if call, ok := expr.(*ast.CallExpr); ok {
		pkg, name := callName(call)
		if pkg == "filepath" && name == "Join" && len(call.Args) > 0 {
			expr = call.Args[0]
		}
	}
	text, ok := stringLiteral(expr)
	return ok && (text == ".." || strings.HasPrefix(text, "../"))
}

func hasLiteral(args []ast.Expr, part string) bool {
	for _, arg := range args {
		if text, ok := stringLiteral(arg); ok && strings.Contains(text, part) {
			return true
		}
	}
	return false
}

func stringLiteral(expr ast.Expr) (string, bool) {
	lit, ok := expr.(*ast.BasicLit)
	if !ok || lit.Kind != token.STRING {
		return "", false
	}
	text, err := strconv.Unquote(lit.Value)
	return text, err == nil
}
