package hiss

import (
	"go/ast"
	"go/token"
)

// The I/O half of HISS-02 for Go: every I/O call carries an explicit context deadline.
// This syntax scanner cannot tell which callee performs I/O, so it decides the two shapes
// that are visible without type information:
//
//   - a context with no deadline passed to a call. context.Background and context.TODO
//     carry none, and neither does anything derived from them by WithCancel, WithValue or
//     signal.NotifyContext; context.WithoutCancel drops the deadline its parent had. A
//     WithTimeout or WithDeadline derivation bounds it. The context is followed through
//     the local variables of one function, each within the block that declares it; one
//     returned by a helper, kept in a field or held by a package variable is not.
//   - a standard-library call that has no context parameter at all, such as exec.Command,
//     net.Dial or http.Get, where a context-aware replacement exists. The table of these
//     calls is the one the language server's loop diagnostic reads too
//     (ResolveContextlessCall).
//
// Package names are resolved through the file's imports, so an aliased or dot import is
// the same call and a local of the same name is not. Test files are exempt, as they are
// from the other Go rules, and so is main.main: the entry point owns the process lifetime,
// and the root context it builds for a long-running server is that lifetime.

// goFunc names a package-level function or variable by import path, independent of how a
// file spells the package.
type goFunc struct {
	Path string
	Name string
}

// contextEffect is what a function of package context, or signal.NotifyContext, does to
// the deadline of the context it returns.
type contextEffect int

const (
	// ctxRoot returns a context that never had a deadline.
	ctxRoot contextEffect = iota
	// ctxBounded adds a deadline, so the result is bounded whatever its parent was.
	ctxBounded
	// ctxInherits keeps the parent's deadline, or the parent's lack of one.
	ctxInherits
	// ctxStrips returns a context with no deadline even when the parent had one.
	ctxStrips
	// ctxNeutral performs no I/O and returns no context.
	ctxNeutral
)

// contextFuncs classifies every function that derives or inspects a context. None of them
// performs I/O, so passing a deadline-free context to one is not itself a finding; what is
// derived from it is followed instead.
var contextFuncs = map[goFunc]contextEffect{
	{"context", "Background"}:        ctxRoot,
	{"context", "TODO"}:              ctxRoot,
	{"context", "WithTimeout"}:       ctxBounded,
	{"context", "WithTimeoutCause"}:  ctxBounded,
	{"context", "WithDeadline"}:      ctxBounded,
	{"context", "WithDeadlineCause"}: ctxBounded,
	{"context", "WithCancel"}:        ctxInherits,
	{"context", "WithCancelCause"}:   ctxInherits,
	{"context", "WithValue"}:         ctxInherits,
	{"os/signal", "NotifyContext"}:   ctxInherits,
	{"context", "WithoutCancel"}:     ctxStrips,
	{"context", "AfterFunc"}:         ctxNeutral,
	{"context", "Cause"}:             ctxNeutral,
}

// contextlessIO maps a standard-library call that takes no context to the call that
// replaces it with one. net.DialTimeout is absent because its timeout bounds the dial as a
// context would; os.ReadFile and the other calls without any context-aware form are absent
// because there is nothing the author could write instead.
var contextlessIO = map[goFunc]string{
	{"os/exec", "Command"}:     "exec.CommandContext",
	{"net", "Dial"}:            "net.Dialer.DialContext",
	{"net", "DialTCP"}:         "net.Dialer.DialContext",
	{"net", "DialUDP"}:         "net.Dialer.DialContext",
	{"net", "DialIP"}:          "net.Dialer.DialContext",
	{"net", "DialUnix"}:        "net.Dialer.DialContext",
	{"net/http", "Get"}:        "http.NewRequestWithContext",
	{"net/http", "Head"}:       "http.NewRequestWithContext",
	{"net/http", "Post"}:       "http.NewRequestWithContext",
	{"net/http", "PostForm"}:   "http.NewRequestWithContext",
	{"net/http", "NewRequest"}: "http.NewRequestWithContext",
}

// ContextlessCall is a standard-library I/O call that takes no context.
type ContextlessCall struct {
	// Name is the call as the standard library's documentation spells it: exec.Command.
	Name string
	// Replacement is the context-aware call that replaces it.
	Replacement string
	// Local is the identifier through which the file reached the package: its package
	// name, or the function itself under a dot import. A local binding of that name in the
	// enclosing function shadows the package, and the call then reaches the local instead.
	Local string
}

// ResolveContextlessCall reports whether fun, the callee of a call in a file whose imports
// are im, names a standard-library I/O call that takes no context. Package names are
// resolved through im, so an alias or a dot import is the same call and a selector on
// anything but an imported package is none; shadowing is left to the caller, which knows
// the enclosing scope.
func ResolveContextlessCall(im GoImports, fun ast.Expr) (ContextlessCall, bool) {
	fn, replacement, local, ok := resolvePackageCall(im, fun, contextlessIO)
	if !ok {
		return ContextlessCall{}, false
	}
	return ContextlessCall{Name: DefaultImportName(fn.Path) + "." + fn.Name, Replacement: replacement, Local: local}, true
}

// osExitFunc is the process exit the HISS-07 abort policy reports.
var osExitFunc = map[goFunc]struct{}{{"os", "Exit"}: {}}

// resolvePackageCall reports which table entry the callee fun names, and the identifier
// that reached the package: the package name of a selector, or the function itself under
// a dot import. A selector on anything but an imported package name resolves to nothing.
// Whether a local shadows that identifier is left to the caller. A package-level variable
// resolves the same way as a function, so AbortsHTTPResponse looks its sentinel up here too.
func resolvePackageCall[V any](im GoImports, fun ast.Expr, table map[goFunc]V) (goFunc, V, string, bool) {
	var zero V
	switch f := ast.Unparen(fun).(type) {
	case *ast.SelectorExpr:
		pkg, ok := f.X.(*ast.Ident)
		if !ok {
			return goFunc{}, zero, "", false
		}
		importPath, bound := im.Path(pkg.Name)
		fn := goFunc{Path: importPath, Name: f.Sel.Name}
		if value, known := table[fn]; bound && known {
			return fn, value, pkg.Name, true
		}
	case *ast.Ident:
		paths := im.DotPaths()
		for i := 0; i < len(paths); i++ {
			fn := goFunc{Path: paths[i], Name: f.Name}
			if value, known := table[fn]; known {
				return fn, value, f.Name, true
			}
		}
	}
	return goFunc{}, zero, "", false
}

// contextCall resolves a call to a function of contextFuncs that no local shadows.
func (g *goScanner) contextCall(call *ast.CallExpr) (contextEffect, bool) {
	_, effect, local, ok := resolvePackageCall(g.imports, call.Fun, contextFuncs)
	if !ok || g.shadowed(local) {
		return 0, false
	}
	return effect, true
}

// ioRuleApplies reports whether the I/O half of HISS-02 governs the current node.
func (g *goScanner) ioRuleApplies() bool {
	return !g.isTest && !g.inEntryPoint()
}

// deadlineFree reports whether expr evaluates to a context without a deadline, following
// inheriting derivations inward to their parent. The loop is bounded by maxNodeStack, which
// already bounds how deeply a file can nest them.
func (g *goScanner) deadlineFree(expr ast.Expr) bool {
	for depth := 0; depth < maxNodeStack; depth++ {
		switch e := ast.Unparen(expr).(type) {
		case *ast.Ident:
			end, free := g.freeContexts[e.Name]
			return free && e.Pos() < end
		case *ast.CallExpr:
			parent, free, decided := g.derivedDeadline(e)
			if decided {
				return free
			}
			expr = parent
		default:
			return false
		}
	}
	return false
}

// derivedDeadline decides whether a call returns a deadline-free context. An inheriting
// derivation is undecided until its parent is: it returns the parent to follow instead.
func (g *goScanner) derivedDeadline(call *ast.CallExpr) (parent ast.Expr, free, decided bool) {
	effect, ok := g.contextCall(call)
	if !ok {
		return nil, false, true
	}
	switch effect {
	case ctxRoot, ctxStrips:
		return nil, true, true
	case ctxInherits:
		if len(call.Args) == 0 {
			return nil, false, true
		}
		return call.Args[0], false, false
	default:
		return nil, false, true
	}
}

// forgetParams drops a function literal's parameters from the deadline-free set: inside
// the literal each name is the parameter, not the enclosing variable it may shadow. The
// enclosing variable stays forgotten after the literal too, which can miss a later use of
// it but never reports a parameter.
func (g *goScanner) forgetParams(lit *ast.FuncLit) {
	if lit.Type == nil || lit.Type.Params == nil {
		return
	}
	fields := lit.Type.Params.List
	for i := 0; i < len(fields); i++ {
		for j := 0; j < len(fields[i].Names); j++ {
			delete(g.freeContexts, fields[i].Names[j].Name)
		}
	}
}

// checkContextSink reports a deadline-free context passed to a call that is not itself a
// context derivation.
func (g *goScanner) checkContextSink(call *ast.CallExpr) {
	if !g.ioRuleApplies() {
		return
	}
	if _, derivation := g.contextCall(call); derivation {
		return
	}
	for i := 0; i < len(call.Args); i++ {
		if g.deadlineFree(call.Args[i]) {
			g.record("HISS-02", call.Args[i].Pos(), "",
				"Context without a deadline reaches a call; derive it with context.WithTimeout or context.WithDeadline")
			return
		}
	}
}

// checkContextlessIO reports a standard-library I/O call that takes no context.
func (g *goScanner) checkContextlessIO(call *ast.CallExpr) {
	if !g.ioRuleApplies() {
		return
	}
	found, ok := ResolveContextlessCall(g.imports, call.Fun)
	if !ok || g.shadowed(found.Local) {
		return
	}
	g.record("HISS-02", call.Pos(), "",
		found.Name+" takes no context, so no deadline bounds it; use "+found.Replacement)
}

// trackContextAssign records which identifiers an assignment leaves holding a deadline-free
// context, and forgets those it rebinds to anything else.
func (g *goScanner) trackContextAssign(assign *ast.AssignStmt) {
	g.trackContextBinding(assign.Lhs, assign.Rhs, assign.Tok == token.DEFINE)
}

// trackContextSpec does the same for a var declaration.
func (g *goScanner) trackContextSpec(spec *ast.ValueSpec) {
	lhs := make([]ast.Expr, 0, len(spec.Names))
	for i := 0; i < len(spec.Names); i++ {
		lhs = append(lhs, spec.Names[i])
	}
	g.trackContextBinding(lhs, spec.Values, true)
}

// trackContextBinding pairs each bound identifier with the value it receives. A single call
// on the right of several names is a tuple, and a context derivation returns its context
// first, so only the first name can receive it.
func (g *goScanner) trackContextBinding(lhs, rhs []ast.Expr, declares bool) {
	for i := 0; i < len(lhs); i++ {
		ident, ok := lhs[i].(*ast.Ident)
		if !ok || ident.Name == "_" {
			continue
		}
		free := false
		switch {
		case len(rhs) == len(lhs):
			free = g.deadlineFree(rhs[i])
		case len(rhs) == 1 && i == 0:
			free = g.deadlineFree(rhs[0])
		}
		g.bindContext(ident.Name, free, declares)
	}
}

// bindContext records whether name now holds a deadline-free context, and until where. A
// declaration lives to the end of the innermost enclosing scope. A plain assignment writes
// a variable declared earlier: one already tracked keeps its scope, and any other is taken
// to live to the end of the enclosing function, since a same-named variable of a narrower
// scope would have been shadowed by it. At package level nothing is tracked.
func (g *goScanner) bindContext(name string, free, declares bool) {
	if !free {
		delete(g.freeContexts, name)
		return
	}
	end := g.scopeEnd(declares)
	if prior, tracked := g.freeContexts[name]; tracked && !declares {
		end = prior
	}
	if end == token.NoPos {
		return
	}
	g.freeContexts[name] = end
}

// scopeEnd returns where the scope of a binding made at the current node ends: the
// innermost block, case or select clause, or the if, for, range, switch or type switch
// whose header declares it, for a declaration; the innermost function for a plain
// assignment. The walk stack holds only ancestors of the current node, so this is a
// bounded scan (maxNodeStack). It returns NoPos at package level.
func (g *goScanner) scopeEnd(declares bool) token.Pos {
	for i := len(g.stack) - 1; i >= 0; i-- {
		switch g.stack[i].(type) {
		case *ast.FuncDecl, *ast.FuncLit:
			return g.stack[i].End()
		case *ast.BlockStmt, *ast.CaseClause, *ast.CommClause, *ast.IfStmt, *ast.ForStmt,
			*ast.RangeStmt, *ast.SwitchStmt, *ast.TypeSwitchStmt:
			if declares {
				return g.stack[i].End()
			}
		}
	}
	return token.NoPos
}
