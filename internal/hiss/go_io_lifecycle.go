// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package hiss

import (
	"go/ast"
	"go/token"
)

// A service started by a framework's lifecycle runs for the lifetime the framework owns: a
// leader-election loop, a controller manager or a subscription must not time out, so the
// context it runs under is cancelled when the lifecycle stops instead of carrying a deadline.
// That is the reasoning behind the main.main exemption, applied to a framework to which main
// hands the process lifetime (#841).
//
// The accepted shape is exact. A context bound with its cancel function by context.WithCancel
// or WithCancelCause (cancelPairs) is lifecycle-owned where it is used inside the start function
// literal of a lifecycleHooks composite literal whose stop function literal calls that cancel
// function as one of its own top-level statements. Anywhere else the context is what it was: a
// use in the constructor body runs before the lifecycle starts and is reported, as is a use in
// the stop function, which runs under the framework's own stop deadline.

// lifecycleHook is a framework type whose stop function the framework runs when the lifetime it
// owns ends.
type lifecycleHook struct {
	// Start and Stop are the fields of a composite literal of the type that hold the start and
	// stop functions.
	Start, Stop string
	// Evidence cites the framework source that runs Stop once Start has run.
	Evidence string
}

// lifecycleHooks are the framework shapes HISS-02 accepts as owning a context's lifetime, keyed
// by import path and type name. A new entry carries the source it cites (docs/standards/
// hiss-rule-matching.md says how to propose one); a type that is not listed owns nothing.
var lifecycleHooks = map[goFunc]lifecycleHook{
	{"go.uber.org/fx", "Hook"}: {
		Start: "OnStart",
		Stop:  "OnStop",
		Evidence: "go.uber.org/fx v1.24.0: lifecycle.go declares Hook{OnStart, OnStop func(context.Context) error}; " +
			"internal/lifecycle/lifecycle.go Lifecycle.Stop runs OnStop backward from the last successful OnStart",
	},
}

// lifecycleBounded reports whether arg is a context a lifecycle owns where the walk is: it comes
// from an identifier paired with a cancel function (pairCancel) and the walk is inside the start
// function of a hook whose stop function calls that cancel function.
func (g *goScanner) lifecycleBounded(arg ast.Expr) bool {
	ident, _ := g.contextOrigin(arg)
	if ident == nil {
		return false
	}
	cancel, paired := g.lifecycle[ident.Name]
	return paired && g.inLifecycleStart(cancel)
}

// lifecycleOwner returns the cancel function paired with the context value comes from, so a
// context derived from a lifecycle-owned one by WithValue stays paired.
func (g *goScanner) lifecycleOwner(value ast.Expr) (string, bool) {
	if value == nil {
		return "", false
	}
	ident, _ := g.contextOrigin(value)
	if ident == nil {
		return "", false
	}
	cancel, paired := g.lifecycle[ident.Name]
	return cancel, paired
}

// pairCancel records the context and the cancel function one assignment binds from a
// cancelPairs derivation, when the context holds no deadline.
func (g *goScanner) pairCancel(lhs, rhs []ast.Expr) {
	if len(lhs) != 2 || len(rhs) != 1 {
		return
	}
	ctx, ctxOK := lhs[0].(*ast.Ident)
	cancel, cancelOK := lhs[1].(*ast.Ident)
	call, callOK := ast.Unparen(rhs[0]).(*ast.CallExpr)
	if !ctxOK || !cancelOK || !callOK || cancel.Name == "_" {
		return
	}
	if _, free := g.freeContexts[ctx.Name]; !free {
		return
	}
	if _, _, local, ok := resolvePackageCall(g.imports, call.Fun, cancelPairs); ok && !g.shadowed(local) {
		g.lifecycle[ctx.Name] = cancel.Name
	}
}

// forgetLifecycle drops the pairing name held, as a context or as a cancel function, once name
// is rebound.
func (g *goScanner) forgetLifecycle(name string) {
	delete(g.lifecycle, name)
	for ctx, cancel := range g.lifecycle {
		if cancel == name {
			delete(g.lifecycle, ctx)
		}
	}
}

// inLifecycleStart reports whether the walk is inside the start function literal of a
// lifecycleHooks composite literal whose stop function calls cancel. The walk stack holds only
// ancestors of the current node, so this is a bounded scan (maxNodeStack).
func (g *goScanner) inLifecycleStart(cancel string) bool {
	for i := len(g.stack) - 1; i >= 2; i-- {
		lit, isLit := g.stack[i].(*ast.FuncLit)
		field, isField := g.stack[i-1].(*ast.KeyValueExpr)
		hook, isHook := g.stack[i-2].(*ast.CompositeLit)
		if !isLit || !isField || !isHook || field.Value != lit {
			continue
		}
		shape, known := g.lifecycleHook(hook)
		if known && isKey(field.Key, shape.Start) && stopCalls(hook, shape.Stop, cancel) {
			return true
		}
	}
	return false
}

// lifecycleHook resolves the type of a composite literal to a lifecycleHooks entry that no
// local shadows.
func (g *goScanner) lifecycleHook(hook *ast.CompositeLit) (lifecycleHook, bool) {
	_, shape, local, ok := resolvePackageCall(g.imports, hook.Type, lifecycleHooks)
	if !ok || g.shadowed(local) {
		return lifecycleHook{}, false
	}
	return shape, true
}

// isKey reports whether a composite literal key is the field name.
func isKey(key ast.Expr, name string) bool {
	ident, ok := key.(*ast.Ident)
	return ok && ident.Name == name
}

// stopCalls reports whether the hook's stop field holds a function literal that calls cancel.
func stopCalls(hook *ast.CompositeLit, stop, cancel string) bool {
	for i := 0; i < len(hook.Elts); i++ {
		field, ok := hook.Elts[i].(*ast.KeyValueExpr)
		if !ok || !isKey(field.Key, stop) {
			continue
		}
		lit, ok := field.Value.(*ast.FuncLit)
		return ok && callsAtTop(lit, cancel)
	}
	return false
}

// callsAtTop reports whether one of lit's own top-level statements calls name, directly or by
// defer, before any statement rebinds it. A call in a nested block or closure may never run, and
// a parameter of that name is another function, so neither counts.
func callsAtTop(lit *ast.FuncLit, name string) bool {
	if lit.Body == nil || paramNamed(lit.Type, name) {
		return false
	}
	for i := 0; i < len(lit.Body.List); i++ {
		stmt := lit.Body.List[i]
		if declaresName(stmt, name) {
			return false
		}
		if callsName(stmt, name) {
			return true
		}
	}
	return false
}

// paramNamed reports whether ft declares a parameter or result called name.
func paramNamed(ft *ast.FuncType, name string) bool {
	if ft == nil {
		return false
	}
	lists := []*ast.FieldList{ft.Params, ft.Results}
	for i := 0; i < len(lists); i++ {
		if lists[i] != nil && fieldsName(lists[i].List, name) {
			return true
		}
	}
	return false
}

// fieldsName reports whether any field of a list binds name.
func fieldsName(fields []*ast.Field, name string) bool {
	for i := 0; i < len(fields); i++ {
		for j := 0; j < len(fields[i].Names); j++ {
			if fields[i].Names[j].Name == name {
				return true
			}
		}
	}
	return false
}

// declaresName reports whether stmt declares name by a short variable declaration or a var.
func declaresName(stmt ast.Stmt, name string) bool {
	switch s := stmt.(type) {
	case *ast.AssignStmt:
		return s.Tok == token.DEFINE && exprsName(s.Lhs, name)
	case *ast.DeclStmt:
		gen, ok := s.Decl.(*ast.GenDecl)
		return ok && gen.Tok == token.VAR && specsName(gen.Specs, name)
	}
	return false
}

// exprsName reports whether any expression is the identifier name.
func exprsName(exprs []ast.Expr, name string) bool {
	for i := 0; i < len(exprs); i++ {
		if ident, ok := exprs[i].(*ast.Ident); ok && ident.Name == name {
			return true
		}
	}
	return false
}

// specsName reports whether any value spec of a var declaration names name.
func specsName(specs []ast.Spec, name string) bool {
	for i := 0; i < len(specs); i++ {
		spec, ok := specs[i].(*ast.ValueSpec)
		if !ok {
			continue
		}
		for j := 0; j < len(spec.Names); j++ {
			if spec.Names[j].Name == name {
				return true
			}
		}
	}
	return false
}

// callsName reports whether stmt is a call of the bare identifier name, as a statement or
// deferred.
func callsName(stmt ast.Stmt, name string) bool {
	var call *ast.CallExpr
	switch s := stmt.(type) {
	case *ast.ExprStmt:
		if expr, ok := s.X.(*ast.CallExpr); ok {
			call = expr
		}
	case *ast.DeferStmt:
		call = s.Call
	}
	if call == nil {
		return false
	}
	ident, ok := ast.Unparen(call.Fun).(*ast.Ident)
	return ok && ident.Name == name
}
