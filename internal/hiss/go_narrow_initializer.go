// SPDX-FileCopyrightText: 2026 Lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package hiss

import (
	"go/ast"
	"go/token"
)

type narrowProof struct {
	scope        goScope
	constants    map[*ast.Ident]bool
	packageNames map[string]bool
}

// narrowInitializers proves syntax-only candidates before collecting declarations.
// All values of a declaration must pass: splitting a mixed declaration can reorder
// its effects. Unresolved names and imported selectors remain unchecked.
func narrowInitializers(fn *ast.FuncDecl, file *ast.File) map[ast.Node]bool {
	p := narrowProof{constants: make(map[*ast.Ident]bool), packageNames: narrowPackageNames(file)}
	movable := make(map[ast.Node]bool)
	p.scope.inspect(fn, func(n ast.Node) bool {
		switch decl := n.(type) {
		case *ast.GenDecl:
			if decl.Tok == token.CONST {
				for i := 0; i < len(decl.Specs); i++ {
					for _, id := range narrowSpecNames(decl.Specs[i]) {
						p.constants[id] = true
					}
				}
			}
		case *ast.AssignStmt:
			movable[decl] = p.safe(decl.Rhs)
		case *ast.ValueSpec:
			movable[decl] = p.safe(decl.Values)
		}
		return true
	})
	return movable
}

func narrowPackageNames(file *ast.File) map[string]bool {
	names := map[string]bool{"true": true, "false": true}
	if file == nil {
		return names
	}
	for i := 0; i < len(file.Decls); i++ {
		switch decl := file.Decls[i].(type) {
		case *ast.GenDecl:
			for j := 0; j < len(decl.Specs); j++ {
				for _, id := range narrowSpecNames(decl.Specs[j]) {
					names[id.Name] = decl.Tok == token.CONST
				}
			}
		case *ast.FuncDecl:
			if decl.Recv == nil {
				names[decl.Name.Name] = false
			}
		}
	}
	return names
}

func narrowSpecNames(spec ast.Spec) []*ast.Ident {
	switch s := spec.(type) {
	case *ast.ValueSpec:
		return s.Names
	case *ast.TypeSpec:
		return []*ast.Ident{s.Name}
	case *ast.ImportSpec:
		if s.Name != nil {
			return []*ast.Ident{s.Name}
		}
	}
	return nil
}

// safe uses a bounded worklist rather than recursion. Composite types are syntax,
// not evaluated values; keys and values must both pass the expression allow-list.
func (p *narrowProof) safe(values []ast.Expr) bool {
	pending := append([]ast.Expr(nil), values...)
	for steps := 0; steps < maxNodeStack && len(pending) > 0; steps++ {
		last := len(pending) - 1
		parts, ok := p.parts(pending[last])
		if !ok {
			return false
		}
		pending = append(pending[:last], parts...)
	}
	return len(pending) == 0
}

func (p *narrowProof) parts(expr ast.Expr) ([]ast.Expr, bool) {
	switch e := expr.(type) {
	case *ast.BasicLit:
		return nil, true
	case *ast.Ident:
		if decl := p.scope.resolve(e.Name); decl != nil {
			return nil, p.constants[decl]
		}
		return nil, p.packageNames[e.Name]
	case *ast.CompositeLit:
		return e.Elts, true
	case *ast.KeyValueExpr:
		return []ast.Expr{e.Key, e.Value}, true
	case *ast.ParenExpr:
		return []ast.Expr{e.X}, true
	case *ast.UnaryExpr:
		return []ast.Expr{e.X}, narrowConstantUnary(e.Op)
	}
	return nil, false
}

func narrowConstantUnary(op token.Token) bool {
	switch op {
	case token.ADD, token.SUB, token.XOR, token.NOT:
		return true
	}
	return false
}
