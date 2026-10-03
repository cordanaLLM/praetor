// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package hiss

import (
	"go/ast"
	"go/token"
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
	// steps counts the nodes inspect has visited, so a caller can prove one walk per function
	// (TestCallGraphScansCallsInLinearTime) without timing it.
	steps int
}

// scopeFrame is one open scope: the node whose extent it covers and the names bound in it.
type scopeFrame struct {
	node  ast.Node
	names []string
}

// binds reports whether a binding of name is in scope at the node the walk has reached.
func (s *goScope) binds(name string) bool {
	return s.visible[name] > 0
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
	var stack []ast.Node
	ast.Inspect(root, func(n ast.Node) bool {
		if n == nil {
			if last := len(stack) - 1; last >= 0 {
				s.leave(stack[last])
				stack = stack[:last]
			}
			return true
		}
		if len(stack) >= maxNodeStack || !visit(n) {
			return false
		}
		s.steps++
		s.enter(n)
		stack = append(stack, n)
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
	if s.visible == nil {
		s.visible = make(map[string]int)
	}
	s.visible[ident.Name]++
	s.frames[last].names = append(s.frames[last].names, ident.Name)
}
