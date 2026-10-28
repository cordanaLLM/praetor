// SPDX-FileCopyrightText: 2026 Lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package hiss

import (
	"fmt"
	"go/ast"
	"go/token"
)

type narrowLocal struct {
	decl  *ast.Ident
	block ast.Node
	keep  bool
}

// checkNarrowScope covers function-top var and := locals with movable initializers.
// Calls and other ordered expressions are not candidates. Any read or write outside
// one child block keeps the declaration; address-taking and captures are conservative
// exemptions. Nested declarations and closure-only uses remain explicit coverage gaps.
func (g *goScanner) checkNarrowScope(fn *ast.FuncDecl) {
	if fn.Body == nil {
		return
	}
	file, ok := g.stack[0].(*ast.File)
	if !ok {
		return
	}
	locals := functionTopLocals(fn.Body, narrowInitializers(fn, file))
	if len(locals) == 0 {
		return
	}
	byDecl := make(map[*ast.Ident]*narrowLocal, len(locals))
	for i := 0; i < len(locals); i++ {
		byDecl[locals[i].decl] = &locals[i]
	}
	var scope goScope
	scope.inspect(fn, func(n ast.Node) bool {
		trackNarrowUse(&scope, n, byDecl)
		return true
	})
	owner := ""
	if fn.Recv != nil && len(fn.Recv.List) > 0 {
		owner, _ = ReceiverTypeName(fn.Recv.List[0].Type)
	}
	name := qualifiedName(owner, fn.Name.Name)
	for i := 0; i < len(locals); i++ {
		local := locals[i]
		if local.keep || local.block == nil {
			continue
		}
		line := g.fset.Position(local.decl.Pos()).Line
		g.record("HISS-05", local.decl.Pos(), name,
			fmt.Sprintf("%s: '%s' line %d; declare in %s block line %d",
				name, local.decl.Name, line, narrowBlockKind(local.block), g.fset.Position(local.block.Pos()).Line))
	}
}

func functionTopLocals(body *ast.BlockStmt, movable map[ast.Node]bool) []narrowLocal {
	var locals []narrowLocal
	for i := 0; i < len(body.List); i++ {
		switch stmt := body.List[i].(type) {
		case *ast.AssignStmt:
			if stmt.Tok == token.DEFINE && movable[stmt] {
				locals = appendShortLocals(locals, stmt.Lhs)
			}
		case *ast.DeclStmt:
			locals = appendVarLocals(locals, stmt, movable)
		}
	}
	return locals
}

func appendShortLocals(locals []narrowLocal, lhs []ast.Expr) []narrowLocal {
	for i := 0; i < len(lhs); i++ {
		if id, ok := lhs[i].(*ast.Ident); ok && id.Name != "_" {
			locals = append(locals, narrowLocal{decl: id})
		}
	}
	return locals
}

func appendVarLocals(locals []narrowLocal, stmt *ast.DeclStmt, movable map[ast.Node]bool) []narrowLocal {
	gen, ok := stmt.Decl.(*ast.GenDecl)
	if !ok || gen.Tok != token.VAR {
		return locals
	}
	for i := 0; i < len(gen.Specs); i++ {
		if spec, ok := gen.Specs[i].(*ast.ValueSpec); ok && movable[spec] {
			for j := 0; j < len(spec.Names); j++ {
				if id := spec.Names[j]; id.Name != "_" {
					locals = append(locals, narrowLocal{decl: id})
				}
			}
		}
	}
	return locals
}

func trackNarrowUse(scope *goScope, n ast.Node, locals map[*ast.Ident]*narrowLocal) {
	keepAddressedLocal(scope, n, locals)
	id, ok := n.(*ast.Ident)
	if !ok || len(scope.nodes) == 0 || !scope.reference(id) {
		return
	}
	local := locals[scope.resolve(id.Name)]
	if local == nil || local.decl == id {
		return
	}
	block := narrowUseBlock(scope)
	if block == nil || (local.block != nil && local.block != block) {
		local.keep = true
	}
	local.block = block
}

func keepAddressedLocal(scope *goScope, n ast.Node, locals map[*ast.Ident]*narrowLocal) {
	if unary, ok := n.(*ast.UnaryExpr); ok && unary.Op == token.AND {
		if id, ok := ast.Unparen(unary.X).(*ast.Ident); ok {
			if local := locals[scope.resolve(id.Name)]; local != nil {
				local.keep = true
			}
		}
	}
}

// The first frame after the function and its body is the outermost child block,
// including its header. Captured bindings are exempt even inside that child.
func narrowUseBlock(scope *goScope) ast.Node {
	for i := 2; i < len(scope.frames); i++ {
		if _, capture := scope.frames[i].node.(*ast.FuncLit); capture {
			return nil
		}
	}
	if len(scope.frames) < 3 {
		return nil
	}
	return scope.frames[2].node
}

func narrowBlockKind(n ast.Node) string {
	switch n.(type) {
	case *ast.IfStmt:
		return "if"
	case *ast.ForStmt:
		return "for"
	case *ast.RangeStmt:
		return "range"
	case *ast.SwitchStmt, *ast.TypeSwitchStmt:
		return "switch"
	case *ast.SelectStmt:
		return "select"
	case *ast.CaseClause:
		return "case"
	case *ast.CommClause:
		return "communication case"
	default:
		return "plain"
	}
}
