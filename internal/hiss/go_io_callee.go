// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package hiss

import (
	"go/ast"
)

// A caller may pass context.Background() to a function of its own module that derives its own
// WithTimeout from configuration before any I/O. The deadline is real, but it lives in the
// callee, which a one-function walk cannot see (#841). The package pass follows the context into
// such a callee and accepts the call when the callee bounds it.
//
// A callee bounds a context parameter when every use of the parameter, in source order, is one
// of these, and nothing else mentions it:
//
//   - the argument of a WithTimeout or WithDeadline derivation (contextFuncs ctxBounded); when
//     that derivation is a top-level statement rebinding the parameter itself,
//     `ctx, cancel := context.WithTimeout(ctx, d)`, every later use is of the bounded context and
//     the walk stops there;
//   - the argument of a function that performs no I/O on it (contextFuncs ctxNeutral, the
//     log/slog functions among them) or the receiver of a context.Context method
//     (contextMethods), which is how a select over channels reads it;
//   - the argument of a log/slog sink method on a receiver the logger proof accepts
//     (loggerProof, shared with the walk of the function that holds the finding), or on a field of
//     a parameter the package pass finds declared *slog.Logger;
//   - the argument of another call the pass resolves (callSite.resolve, moduleIndex.classify): an
//     allow-listed function at a checked version, or a function of the same module, which must
//     bound its own parameter in turn. A call that spreads its last argument maps the others one
//     to one; only an argument in the spread position is not followed.
//
// Any other mention fails the callee: an inheriting derivation, an assignment, a return, a
// comparison, a call the pass cannot resolve, a function outside the module. A parameter that is
// unnamed or blank is never used, so it bounds trivially; a variadic one is not followed.
//
// The walk holds no recursion (HISS-01): each proof explores the callees with an explicit stack,
// at most maxCalleeDepth calls deep from the call that recorded the finding and over at most
// maxCalleeNodes callees in all, across every argument of that call (HISS-02), and a callee
// already on the stack fails the proof.

const (
	// maxCalleeDepth bounds how many module-local calls deep one proof follows a context: the
	// callee the finding's call names is depth 1.
	maxCalleeDepth = 4
	// maxCalleeNodes bounds the callees one proof visits, shared by every argument it follows.
	maxCalleeNodes = 64
	// maxCalleeSteps bounds the steps of one proof's walk over its stack.
	maxCalleeSteps = 4096
)

// calleeKey is one parameter of one function, by position.
type calleeKey struct {
	entry *funcEntry
	param int
}

// calleeAnalysis is what one function does with one context parameter: the callee parameters it
// hands it to, when ok, or a use that may reach I/O.
type calleeAnalysis struct {
	deps []calleeKey
	ok   bool
}

// calleeFrame is one callee on a proof's explicit stack.
type calleeFrame struct {
	key      calleeKey
	depth    int
	deps     []calleeKey
	next     int
	analyzed bool
}

// boundedAll reports whether every callee parameter in deps bounds its context.
// The callees they reach count toward one budget of maxCalleeNodes for the whole proof.
func (x *moduleIndex) boundedAll(deps []calleeKey) bool {
	visits := 0
	for i := 0; i < len(deps); i++ {
		if !x.boundedFrom(deps[i], &visits) {
			return false
		}
	}
	return true
}

// boundedFrom reports whether start bounds its context, following the callees it hands it to
// depth-first with an explicit stack. Every callee must bound it, so the first one that does not,
// a callee past maxCalleeDepth, a cycle, or a walk past its node or step bound fails the proof.
// visits counts the callees the proof has visited so far, across every start it was called for.
func (x *moduleIndex) boundedFrom(start calleeKey, visits *int) bool {
	stack := []calleeFrame{{key: start, depth: 1}}
	*visits++
	for step := 0; step < maxCalleeSteps && len(stack) > 0; step++ {
		top := &stack[len(stack)-1]
		if !top.analyzed {
			analysis := x.analysis(top.key)
			if !analysis.ok {
				return false
			}
			top.deps, top.analyzed = analysis.deps, true
		}
		if top.next >= len(top.deps) {
			stack = stack[:len(stack)-1]
			continue
		}
		dep := top.deps[top.next]
		top.next++
		if top.depth >= maxCalleeDepth || *visits >= maxCalleeNodes || onCalleeStack(stack, dep) {
			return false
		}
		*visits++
		stack = append(stack, calleeFrame{key: dep, depth: top.depth + 1})
	}
	return len(stack) == 0
}

// onCalleeStack reports whether key is already being followed.
func onCalleeStack(stack []calleeFrame, key calleeKey) bool {
	for i := 0; i < len(stack); i++ {
		if stack[i].key == key {
			return true
		}
	}
	return false
}

// analysis returns what key's function does with its parameter, analysing it once per pass.
func (x *moduleIndex) analysis(key calleeKey) calleeAnalysis {
	if cached, done := x.analyses[key]; done {
		return cached
	}
	result := x.analyzeParam(key)
	x.analyses[key] = result
	return result
}

// analyzeParam walks key's function for the uses of its parameter (mentionWalk).
func (x *moduleIndex) analyzeParam(key calleeKey) calleeAnalysis {
	decl := key.entry.decl
	name, typ, ok := paramAt(decl.Type, key.param)
	switch {
	case !ok:
		return calleeAnalysis{}
	case name == "" || name == "_":
		return calleeAnalysis{ok: true}
	case decl.Body == nil || !namesType(key.entry.im, typ, contextType):
		return calleeAnalysis{}
	}
	walk := &mentionWalk{
		x: x, entry: key.entry, param: name,
		top: make(map[ast.Stmt]bool), accounted: make(map[*ast.Ident]bool),
	}
	walk.logs = newLoggerProof(key.entry.im, &walk.scope)
	return walk.run()
}

// paramAt returns the name and declared type of the parameter at index, "" for an unnamed one.
// It reports false past the last parameter and at a variadic one.
func paramAt(ft *ast.FuncType, index int) (string, ast.Expr, bool) {
	if ft == nil || ft.Params == nil || index < 0 {
		return "", nil, false
	}
	at := 0
	for i := 0; i < len(ft.Params.List); i++ {
		field := ft.Params.List[i]
		if _, variadic := field.Type.(*ast.Ellipsis); variadic {
			return "", nil, false
		}
		width := max(len(field.Names), 1)
		if index >= at+width {
			at += width
			continue
		}
		if len(field.Names) == 0 {
			return "", field.Type, true
		}
		return field.Names[index-at].Name, field.Type, true
	}
	return "", nil, false
}

// mentionWalk classifies every mention of one context parameter in one function body.
type mentionWalk struct {
	x     *moduleIndex
	entry *funcEntry
	param string
	scope goScope
	// logs is the logger proof the main walk uses, fed the nodes of this function (go_io_logger.go).
	logs loggerProof
	// top holds the body's own statements, where a bounded rebinding ends the walk.
	top map[ast.Stmt]bool
	// accounted holds the identifiers a classified use already explains.
	accounted map[*ast.Ident]bool
	deps      []calleeKey
	failed    bool
	done      bool
}

// run walks the function once (goScope.inspect) and reports what it found.
func (w *mentionWalk) run() calleeAnalysis {
	body := w.entry.decl.Body
	for i := 0; i < len(body.List); i++ {
		w.top[body.List[i]] = true
	}
	w.scope.inspect(w.entry.decl, w.visit)
	if w.failed {
		return calleeAnalysis{}
	}
	return calleeAnalysis{deps: w.deps, ok: true}
}

// visit classifies one node. A call or selector is seen before the identifiers inside it, so a
// use it explains is accounted before the identifier is reached; an identifier of the parameter
// that nothing accounted for fails the walk.
func (w *mentionWalk) visit(n ast.Node) bool {
	if w.failed || w.done || w.signature(n) {
		return false
	}
	w.logs.track(n)
	if stmt, ok := n.(ast.Stmt); ok && w.top[stmt] && w.rebindsBounded(stmt) {
		w.done = true
		return false
	}
	w.note(n)
	return !w.failed
}

// note accounts the uses a call or selector explains, and fails the walk at an identifier of
// the parameter nothing accounted for.
func (w *mentionWalk) note(n ast.Node) {
	switch node := n.(type) {
	case *ast.CallExpr:
		w.accountCall(node)
	case *ast.SelectorExpr:
		w.accountSelector(node)
	case *ast.Ident:
		if node.Name == w.param && w.isParam() && !w.accounted[node] {
			w.failed = true
		}
	}
}

// signature reports whether n is the function's name, receiver or signature, which declare
// names rather than use them; the scope already binds them as it enters the declaration.
func (w *mentionWalk) signature(n ast.Node) bool {
	decl := w.entry.decl
	return n == ast.Node(decl.Name) || n == ast.Node(decl.Recv) || n == ast.Node(decl.Type)
}

// isParam reports whether the parameter is the only binding of its name in scope here.
func (w *mentionWalk) isParam() bool {
	return w.scope.count(w.param) == 1
}

// rebindsBounded reports whether stmt is `p, cancel := context.WithTimeout(p, d)` (or `=`, or
// WithDeadline) for the parameter p.
func (w *mentionWalk) rebindsBounded(stmt ast.Stmt) bool {
	assign, ok := stmt.(*ast.AssignStmt)
	if !ok || len(assign.Lhs) != 2 || len(assign.Rhs) != 1 || !w.isParam() {
		return false
	}
	lhs, ok := assign.Lhs[0].(*ast.Ident)
	return ok && lhs.Name == w.param && w.boundsParam(assign.Rhs[0])
}

// boundsParam reports whether expr is a WithTimeout or WithDeadline derivation of the parameter.
func (w *mentionWalk) boundsParam(expr ast.Expr) bool {
	call, ok := ast.Unparen(expr).(*ast.CallExpr)
	if !ok || len(call.Args) == 0 {
		return false
	}
	arg, ok := ast.Unparen(call.Args[0]).(*ast.Ident)
	effect, known := w.contextEffect(call)
	return ok && arg.Name == w.param && known && effect == ctxBounded
}

// contextEffect resolves a call to a contextFuncs entry no binding shadows.
func (w *mentionWalk) contextEffect(call *ast.CallExpr) (contextEffect, bool) {
	_, effect, local, ok := resolvePackageCall(w.entry.im, call.Fun, contextFuncs)
	return effect, ok && w.scope.count(local) == 0
}

// accountCall accounts each argument of call that is the parameter and a use call explains.
func (w *mentionWalk) accountCall(call *ast.CallExpr) {
	for i := 0; i < len(call.Args); i++ {
		arg, ok := ast.Unparen(call.Args[i]).(*ast.Ident)
		if ok && arg.Name == w.param && w.isParam() && w.accepts(call, i) {
			w.accounted[arg] = true
		}
	}
}

// accepts reports whether handing the parameter to call as argument arg is a use that bounds it,
// does no I/O with it, or reaches a callee the proof follows next (deps). A call that spreads its
// last argument maps every other argument to its own parameter; only the spread one is not followed.
func (w *mentionWalk) accepts(call *ast.CallExpr, arg int) bool {
	if effect, known := w.contextEffect(call); known {
		return effect == ctxNeutral || (effect == ctxBounded && arg == 0)
	}
	sink := w.logs.sink(call)
	if sink.kind == provenSink {
		return true
	}
	if spreads(call, arg) {
		return false
	}
	decl := w.entry.decl
	site := newCallSite(w.entry.im, w.entry.dir, w.entry.pkg, decl, w.scope.count)
	target, ok := site.resolve(call, sink)
	if !ok {
		return false
	}
	deps, proven := w.x.classify(target, []int{arg})
	w.deps = append(w.deps, deps...)
	return proven
}

// accountSelector accounts a selector's field or method name, which is no variable, and the
// parameter as the receiver of a context.Context method that only reads it.
func (w *mentionWalk) accountSelector(sel *ast.SelectorExpr) {
	w.accounted[sel.Sel] = true
	x, ok := sel.X.(*ast.Ident)
	if ok && x.Name == w.param && w.isParam() && neutralMethod("context", "Context", sel.Sel.Name) {
		w.accounted[x] = true
	}
}
