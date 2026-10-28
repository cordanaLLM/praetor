// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package hiss

import (
	"go/ast"
	"go/token"
	"slices"
)

// goScope answers, during one walk of Go source, whether a name is bound at the node the walk
// has reached. A bare call to a package-level name, or a selector on an imported package name,
// reaches the package-level binding only when no binding of the same name is in scope there.
// A binding that is not in scope hides nothing: one declared in a sibling block, or after the
// call, leaves the call reaching the function.
//
// The rules are Go's ("Declarations and scope" in the language specification):
//   - a function's receiver, type parameters, parameters and named results are in scope in its
//     body, and a function literal's parameters and results in the literal's body;
//   - a variable or constant declared inside a function, by var, const or a short variable
//     declaration, is in scope from the end of its declaration to the end of the innermost
//     block holding it, so `g := g()` still calls the function g;
//   - a type declared inside a function is in scope from its name on;
//   - every block, every if, for, range, switch, type switch and select statement and every
//     case or communication clause is a block of its own, so a variable its header declares
//     goes out of scope with it;
//   - a range clause's variables are in scope in its body, not in its range expression;
//   - a label is not a binding.
//
// A driver calls enter as its walk enters a node and leave as the walk leaves it, after the
// node's children. Each visible name is counted once per binding, so a lookup is one map read
// and a whole function costs one walk however many calls it makes. Checking each call by
// walking the whole body again was quadratic in the function's size, and it ignored scope.
type goScope struct {
	frames  []scopeFrame
	visible map[string]int
	// nodes is inspect's ancestor stack; consumers can distinguish names from references.
	nodes []ast.Node
	// steps counts the nodes inspect has visited, so a caller can prove one walk per function
	// (TestCallGraphScansCallsInLinearTime) without timing it.
	steps int
}

// scopeFrame is one open scope: the node whose extent it covers and the names bound in it.
type scopeFrame struct {
	node     ast.Node
	names    []string
	bindings map[string]*ast.Ident
}

// resolve returns the declaration visible at this point, without go/types or imports.
func (s *goScope) resolve(name string) *ast.Ident {
	for i := len(s.frames) - 1; i >= 0; i-- {
		if ident := s.frames[i].bindings[name]; ident != nil {
			return ident
		}
	}
	return nil
}

// reference excludes declarations, selector members and labels. A := left operand
// is a write only when that binding already exists in the current declaration scope.
func (s *goScope) reference(id *ast.Ident) bool {
	switch p := s.nodes[len(s.nodes)-1].(type) {
	case *ast.AssignStmt:
		return s.assignmentReference(id, p)
	case *ast.ValueSpec:
		return !slices.Contains(p.Names, id)
	case *ast.Field:
		return !slices.Contains(p.Names, id)
	case *ast.SelectorExpr:
		return p.Sel != id
	case *ast.TypeSpec:
		return p.Name != id
	case *ast.LabeledStmt:
		return p.Label != id
	case *ast.BranchStmt:
		return p.Label != id
	case *ast.RangeStmt:
		return rangeReference(id, p)
	}
	return true
}

func (s *goScope) assignmentReference(id *ast.Ident, p *ast.AssignStmt) bool {
	return p.Tok != token.DEFINE || !slices.Contains(p.Lhs, ast.Expr(id)) || s.local(id.Name) != nil
}

func rangeReference(id *ast.Ident, p *ast.RangeStmt) bool {
	return p.Tok != token.DEFINE || (p.Key != id && p.Value != id)
}

// local returns a binding in the current declaration scope. Function parameters and
// results share the body's declaration scope for short variable redeclarations.
func (s *goScope) local(name string) *ast.Ident {
	last := len(s.frames) - 1
	if last < 0 {
		return nil
	}
	if ident := s.frames[last].bindings[name]; ident != nil {
		return ident
	}
	if last > 0 {
		var body *ast.BlockStmt
		switch fn := s.frames[last-1].node.(type) {
		case *ast.FuncDecl:
			body = fn.Body
		case *ast.FuncLit:
			body = fn.Body
		}
		if body != nil && body == s.frames[last].node {
			return s.frames[last-1].bindings[name]
		}
	}
	return nil
}

// binds reports whether a binding of name is in scope at the node the walk has reached.
func (s *goScope) binds(name string) bool {
	return s.visible[name] > 0
}

// count returns how many bindings of name are in scope at the node the walk has reached. One
// means only the outermost binding, such as a method's receiver or a function's parameter, is
// visible; more mean a nested binding hides it.
func (s *goScope) count(name string) int {
	return s.visible[name]
}

// bindingEnd returns where a binding made at the node the walk has reached goes out of scope:
// the innermost open scope for a declaration, and the innermost function for a plain
// assignment, which writes a variable declared there or further out. It returns NoPos outside
// every function, where nothing a walk tracks is bound.
func (s *goScope) bindingEnd(declares bool) token.Pos {
	for i := len(s.frames) - 1; i >= 0; i-- {
		node := s.frames[i].node
		switch node.(type) {
		case *ast.FuncDecl, *ast.FuncLit:
			return node.End()
		}
		if declares {
			return node.End()
		}
	}
	return token.NoPos
}

// enter opens the scope n begins and binds what is in scope from its start: a signature's
// names, a range clause's variables as the walk reaches the body, and a local type's name.
func (s *goScope) enter(n ast.Node) {
	switch x := n.(type) {
	case *ast.FuncDecl:
		s.open(n)
		s.declareFields(x.Recv)
		s.declareSignature(x.Type)
	case *ast.FuncLit:
		s.open(n)
		s.declareSignature(x.Type)
	case *ast.BlockStmt:
		s.declareRangeVars(x)
		s.open(n)
	case *ast.IfStmt, *ast.ForStmt, *ast.RangeStmt, *ast.SwitchStmt, *ast.TypeSwitchStmt,
		*ast.SelectStmt, *ast.CaseClause, *ast.CommClause:
		s.open(n)
	case *ast.TypeSpec:
		s.declare(x.Name)
	}
}

// leave binds what a declaration makes visible once it ends, then closes the scope n opened.
// An if, for, switch or type switch header's short variable declaration, and a communication
// clause's, ends inside the statement's own scope, so it binds there.
func (s *goScope) leave(n ast.Node) {
	switch x := n.(type) {
	case *ast.AssignStmt:
		if x.Tok == token.DEFINE {
			s.declareExprs(x.Lhs)
		}
	case *ast.ValueSpec:
		s.declareIdents(x.Names)
	}
	if last := len(s.frames) - 1; last >= 0 && s.frames[last].node == n {
		s.close()
	}
}

// inspect walks root once, calling visit on each node in the scope in force where that node
// begins, before the node opens a scope of its own. A node visit refuses is skipped with its
// children and its bindings. So is a node deeper than maxNodeStack (HISS-02); in a scanned file
// that depth also truncates the scanner's own walk, which records the file as a lower bound.
func (s *goScope) inspect(root ast.Node, visit func(ast.Node) bool) {
	ast.Inspect(root, func(n ast.Node) bool {
		if n == nil {
			if last := len(s.nodes) - 1; last >= 0 {
				s.leave(s.nodes[last])
				s.nodes = s.nodes[:last]
			}
			return true
		}
		if len(s.nodes) >= maxNodeStack || !visit(n) {
			return false
		}
		s.steps++
		s.enter(n)
		s.nodes = append(s.nodes, n)
		return true
	})
}

// bindsAt reports whether a binding of name made inside fn is in scope at pos, a position in
// fn. It walks fn once, up to pos; a nil fn binds nothing.
func bindsAt(fn *ast.FuncDecl, name string, pos token.Pos) bool {
	if fn == nil {
		return false
	}
	var s goScope
	found, reached := false, false
	s.inspect(fn, func(n ast.Node) bool {
		if !reached && n.Pos() >= pos {
			found, reached = s.binds(name), true
		}
		return !reached
	})
	return found
}

func (s *goScope) open(n ast.Node) {
	s.frames = append(s.frames, scopeFrame{node: n})
}

// close ends the innermost scope and releases every name bound in it.
func (s *goScope) close() {
	last := len(s.frames) - 1
	names := s.frames[last].names
	s.frames = s.frames[:last]
	for i := 0; i < len(names); i++ {
		if s.visible[names[i]]--; s.visible[names[i]] <= 0 {
			delete(s.visible, names[i])
		}
	}
}

// declareRangeVars binds a range clause's variables as the walk enters the loop's body. The
// walk has already left the range expression, where they are not in scope.
func (s *goScope) declareRangeVars(body *ast.BlockStmt) {
	last := len(s.frames) - 1
	if last < 0 {
		return
	}
	loop, ok := s.frames[last].node.(*ast.RangeStmt)
	if !ok || loop.Body != body || loop.Tok != token.DEFINE {
		return
	}
	s.declareExprs([]ast.Expr{loop.Key, loop.Value})
}

// declareSignature binds a function's type parameters, parameters and named results.
func (s *goScope) declareSignature(ft *ast.FuncType) {
	if ft == nil {
		return
	}
	s.declareFields(ft.TypeParams)
	s.declareFields(ft.Params)
	s.declareFields(ft.Results)
}

// declareFields binds the names of a receiver, type parameter, parameter or result list.
func (s *goScope) declareFields(list *ast.FieldList) {
	if list == nil {
		return
	}
	for i := 0; i < len(list.List); i++ {
		s.declareIdents(list.List[i].Names)
	}
}

// declareExprs binds each expression that is an identifier: the left of a short variable
// declaration, or a range clause's key and value.
func (s *goScope) declareExprs(exprs []ast.Expr) {
	for i := 0; i < len(exprs); i++ {
		if ident, ok := exprs[i].(*ast.Ident); ok {
			s.declare(ident)
		}
	}
}

func (s *goScope) declareIdents(idents []*ast.Ident) {
	for i := 0; i < len(idents); i++ {
		s.declare(idents[i])
	}
}

// declare binds one name in the innermost open scope. Outside every function nothing is open,
// and a package-level declaration is the binding a call reaches, not one that hides it. The
// blank identifier binds nothing.
func (s *goScope) declare(ident *ast.Ident) {
	last := len(s.frames) - 1
	if last < 0 || ident == nil || ident.Name == "_" {
		return
	}
	if s.local(ident.Name) != nil {
		return
	}
	if s.visible == nil {
		s.visible = make(map[string]int)
	}
	if s.frames[last].bindings == nil {
		s.frames[last].bindings = make(map[string]*ast.Ident)
	}
	s.frames[last].bindings[ident.Name] = ident
	s.visible[ident.Name]++
	s.frames[last].names = append(s.frames[last].names, ident.Name)
}
