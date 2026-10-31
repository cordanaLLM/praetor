// SPDX-FileCopyrightText: 2026 Lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package hiss

import (
	"go/ast"
	"go/token"
)

// keepNarrowState extends the address exemption using the same declaration lookup.
// Without a dominance/data-flow proof, even an unconditional per-pass reset stays
// exempt. A method receiver cannot be proven read-only from syntax alone.
func keepNarrowState(scope *goScope, n ast.Node, locals map[*ast.Ident]*narrowLocal) {
	if unary, ok := n.(*ast.UnaryExpr); ok && unary.Op == token.AND {
		keepNarrowExpr(scope, unary.X, locals)
	}
	if !narrowInLoop(scope, n) {
		return
	}
	switch stmt := n.(type) {
	case *ast.AssignStmt:
		keepNarrowAssignment(scope, stmt, locals)
	case *ast.IncDecStmt:
		keepNarrowExpr(scope, stmt.X, locals)
	case *ast.RangeStmt:
		if stmt.Tok == token.ASSIGN {
			keepNarrowExpr(scope, stmt.Key, locals)
			keepNarrowExpr(scope, stmt.Value, locals)
		}
	case *ast.CallExpr:
		keepNarrowExpr(scope, narrowCallMutation(stmt), locals)
	}
}

func keepNarrowAssignment(scope *goScope, stmt *ast.AssignStmt, locals map[*ast.Ident]*narrowLocal) {
	for _, lhs := range stmt.Lhs {
		if id, ok := lhs.(*ast.Ident); ok && stmt.Tok == token.DEFINE && scope.local(id.Name) == nil {
			continue // A fresh shadow does not mutate the outer declaration.
		}
		keepNarrowExpr(scope, lhs, locals)
	}
}

func narrowCallMutation(call *ast.CallExpr) ast.Expr {
	switch fn := ast.Unparen(call.Fun).(type) {
	case *ast.SelectorExpr:
		return fn.X
	case *ast.Ident:
		if (fn.Name == "delete" || fn.Name == "clear") && len(call.Args) > 0 {
			return call.Args[0]
		}
	}
	return nil
}

// Include conditions and post statements: they can also carry state between passes.
// The initializer and range expression execute once, outside the repeated region.
func narrowInLoop(scope *goScope, n ast.Node) bool {
	if loop, ok := n.(*ast.RangeStmt); ok && loop.Tok == token.ASSIGN {
		return true
	}
	for i := 2; i < len(scope.frames); i++ {
		switch loop := scope.frames[i].node.(type) {
		case *ast.ForStmt:
			if loop.Init == nil || n.Pos() >= loop.Init.End() {
				return true
			}
		case *ast.RangeStmt:
			if n.Pos() >= loop.Body.Pos() {
				return true
			}
		}
	}
	return false
}

func keepNarrowExpr(scope *goScope, expr ast.Expr, locals map[*ast.Ident]*narrowLocal) {
	if id := narrowStorageRoot(expr); id != nil {
		if local := locals[scope.resolve(id.Name)]; local != nil {
			local.keep = true
		}
	}
}

// Only the storage root is mutated: assigning xs[index] does not write index.
// Bounded peeling reuses the scanner's syntax-depth limit, without recursion.
func narrowStorageRoot(expr ast.Expr) *ast.Ident {
	for depth := 0; depth < maxNodeStack; depth++ {
		switch e := expr.(type) {
		case *ast.Ident:
			return e
		case *ast.ParenExpr:
			expr = e.X
		case *ast.SelectorExpr:
			expr = e.X
		case *ast.IndexExpr:
			expr = e.X
		case *ast.SliceExpr:
			expr = e.X
		case *ast.StarExpr:
			expr = e.X
		default:
			return nil
		}
	}
	return nil
}
