// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package hiss

import (
	"go/ast"
	"go/token"
	"slices"
	"strings"
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
//
// The cancel function may be stored before it is called: `cancel = runCancel` for a variable and
// `h.drainCancel = runCancel` for a field of a name (storeCancel), and the stop function then
// calls the place it was stored in. The call must be reachable on every path: no statement before
// it may return, except the guard `if cancel == nil { return }` on the very place it calls, which
// returns only when nothing was stored. A place stored in more than maxCancelAliases times, a
// place overwritten or whose holder is reassigned before the context is used, and a stop function
// that reaches the cancel function through a call (`return h.stop(ctx)`) are not followed.
//
// Known gaps, all on the side of accepting: a place overwritten after the context was used, a
// cancel place a different function than OnStop shares the name of, and a blocking OnStart that
// uses the lifecycle context synchronously (`return mgr.Ping(ctx)`). The framework never runs the
// stop function of a hook whose start function did not return, so a start that hangs is never
// cancelled; the shape is accepted anyway because the rule cannot tell a hang from a long start.

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

// lifecyclePair is the set of places that hold the cancel function of one lifecycle context:
// the variable context.WithCancel returned it in, and every variable or struct field the walk saw
// it stored in (`cancel = runCancel`, `h.drainCancel = cancel`). Each is a cancelKey.
type lifecyclePair struct {
	cancels []string
}

// maxCancelAliases bounds how many places one pairing follows a cancel function to (HISS-02); a
// store past it is not followed, so the context it would have covered stays reported.
const maxCancelAliases = 8

// cancelKey spells where a cancel function is held: a bare name, or a field of a bare name
// (`h.drainCancel`). Any other expression holds nothing the walk follows.
func cancelKey(expr ast.Expr) (string, bool) {
	switch e := ast.Unparen(expr).(type) {
	case *ast.Ident:
		return e.Name, e.Name != "_"
	case *ast.SelectorExpr:
		base, ok := ast.Unparen(e.X).(*ast.Ident)
		if !ok || base.Name == "_" {
			return "", false
		}
		return base.Name + "." + e.Sel.Name, true
	}
	return "", false
}

// keyBase returns the name a cancelKey starts with.
func keyBase(key string) string {
	base, _, _ := strings.Cut(key, ".")
	return base
}

// lifecycleBounded reports whether arg is a context a lifecycle owns where the walk is: it comes
// from an identifier paired with a cancel function (pairCancel) and the walk is inside the start
// function of a hook whose stop function calls one of the places that holds that function.
func (g *goScanner) lifecycleBounded(arg ast.Expr) bool {
	ident, _ := g.contextOrigin(arg)
	if ident == nil {
		return false
	}
	pair, paired := g.lifecycle[ident.Name]
	return paired && g.inLifecycleStart(pair.cancels)
}

// lifecycleOwner returns the pairing of the context value comes from, so a context derived from
// a lifecycle-owned one by WithValue stays paired.
func (g *goScanner) lifecycleOwner(value ast.Expr) (lifecyclePair, bool) {
	if value == nil {
		return lifecyclePair{}, false
	}
	ident, _ := g.contextOrigin(value)
	if ident == nil {
		return lifecyclePair{}, false
	}
	pair, paired := g.lifecycle[ident.Name]
	return pair, paired
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
		g.lifecycle[ctx.Name] = lifecyclePair{cancels: []string{cancel.Name}}
	}
}

// storeCancel follows a cancel function into the variable or field an assignment stores it in
// (`cancel = runCancel`, `h.drainCancel = cancel`): every pairing that holds the stored function
// gains the place it was stored in. A field that receives anything else stops holding what it
// held before, so a later call of it proves nothing; a bare name was forgotten when it was rebound
// (trackContextBinding).
func (g *goScanner) storeCancel(lhs, rhs []ast.Expr) {
	for i := 0; i < len(lhs); i++ {
		dst, ok := cancelKey(lhs[i])
		if !ok {
			continue
		}
		src := ""
		if value := boundValue(lhs, rhs, i); value != nil {
			src, _ = cancelKey(value)
		}
		holders := g.cancelHolders(src)
		if strings.Contains(dst, ".") {
			g.forgetLifecycle(dst)
		}
		for j := 0; j < len(holders); j++ {
			pair := g.lifecycle[holders[j]]
			if len(pair.cancels) < maxCancelAliases {
				g.lifecycle[holders[j]] = lifecyclePair{cancels: append(slices.Clone(pair.cancels), dst)}
			}
		}
	}
}

// cancelHolders returns the contexts whose pairing holds the cancel function at key.
func (g *goScanner) cancelHolders(key string) []string {
	var held []string
	if key == "" {
		return held
	}
	for ctx, pair := range g.lifecycle {
		if slices.Contains(pair.cancels, key) {
			held = append(held, ctx)
		}
	}
	return held
}

// forgetLifecycle drops the pairing name held, as a context or as a cancel function or a field of
// one, once name is rebound; a pairing left with no place that holds its cancel function goes too.
func (g *goScanner) forgetLifecycle(name string) {
	delete(g.lifecycle, name)
	for ctx, pair := range g.lifecycle {
		kept := make([]string, 0, len(pair.cancels))
		for i := 0; i < len(pair.cancels); i++ {
			if pair.cancels[i] != name && !strings.HasPrefix(pair.cancels[i], name+".") {
				kept = append(kept, pair.cancels[i])
			}
		}
		if len(kept) == 0 {
			delete(g.lifecycle, ctx)
			continue
		}
		g.lifecycle[ctx] = lifecyclePair{cancels: kept}
	}
}

// inLifecycleStart reports whether the walk is inside the start function literal of a
// lifecycleHooks composite literal whose stop function calls one of the cancel places. The walk
// stack holds only ancestors of the current node, so this is a bounded scan (maxNodeStack).
func (g *goScanner) inLifecycleStart(cancels []string) bool {
	for i := len(g.stack) - 1; i >= 2; i-- {
		lit, isLit := g.stack[i].(*ast.FuncLit)
		field, isField := g.stack[i-1].(*ast.KeyValueExpr)
		hook, isHook := g.stack[i-2].(*ast.CompositeLit)
		if !isLit || !isField || !isHook || field.Value != lit {
			continue
		}
		shape, known := g.lifecycleHook(hook)
		if known && isKey(field.Key, shape.Start) && stopCalls(hook, shape.Stop, cancels) {
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

// stopCalls reports whether the hook's stop field holds a function literal that calls one of the
// cancel places.
func stopCalls(hook *ast.CompositeLit, stop string, cancels []string) bool {
	for i := 0; i < len(hook.Elts); i++ {
		field, ok := hook.Elts[i].(*ast.KeyValueExpr)
		if !ok || !isKey(field.Key, stop) {
			continue
		}
		lit, ok := field.Value.(*ast.FuncLit)
		if !ok {
			return false
		}
		for j := 0; j < len(cancels); j++ {
			if callsAtTop(lit, cancels[j]) {
				return true
			}
		}
		return false
	}
	return false
}

// callsAtTop reports whether one of lit's own top-level statements calls key, directly or by
// defer, before any statement rebinds it and before any statement that may return. A call in a
// nested block or closure may never run, and so may one after an earlier conditional return: the
// error path of `if err := srv.Shutdown(c); err != nil { return err }` skips it; the one return
// that cannot skip it is the nil guard of the cancel place itself (returnsWhenNil). A parameter of
// the base name is another function, so it does not count either.
func callsAtTop(lit *ast.FuncLit, key string) bool {
	base := keyBase(key)
	if lit.Body == nil || paramNamed(lit.Type, base) {
		return false
	}
	for i := 0; i < len(lit.Body.List); i++ {
		stmt := lit.Body.List[i]
		if declaresName(stmt, base) || assignsKey(stmt, key) {
			return false
		}
		if callsKey(stmt, key) {
			return true
		}
		if mayReturn(stmt) && !returnsWhenNil(stmt, key) {
			return false
		}
	}
	return false
}

// returnsWhenNil reports whether stmt is `if key == nil { return ... }` with no init statement,
// no else branch and nothing else in the block: it returns only when no cancel function was ever
// stored, so there is nothing to cancel and the call after it cannot be skipped. A guard on another
// place, a negated test and a block that does more are conditional returns like any other.
func returnsWhenNil(stmt ast.Stmt, key string) bool {
	guard, ok := stmt.(*ast.IfStmt)
	if !ok || guard.Init != nil || guard.Else != nil || !onlyReturns(guard.Body) {
		return false
	}
	cond, ok := ast.Unparen(guard.Cond).(*ast.BinaryExpr)
	if !ok || cond.Op != token.EQL {
		return false
	}
	return (isCancelKey(cond.X, key) && isNil(cond.Y)) || (isNil(cond.X) && isCancelKey(cond.Y, key))
}

// onlyReturns reports whether block holds exactly one statement, a return.
func onlyReturns(block *ast.BlockStmt) bool {
	if block == nil || len(block.List) != 1 {
		return false
	}
	_, isReturn := block.List[0].(*ast.ReturnStmt)
	return isReturn
}

// isCancelKey reports whether expr spells key.
func isCancelKey(expr ast.Expr, key string) bool {
	got, ok := cancelKey(expr)
	return ok && got == key
}

// isNil reports whether expr is the predeclared nil as the file spells it.
func isNil(expr ast.Expr) bool {
	ident, ok := ast.Unparen(expr).(*ast.Ident)
	return ok && ident.Name == "nil"
}

// mayReturn reports whether stmt holds a return statement outside any function literal.
func mayReturn(stmt ast.Stmt) bool {
	found := false
	ast.Inspect(stmt, func(n ast.Node) bool {
		switch n.(type) {
		case *ast.FuncLit:
			return false
		case *ast.ReturnStmt:
			found = true
		}
		return !found
	})
	return found
}

// assignsKey reports whether stmt is a plain assignment to key, or to the name a key field
// belongs to.
func assignsKey(stmt ast.Stmt, key string) bool {
	assign, ok := stmt.(*ast.AssignStmt)
	if !ok || assign.Tok == token.DEFINE {
		return false
	}
	for i := 0; i < len(assign.Lhs); i++ {
		if lhs, ok := cancelKey(assign.Lhs[i]); ok && (lhs == key || lhs == keyBase(key)) {
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

// callsKey reports whether stmt is a call of the function held at key, as a statement or
// deferred.
func callsKey(stmt ast.Stmt, key string) bool {
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
	callee, ok := cancelKey(call.Fun)
	return ok && callee == key
}
