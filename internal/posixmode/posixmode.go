// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

// Package posixmode finds Go code that decides on a POSIX permission mode where POSIX modes may
// not exist (HISS-21, #1374). Windows synthesises a file's mode from its read-only attribute, so
// a comparison of Mode().Perm() with 0o700 or 0o600 can never hold there: in a file built for
// every platform such a check refuses with a mode the platform cannot produce, or it skips a
// protection without saying so. #132 found two by running on Windows; this package finds them by
// reading the source.
//
// The engine runs the matcher over its own tree (TestRepositoryHasNoUntaggedPosixModeComparison);
// a later unit lifts it unchanged into internal/hiss as the adopter-facing HISS-21 rule.
package posixmode

import (
	"go/ast"
	"go/build/constraint"
	"go/token"
	"path"
	"strings"
)

// Finding is one comparison of a permission mode with a literal, in a file every platform builds.
type Finding struct {
	// Pos is the comparison's position.
	Pos token.Position
	// Func names the enclosing function, "Type.Method" for a method, "" at package level: the
	// key an allow-list names a reviewed use by, which survives edits to the lines around it.
	Func string
}

// goosSuffixes are the GOOS values a file name suffix restricts a file to (go help buildconstraint).
var goosSuffixes = map[string]bool{
	"aix": true, "android": true, "darwin": true, "dragonfly": true, "freebsd": true, "hurd": true,
	"illumos": true, "ios": true, "js": true, "linux": true, "nacl": true, "netbsd": true,
	"openbsd": true, "plan9": true, "solaris": true, "wasip1": true, "windows": true, "zos": true,
}

// Applies reports whether the matcher reads file, a slash path: a Go file that is no test and
// that neither its name (a _<goos>.go or _<goos>_<goarch>.go suffix) nor a //go:build line
// restricts, so every platform builds it.
func Applies(name string, file *ast.File) bool {
	base := path.Base(name)
	if !strings.HasSuffix(base, ".go") || strings.HasSuffix(base, "_test.go") {
		return false
	}
	parts := strings.Split(strings.TrimSuffix(base, ".go"), "_")
	for i := max(1, len(parts)-2); i < len(parts); i++ {
		if goosSuffixes[parts[i]] {
			return false
		}
	}
	for _, group := range file.Comments {
		if group.Pos() >= file.Package {
			break
		}
		for _, comment := range group.List {
			if constraint.IsGoBuild(comment.Text) {
				return false
			}
		}
	}
	return true
}

// Comparisons returns every == or != in file between an integer literal and a permission mode:
// a call of Perm() with no arguments, or such a call masked with & or &^ by a literal, as in
// info.Mode().Perm() != 0o700 or info.Mode().Perm()&0o100 == 0. A comparison with a variable,
// such as the tightened mode perm & info.Mode().Perm(), records or tightens a mode and is not
// read. Callers decide with Applies whether the file is one every platform builds.
func Comparisons(fset *token.FileSet, file *ast.File) []Finding {
	var findings []Finding
	for _, decl := range file.Decls {
		name := declName(decl)
		ast.Inspect(decl, func(node ast.Node) bool {
			compare, ok := node.(*ast.BinaryExpr)
			if !ok || (compare.Op != token.EQL && compare.Op != token.NEQ) {
				return true
			}
			if (isMode(compare.X) && isIntLiteral(compare.Y)) || (isMode(compare.Y) && isIntLiteral(compare.X)) {
				findings = append(findings, Finding{Pos: fset.Position(compare.OpPos), Func: name})
			}
			return true
		})
	}
	return findings
}

// declName names a declaration's function, "Type.Method" for a method; "" for any other
// declaration.
func declName(decl ast.Decl) string {
	fn, ok := decl.(*ast.FuncDecl)
	if !ok {
		return ""
	}
	if fn.Recv == nil || len(fn.Recv.List) == 0 {
		return fn.Name.Name
	}
	receiver := fn.Recv.List[0].Type
	if star, isPointer := receiver.(*ast.StarExpr); isPointer {
		receiver = star.X
	}
	if generic, isGeneric := receiver.(*ast.IndexExpr); isGeneric {
		receiver = generic.X
	}
	if ident, isIdent := receiver.(*ast.Ident); isIdent {
		return ident.Name + "." + fn.Name.Name
	}
	return fn.Name.Name
}

// isMode reports whether expr is a Perm() call, or one masked with & or &^ by a literal.
func isMode(expr ast.Expr) bool {
	expr = ast.Unparen(expr)
	if call, ok := expr.(*ast.CallExpr); ok {
		selector, isSelector := call.Fun.(*ast.SelectorExpr)
		return isSelector && selector.Sel.Name == "Perm" && len(call.Args) == 0
	}
	mask, ok := expr.(*ast.BinaryExpr)
	if !ok || (mask.Op != token.AND && mask.Op != token.AND_NOT) {
		return false
	}
	return (isMode(mask.X) && isIntLiteral(mask.Y)) || (isMode(mask.Y) && isIntLiteral(mask.X))
}

// isIntLiteral reports whether expr is an integer literal.
func isIntLiteral(expr ast.Expr) bool {
	literal, ok := ast.Unparen(expr).(*ast.BasicLit)
	return ok && literal.Kind == token.INT
}
