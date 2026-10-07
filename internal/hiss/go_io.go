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
//
// Four shapes that hand a deadline-free context to a call are accepted (#841). Each is an
// exact shape with its evidence, never a guess about what a callee does, and a call beside
// one that does not match it is reported as before:
//
//   - lifecycle-owned: a context.WithCancel context whose cancel function a framework's stop
//     hook calls, used inside that hook's start function (go_io_lifecycle.go, lifecycleHooks);
//   - sinks: the log/slog functions and Logger methods that take a context only for its values,
//     and the context's own methods, which a select over channels reads (contextFuncs,
//     contextMethods);
//   - callee-bounded: a function of the same module that derives WithTimeout or WithDeadline
//     from the context before anything else uses it (go_io_callee.go);
//   - ignored by a third-party function whose source, at the versions checked, never uses the
//     context for I/O (go_io_index.go, contextIgnoredBy).
//
// The last two need files other than the caller's, so the walk records the finding together
// with what would discharge it, and the package pass after the walk drops it once that holds
// (resolveIOProofs in go_io_proof.go).

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

// contextFuncs classifies every function that derives, inspects or only reads a context. None
// of them performs I/O on it, so passing a deadline-free context to one is not itself a finding;
// what is derived from it is followed instead.
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
	// log/slog hands the context to its handler for the values it carries: Handler.Handle
	// documents the context as "present solely to provide Handlers access to the context's
	// values", and that canceling it should not affect record processing.
	{"log/slog", "DebugContext"}: ctxNeutral,
	{"log/slog", "InfoContext"}:  ctxNeutral,
	{"log/slog", "WarnContext"}:  ctxNeutral,
	{"log/slog", "ErrorContext"}: ctxNeutral,
	{"log/slog", "Log"}:          ctxNeutral,
	{"log/slog", "LogAttrs"}:     ctxNeutral,
}

// goMethod names a method by the import path and name of its receiver's type.
type goMethod struct {
	Path string
	Type string
	Name string
}

// contextMethods classifies the methods that take a context, or are called on one, and perform
// no I/O on it, as contextFuncs does for functions. The log/slog Logger methods have the
// package functions' evidence. A context.Context method only reads the context, so a select
// whose cases receive from ctx.Done() and otherwise only send to or receive from channels
// performs no I/O on it either; a case whose channel operand is itself a call is a call, and is
// judged as one.
var contextMethods = map[goMethod]contextEffect{
	{"log/slog", "Logger", "DebugContext"}: ctxNeutral,
	{"log/slog", "Logger", "InfoContext"}:  ctxNeutral,
	{"log/slog", "Logger", "WarnContext"}:  ctxNeutral,
	{"log/slog", "Logger", "ErrorContext"}: ctxNeutral,
	{"log/slog", "Logger", "Log"}:          ctxNeutral,
	{"log/slog", "Logger", "LogAttrs"}:     ctxNeutral,
	{"context", "Context", "Deadline"}:     ctxNeutral,
	{"context", "Context", "Done"}:         ctxNeutral,
	{"context", "Context", "Err"}:          ctxNeutral,
	{"context", "Context", "Value"}:        ctxNeutral,
}

// neutralMethod reports whether contextMethods classifies method name of the type typ in the
// package path as one that performs no I/O on a context.
func neutralMethod(path, typ, name string) bool {
	effect, known := contextMethods[goMethod{Path: path, Type: typ, Name: name}]
	return known && effect == ctxNeutral
}

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

// contextType is context.Context, the declared type of a parameter the callee walk follows.
var contextType = map[goFunc]struct{}{{"context", "Context"}: {}}

// cancelPairs are the derivations whose second result cancels the context they return, the
// pair a lifecycle hook may own (go_io_lifecycle.go).
var cancelPairs = map[goFunc]struct{}{
	{"context", "WithCancel"}:      {},
	{"context", "WithCancelCause"}: {},
}

// namesType reports whether expr spells a type of table through the file's imports im. A
// buildable program cannot shadow an imported package name where a type is spelled, so no
// scope is consulted.
func namesType[V any](im GoImports, expr ast.Expr, table map[goFunc]V) bool {
	_, _, _, ok := resolvePackageCall(im, expr, table)
	return ok
}

// isLoggerPointer reports whether expr spells *slog.Logger through the file's imports im.
func isLoggerPointer(im GoImports, expr ast.Expr) bool {
	star, ok := expr.(*ast.StarExpr)
	return ok && namesType(im, star.X, loggerType)
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
	// name, or the function itself under a dot import. A local binding of that name in scope
	// at the call shadows the package, and the call then reaches the local instead.
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

// contextOrigin follows inheriting derivations inward from expr to the identifier the context
// comes from. When a call decides the question first it returns nil and whether that call
// returns a context without a deadline; any other expression is no deadline-free context. The
// loop is bounded by maxNodeStack, which already bounds how deeply a file can nest them.
func (g *goScanner) contextOrigin(expr ast.Expr) (*ast.Ident, bool) {
	for depth := 0; depth < maxNodeStack; depth++ {
		switch e := ast.Unparen(expr).(type) {
		case *ast.Ident:
			return e, false
		case *ast.CallExpr:
			parent, free, decided := g.derivedDeadline(e)
			if decided {
				return nil, free
			}
			expr = parent
		default:
			return nil, false
		}
	}
	return nil, false
}

// deadlineFree reports whether expr evaluates to a context without a deadline: a deciding call,
// or an identifier holding one where it is used.
func (g *goScanner) deadlineFree(expr ast.Expr) bool {
	ident, free := g.contextOrigin(expr)
	if ident == nil {
		return free
	}
	end, tracked := g.freeContexts[ident.Name]
	return tracked && ident.Pos() < end
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

// enterFunc starts a function declaration with nothing tracked but its *slog.Logger parameters.
func (g *goScanner) enterFunc(fn *ast.FuncDecl) {
	clear(g.freeContexts)
	clear(g.loggers)
	clear(g.lifecycle)
	g.bindParams(fn.Type, fn.End())
}

// forgetParams drops a function literal's parameters from what the walk tracks: inside the
// literal each name is the parameter, not the enclosing variable it may shadow. The enclosing
// variable stays forgotten after the literal too, which can miss a later use of it but never
// reports a parameter; a forgotten logger is reported, never exempted.
func (g *goScanner) forgetParams(lit *ast.FuncLit) {
	g.bindParams(lit.Type, lit.End())
}

// bindParams rebinds every parameter of ft as a parameter, which holds no deadline-free context
// the walk knows of and no lifecycle pairing, and holds a logger until end exactly when it is
// declared *slog.Logger.
func (g *goScanner) bindParams(ft *ast.FuncType, end token.Pos) {
	if ft == nil || ft.Params == nil {
		return
	}
	fields := ft.Params.List
	for i := 0; i < len(fields); i++ {
		logger := isLoggerPointer(g.imports, fields[i].Type)
		for j := 0; j < len(fields[i].Names); j++ {
			name := fields[i].Names[j].Name
			delete(g.freeContexts, name)
			g.forgetLifecycle(name)
			delete(g.loggers, name)
			if logger {
				g.loggers[name] = end
			}
		}
	}
}

// checkContextSink reports a deadline-free context passed to a call that is not a context
// derivation or a sink, unless the context is lifecycle-owned there. When the call may reach a
// function the package pass can judge, the finding carries the proof that would discharge it.
func (g *goScanner) checkContextSink(call *ast.CallExpr) {
	if !g.ioRuleApplies() {
		return
	}
	if _, classified := g.contextCall(call); classified || g.loggerSink(call) {
		return
	}
	free := g.freeArgs(call)
	if len(free) == 0 {
		return
	}
	recorded := len(g.rep.Violations)
	g.record("HISS-02", call.Args[free[0]].Pos(), "",
		"Context without a deadline reaches a call; derive it with context.WithTimeout or context.WithDeadline")
	if len(g.rep.Violations) > recorded {
		g.deferProof(call, free)
	}
}

// freeArgs returns the positions of call's arguments that carry a context without a deadline
// and are not owned by a lifecycle where the call is.
func (g *goScanner) freeArgs(call *ast.CallExpr) []int {
	var free []int
	for i := 0; i < len(call.Args); i++ {
		if g.deadlineFree(call.Args[i]) && !g.lifecycleBounded(call.Args[i]) {
			free = append(free, i)
		}
	}
	return free
}

// loggerSink reports whether call is a sink method of a *slog.Logger the walk can prove: its
// receiver is a log/slog constructor's result, a With or WithGroup of a logger, or a parameter
// or local holding one (loggers). A receiver field is proved by the package pass instead.
func (g *goScanner) loggerSink(call *ast.CallExpr) bool {
	sel, ok := ast.Unparen(call.Fun).(*ast.SelectorExpr)
	return ok && neutralMethod("log/slog", "Logger", sel.Sel.Name) && g.loggerExpr(sel.X)
}

// loggerExpr reports whether expr evaluates to a *slog.Logger the walk knows of, following
// With and WithGroup inward to their receiver. The loop is bounded by maxNodeStack.
func (g *goScanner) loggerExpr(expr ast.Expr) bool {
	for depth := 0; depth < maxNodeStack; depth++ {
		switch e := ast.Unparen(expr).(type) {
		case *ast.Ident:
			end, held := g.loggers[e.Name]
			return held && e.Pos() < end
		case *ast.CallExpr:
			if _, _, local, ok := resolvePackageCall(g.imports, e.Fun, loggerSources); ok {
				return !g.shadowed(local)
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

// trackContextSpec does the same for a var declaration. A variable declared *slog.Logger holds
// a logger whatever its value.
func (g *goScanner) trackContextSpec(spec *ast.ValueSpec) {
	lhs := make([]ast.Expr, 0, len(spec.Names))
	for i := 0; i < len(spec.Names); i++ {
		lhs = append(lhs, spec.Names[i])
	}
	g.trackContextBinding(lhs, spec.Values, true)
	if spec.Type == nil || !isLoggerPointer(g.imports, spec.Type) {
		return
	}
	for i := 0; i < len(spec.Names); i++ {
		g.bindTracked(g.loggers, spec.Names[i].Name, true, true)
	}
}

// trackContextBinding pairs each bound identifier with the value it receives (boundValue) and
// records whether it now holds a deadline-free context, a logger, and the lifecycle pairing of
// the context it inherits from. Every value is read before any name is rebound.
func (g *goScanner) trackContextBinding(lhs, rhs []ast.Expr, declares bool) {
	for i := 0; i < len(lhs); i++ {
		ident, ok := lhs[i].(*ast.Ident)
		if !ok || ident.Name == "_" {
			continue
		}
		value := boundValue(lhs, rhs, i)
		free := value != nil && g.deadlineFree(value)
		logger := value != nil && g.loggerExpr(value)
		cancel, owned := g.lifecycleOwner(value)
		g.forgetLifecycle(ident.Name)
		g.bindTracked(g.freeContexts, ident.Name, free, declares)
		g.bindTracked(g.loggers, ident.Name, logger, declares)
		if owned && free {
			g.lifecycle[ident.Name] = cancel
		}
	}
	g.pairCancel(lhs, rhs)
}

// boundValue returns the expression lhs[i] receives, or nil when it receives one result of a
// tuple. A single call on the right of several names is a tuple, and a context derivation
// returns its context first, so only the first name can receive it.
func boundValue(lhs, rhs []ast.Expr, i int) ast.Expr {
	switch {
	case len(rhs) == len(lhs):
		return rhs[i]
	case len(rhs) == 1 && i == 0:
		return rhs[0]
	default:
		return nil
	}
}

// bindTracked records in tracked whether name is now set, and until where. A declaration lives
// to the end of the innermost enclosing scope. A plain assignment writes a variable declared
// earlier: one already tracked keeps its scope, and any other is taken to live to the end of
// the enclosing function, since a same-named variable of a narrower scope would have been
// shadowed by it. At package level nothing is tracked.
func (g *goScanner) bindTracked(tracked map[string]token.Pos, name string, set, declares bool) {
	if !set {
		delete(tracked, name)
		return
	}
	end := g.scopeEnd(declares)
	if prior, ok := tracked[name]; ok && !declares {
		end = prior
	}
	if end == token.NoPos {
		return
	}
	tracked[name] = end
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
