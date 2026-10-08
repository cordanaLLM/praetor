// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package hiss

import (
	"go/ast"
	"go/token"
)

// A log/slog sink method takes a context only for its values (contextMethods), so it counts as a
// sink only on a receiver proven to be a *slog.Logger. loggerProof is that proof, and the walk
// (go_io.go checkContextSink) and the callee walk (go_io_callee.go) both feed it the nodes they
// visit, so a logger is proven the same way in the function that holds the finding and in every
// callee the package pass follows (#841).
//
// A receiver is proven when it is
//
//   - a log/slog constructor's result (loggerSources), or a With or WithGroup of a proven logger;
//   - an identifier holding one: a parameter or a variable declared *slog.Logger, or a local
//     bound to a proven logger, within the scope of that binding;
//   - a field of a parameter or receiver declared with a named type of the package, T or *T,
//     when the package pass finds the field declared *slog.Logger in T's struct type
//     (go_io_index.go loggerField). The walk cannot see T's declaration, so it defers that field.
//
// Every other receiver is not a logger. A name rebound to anything else, a range variable of the
// same name, and a function literal's parameter of the same name all end what was proven of it.

// loggerType is log/slog's Logger, whose pointer receives the sink methods contextMethods lists.
var loggerType = map[goFunc]struct{}{{"log/slog", "Logger"}: {}}

// loggerSources are the log/slog functions that return a *slog.Logger.
var loggerSources = map[goFunc]struct{}{
	{"log/slog", "Default"}: {},
	{"log/slog", "New"}:     {},
	{"log/slog", "With"}:    {},
}

// loggerDerivations are the methods of *slog.Logger that return another *slog.Logger.
var loggerDerivations = map[string]struct{}{"With": {}, "WithGroup": {}}

// isLoggerPointer reports whether expr spells *slog.Logger through the file's imports im.
func isLoggerPointer(im GoImports, expr ast.Expr) bool {
	star, ok := expr.(*ast.StarExpr)
	return ok && namesType(im, star.X, loggerType)
}

// sinkKind is what loggerProof decides about a call.
type sinkKind int

const (
	// notSink is a call that is no log/slog sink method on a proven logger.
	notSink sinkKind = iota
	// provenSink is a sink method on a receiver the walk proves a *slog.Logger.
	provenSink
	// fieldSink is a sink method on a field the package pass must find declared *slog.Logger.
	fieldSink
)

// sinkVerdict is loggerProof's decision about one call; Struct and Field name the deferred field
// of a fieldSink.
type sinkVerdict struct {
	kind          sinkKind
	Struct, Field string
}

// typedName is a parameter or receiver declared with a named type of the package.
type typedName struct {
	typ string
	end token.Pos
}

// loggerProof tracks, through one function of one file, which identifiers hold a *slog.Logger and
// which hold a value of a named type of the package.
type loggerProof struct {
	im    GoImports
	scope *goScope
	// held maps each identifier holding a *slog.Logger to the end of its binding's scope.
	held map[string]token.Pos
	// typed maps each parameter or receiver declared T or *T, for a type T of the package, to T
	// and the end of its function.
	typed map[string]typedName
}

// newLoggerProof starts a proof for a file with imports im, walked under scope.
func newLoggerProof(im GoImports, scope *goScope) loggerProof {
	return loggerProof{im: im, scope: scope, held: make(map[string]token.Pos), typed: make(map[string]typedName)}
}

// track updates the proof for n, the node a walk has reached, before n opens its own scope.
func (l *loggerProof) track(n ast.Node) {
	switch node := n.(type) {
	case *ast.FuncDecl:
		l.enterFunc(node)
	case *ast.FuncLit:
		l.bindFields(node.Type.Params, node.End(), l.scope.binds)
	case *ast.AssignStmt:
		l.assign(node.Lhs, node.Rhs, node.Tok == token.DEFINE)
	case *ast.ValueSpec:
		l.spec(node)
	case *ast.RangeStmt:
		l.forget(node.Key)
		l.forget(node.Value)
	}
}

// enterFunc starts a function declaration with nothing proven but its receiver and parameters.
func (l *loggerProof) enterFunc(fn *ast.FuncDecl) {
	clear(l.held)
	clear(l.typed)
	typeParams := typeParamNames(fn)
	isTypeParam := func(name string) bool { return typeParams[name] }
	if fn.Recv != nil && len(fn.Recv.List) == 1 && len(fn.Recv.List[0].Names) == 1 {
		if base, ok := ReceiverTypeName(fn.Recv.List[0].Type); ok {
			l.typed[fn.Recv.List[0].Names[0].Name] = typedName{typ: base, end: fn.End()}
		}
	}
	l.bindFields(fn.Type.Params, fn.End(), isTypeParam)
}

// typeParamNames returns the type parameters of fn and of its receiver's type.
func typeParamNames(fn *ast.FuncDecl) map[string]bool {
	names := make(map[string]bool)
	if fn.Type.TypeParams != nil {
		fields := fn.Type.TypeParams.List
		for i := 0; i < len(fields); i++ {
			for j := 0; j < len(fields[i].Names); j++ {
				names[fields[i].Names[j].Name] = true
			}
		}
	}
	if fn.Recv != nil && len(fn.Recv.List) > 0 {
		recv := fn.Recv.List[0].Type
		if star, ok := recv.(*ast.StarExpr); ok {
			recv = star.X
		}
		addReceiverTypeParams(names, recv)
	}
	return names
}

// addReceiverTypeParams adds the type parameters a generic receiver type T[A, B] names.
func addReceiverTypeParams(names map[string]bool, recv ast.Expr) {
	var indices []ast.Expr
	switch t := recv.(type) {
	case *ast.IndexExpr:
		indices = []ast.Expr{t.Index}
	case *ast.IndexListExpr:
		indices = t.Indices
	}
	for i := 0; i < len(indices); i++ {
		if ident, ok := indices[i].(*ast.Ident); ok {
			names[ident.Name] = true
		}
	}
}

// bindFields binds each parameter of list until end: a *slog.Logger one as a logger, one declared
// T or *T as typed, for a T that hidden does not report as a type parameter or a local type.
// Every other parameter ends what was proven of its name.
func (l *loggerProof) bindFields(list *ast.FieldList, end token.Pos, hidden func(string) bool) {
	if list == nil {
		return
	}
	for i := 0; i < len(list.List); i++ {
		field := list.List[i]
		logger := isLoggerPointer(l.im, field.Type)
		typ, typed := packageTypeName(field.Type)
		typed = typed && !hidden(typ)
		for j := 0; j < len(field.Names); j++ {
			name := field.Names[j].Name
			delete(l.held, name)
			delete(l.typed, name)
			switch {
			case logger:
				l.held[name] = end
			case typed:
				l.typed[name] = typedName{typ: typ, end: end}
			}
		}
	}
}

// packageTypeName returns T for a type spelled T or *T: a type of the package or a predeclared
// one, which declares no struct the package pass finds.
func packageTypeName(expr ast.Expr) (string, bool) {
	if star, ok := expr.(*ast.StarExpr); ok {
		expr = star.X
	}
	ident, ok := expr.(*ast.Ident)
	if !ok {
		return "", false
	}
	return ident.Name, true
}

// assign records which names an assignment or declaration leaves holding a logger. Every value is
// read before any name is rebound, and a rebound name is no longer typed.
func (l *loggerProof) assign(lhs, rhs []ast.Expr, declares bool) {
	held := make([]bool, len(lhs))
	for i := 0; i < len(lhs); i++ {
		value := boundValue(lhs, rhs, i)
		held[i] = value != nil && l.expr(value)
	}
	for i := 0; i < len(lhs); i++ {
		ident, ok := lhs[i].(*ast.Ident)
		if !ok || ident.Name == "_" {
			continue
		}
		delete(l.typed, ident.Name)
		// A plain assignment to a name not proven before proves it only in the scope the
		// assignment is in: the variable may be declared wider, as another type, and hold
		// something else on the paths that skip this one.
		_, proven := l.held[ident.Name]
		bindTracked(l.held, ident.Name, held[i], declares || !proven, l.scope)
	}
}

// spec records a var declaration. A variable declared *slog.Logger holds a logger whatever its
// value.
func (l *loggerProof) spec(spec *ast.ValueSpec) {
	l.assign(identExprs(spec.Names), spec.Values, true)
	if spec.Type == nil || !isLoggerPointer(l.im, spec.Type) {
		return
	}
	for i := 0; i < len(spec.Names); i++ {
		bindTracked(l.held, spec.Names[i].Name, true, true, l.scope)
	}
}

// forget ends what was proven of a range clause's key or value.
func (l *loggerProof) forget(expr ast.Expr) {
	if ident, ok := expr.(*ast.Ident); ok {
		delete(l.held, ident.Name)
		delete(l.typed, ident.Name)
	}
}

// sink decides whether call is a log/slog sink method on a proven logger, on a field the package
// pass must prove, or neither.
func (l *loggerProof) sink(call *ast.CallExpr) sinkVerdict {
	sel, ok := ast.Unparen(call.Fun).(*ast.SelectorExpr)
	if !ok || !neutralMethod("log/slog", "Logger", sel.Sel.Name) {
		return sinkVerdict{kind: notSink}
	}
	if l.expr(sel.X) {
		return sinkVerdict{kind: provenSink}
	}
	field, ok := ast.Unparen(sel.X).(*ast.SelectorExpr)
	if !ok {
		return sinkVerdict{kind: notSink}
	}
	owner, ok := ast.Unparen(field.X).(*ast.Ident)
	if !ok {
		return sinkVerdict{kind: notSink}
	}
	bound, typed := l.typed[owner.Name]
	if !typed || owner.Pos() >= bound.end {
		return sinkVerdict{kind: notSink}
	}
	return sinkVerdict{kind: fieldSink, Struct: bound.typ, Field: field.Sel.Name}
}

// expr reports whether expr evaluates to a *slog.Logger the proof knows of, following With and
// WithGroup inward to their receiver. The loop is bounded by maxNodeStack.
func (l *loggerProof) expr(expr ast.Expr) bool {
	for depth := 0; depth < maxNodeStack; depth++ {
		switch e := ast.Unparen(expr).(type) {
		case *ast.Ident:
			end, held := l.held[e.Name]
			return held && e.Pos() < end
		case *ast.CallExpr:
			if _, _, local, ok := resolvePackageCall(l.im, e.Fun, loggerSources); ok {
				return !l.scope.binds(local)
			}
			parent, derives := loggerDerivation(e)
			if !derives {
				return false
			}
			expr = parent
		default:
			return false
		}
	}
	return false
}

// loggerDerivation returns the receiver of a With or WithGroup method call.
func loggerDerivation(call *ast.CallExpr) (ast.Expr, bool) {
	sel, ok := ast.Unparen(call.Fun).(*ast.SelectorExpr)
	if !ok {
		return nil, false
	}
	_, derives := loggerDerivations[sel.Sel.Name]
	return sel.X, derives
}

// identExprs returns names as expressions, the left side of a var declaration.
func identExprs(names []*ast.Ident) []ast.Expr {
	exprs := make([]ast.Expr, 0, len(names))
	for i := 0; i < len(names); i++ {
		exprs = append(exprs, names[i])
	}
	return exprs
}

// bindTracked records in tracked whether name is now set, and until where. A declaration lives
// to the end of the innermost enclosing scope. A plain assignment writes a variable declared
// earlier: one already tracked keeps its scope, and any other is taken to live to the end of
// the enclosing function, since a same-named variable of a narrower scope would have been
// shadowed by it. At package level nothing is tracked (goScope.bindingEnd).
func bindTracked(tracked map[string]token.Pos, name string, set, declares bool, scope *goScope) {
	if !set {
		delete(tracked, name)
		return
	}
	end := scope.bindingEnd(declares)
	if prior, ok := tracked[name]; ok && !declares {
		end = prior
	}
	if end == token.NoPos {
		return
	}
	tracked[name] = end
}
