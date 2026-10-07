package hiss

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"strings"
	"unicode"
)

// maxNodeStack bounds the AST ancestor stack kept while walking a Go file (HISS-02).
const maxNodeStack = 1024

// scanGoSource audits one Go file through go/parser and go/ast instead of line
// heuristics, so braces inside string literals, wrapped signatures and comments can
// no longer desynchronise the scanner. Syntax errors leave a partial AST that is still
// scanned; a file without any Go structure yields nothing.
func scanGoSource(data []byte, rel string, rep *ScanReport, opts ScanOptions) {
	fset := token.NewFileSet()
	// Identifier resolution is unused by this syntax scanner and can replace a deep
	// valid file with a package-only AST when the deprecated resolver hits its limit.
	file, parseErr := parser.ParseFile(fset, rel, data, parser.ParseComments|parser.SkipObjectResolution)
	// Any parse error means the AST is partial at best, so the rules below never saw the
	// whole file. Recording it keeps the report honest: otherwise deleting a single
	// character from a source file removes its infractions from the baseline with no
	// signal, and the debt ratchet reads the loss as an improvement.
	//
	// The parser recovers rather than giving up, returning a non-nil File whose Name is a
	// non-nil Ident with an empty string, so neither a nil File nor a nil Name identifies
	// a failure; parseErr is the only reliable signal.
	if parseErr != nil {
		rep.Skips.Unparsed++
	}
	if file == nil || file.Name == nil || file.Name.Name == "" {
		return
	}
	g := &goScanner{
		fset:    fset,
		rel:     rel,
		rep:     rep,
		maxLOC:  opts.MaxFuncLOC,
		isTest:  strings.HasSuffix(rel, "_test.go"),
		safety:  safetyCommentLines(fset, file),
		imports: FileImports(file),
		pkg:     file.Name.Name,

		freeContexts: make(map[string]token.Pos),
		lifecycle:    make(map[string]lifecyclePair),
	}
	g.logs = newLoggerProof(g.imports, &g.scope)
	g.walk(file)
	g.noteFunctions(file)
	g.measure(file, opts.Complexity)
}

// noteFunctions records every function the file declares, a method under its receiver type, so
// a finding at or inside one is anchored to it rather than to its line (anchor.go). A function
// literal bound at package level is not a declaration; a finding inside one is anchored by its
// line's text.
func (g *goScanner) noteFunctions(file *ast.File) {
	for i := 0; i < len(file.Decls); i++ {
		fn, ok := file.Decls[i].(*ast.FuncDecl)
		if !ok || fn.Name == nil {
			continue
		}
		owner := ""
		if fn.Recv != nil && len(fn.Recv.List) > 0 {
			owner, _ = ReceiverTypeName(fn.Recv.List[0].Type)
		}
		noteFunction(g.rep, qualifiedName(owner, fn.Name.Name), g.line(fn.Pos()), g.line(fn.End()))
	}
}

// measure reports every production function whose complexity exceeds limits. The values
// are measurements, not violations: they never reach the infraction list, so no scan result,
// baseline or gate changes because of them. Test files are exempt, as they are from the
// length rule and from gocyclo, gocognit and funlen in .golangci.yml.
func (g *goScanner) measure(file *ast.File, limits ComplexityLimits) {
	if g.isTest {
		return
	}
	units := MeasureFile(g.fset, file)
	for i := 0; i < len(units); i++ {
		for _, m := range units[i].Measurements(g.fset, g.rel, limits) {
			recordMeasurement(g.rep, m)
		}
	}
}

type goScanner struct {
	fset    *token.FileSet
	rel     string
	rep     *ScanReport
	maxLOC  int
	isTest  bool
	safety  map[int]struct{}
	imports GoImports
	// pkg is the file's package name; main.main is an entry point only in package main.
	pkg   string
	stack []ast.Node
	// freeContexts maps each identifier of the current function that holds a context
	// without a deadline to the end of the scope that binding lives in (go_io.go). It is
	// cleared at every function declaration.
	freeContexts map[string]token.Pos
	// logs proves which receivers of a log/slog sink method are *slog.Logger values
	// (go_io_logger.go); the callee walk feeds the same proof.
	logs loggerProof
	// lifecycle maps each identifier holding a context.WithCancel context, or one inherited from
	// it, to its cancel function (go_io_lifecycle.go).
	lifecycle map[string]lifecyclePair
	// scope tracks which names are bound at the node the walk has reached (go_scope.go).
	scope goScope
}

// walk visits every node once with an explicit ancestor stack; the traversal itself is
// the standard library's ast.Inspect, this code holds no recursion. Each node is inspected
// in the scope in force where it begins, before it opens a scope of its own.
func (g *goScanner) walk(file *ast.File) {
	ast.Inspect(file, func(n ast.Node) bool {
		if n == nil {
			if last := len(g.stack) - 1; last >= 0 {
				g.scope.leave(g.stack[last])
				g.stack = g.stack[:last]
			}
			return true
		}
		if len(g.stack) >= maxNodeStack {
			g.rep.Truncated = true
			return false
		}
		g.inspect(n)
		g.scope.enter(n)
		g.stack = append(g.stack, n)
		return true
	})
}

// ReceiverName returns the bound receiver identifier of a method, or "" for a plain
// function or an unnamed receiver.
func ReceiverName(fn *ast.FuncDecl) string {
	if fn == nil || fn.Recv == nil || len(fn.Recv.List) == 0 || len(fn.Recv.List[0].Names) == 0 {
		return ""
	}
	return fn.Recv.List[0].Names[0].Name
}

// maxReceiverTypeDepth bounds the pointer, parenthesis and instantiation layers
// ReceiverTypeName unwraps (HISS-02). A valid receiver nests at most a few deep.
const maxReceiverTypeDepth = 8

// ReceiverTypeName returns the declared type name at the root of a method receiver's type
// expression, unwrapping a pointer, parentheses and a generic instantiation: *Set[T],
// (Pair[K, V]) and T all name their base type. It reports false for any other shape.
//
// Matching only *ast.Ident and *ast.StarExpr collapsed every generic receiver to one
// placeholder, so same-named methods on different generic types collided.
func ReceiverTypeName(expr ast.Expr) (string, bool) {
	for i := 0; i < maxReceiverTypeDepth; i++ {
		switch t := expr.(type) {
		case *ast.Ident:
			return t.Name, true
		case *ast.StarExpr:
			expr = t.X
		case *ast.ParenExpr:
			expr = t.X
		case *ast.IndexExpr:
			expr = t.X
		case *ast.IndexListExpr:
			expr = t.X
		default:
			return "", false
		}
	}
	return "", false
}

// maxTypeArgUnwrap bounds unwrapping nested parentheses and index expressions (HISS-02).
const maxTypeArgUnwrap = 16

// stripTypeArgs unwraps enclosing parentheses and any explicit type arguments
// (an IndexExpr or IndexListExpr) to return the underlying callee expression.
func stripTypeArgs(fun ast.Expr) ast.Expr {
	for i := 0; i < maxTypeArgUnwrap && fun != nil; i++ {
		fun = ast.Unparen(fun)
		switch indexed := fun.(type) {
		case *ast.IndexExpr:
			fun = indexed.X
		case *ast.IndexListExpr:
			fun = indexed.X
		default:
			return fun
		}
	}
	return ast.Unparen(fun)
}

// CallTargetsEnclosing reports whether a call targets the enclosing function itself: a bare
// identifier for a plain function, or recv.method for a method. A same-named method on any
// other value -- the delegation idiom `return x.inner.Close()` -- is not recursion.
func CallTargetsEnclosing(fun ast.Expr, fnName, recv string) bool {
	// A generic call carries its type arguments as an index expression around the callee,
	// so Count[T](k) reaches here as IndexExpr{X: Ident("Count")}. Without unwrapping, an
	// explicitly instantiated self-call was invisible while the inferred Count(k) form was
	// reported -- the same recursion, detected or not according to whether the author
	// wrote the type argument.
	switch expr := stripTypeArgs(fun).(type) {
	case *ast.Ident:
		return recv == "" && expr.Name == fnName
	case *ast.SelectorExpr:
		if recv == "" || expr.Sel.Name != fnName {
			return false
		}
		x, isIdent := ast.Unparen(expr.X).(*ast.Ident)
		return isIdent && x.Name == recv
	default:
		return false
	}
}

// enclosingFunc returns the innermost function declaration on the walk stack, or nil when
// the node is not inside one. The stack is already maintained by walk and is bounded by
// maxNodeStack, so this costs a bounded scan rather than a second traversal (HISS-02).
func (g *goScanner) enclosingFunc() *ast.FuncDecl {
	for i := len(g.stack) - 1; i >= 0; i-- {
		if fn, ok := g.stack[i].(*ast.FuncDecl); ok {
			return fn
		}
	}
	return nil
}

// checkSelfRecursion reports a call that targets the function enclosing it.
//
// HISS-01 requires the call graph to form a DAG and declares an immediate build failure,
// but the scanner reported only `goto`, so every form of recursion passed the gate. This
// closes direct recursion, which is the shape a single file can decide. Mutual and
// indirect recursion need the package call graph, which go_callgraph.go builds after the walk.
func (g *goScanner) checkSelfRecursion(call *ast.CallExpr) {
	fn := g.enclosingFunc()
	if fn == nil || fn.Name == nil {
		return
	}
	if !CallTargetsEnclosing(call.Fun, fn.Name.Name, ReceiverName(fn)) {
		return
	}
	// In a plain function a binding of the same name in scope at the call -- a parameter, a
	// named result, or a local declared earlier in an enclosing block -- shadows the function,
	// so the call reaches that binding rather than recursing; one declared in another block or
	// after the call does not. A method is called through its receiver, which no binding of
	// the method's name can shadow.
	if fn.Recv == nil && g.shadowed(fn.Name.Name) {
		return
	}
	g.record("HISS-01", call.Pos(), fn.Name.Name,
		"Direct recursion in "+fn.Name.Name+"; the call graph must form an acyclic DAG")
}

func (g *goScanner) inspect(n ast.Node) {
	g.logs.track(n)
	switch node := n.(type) {
	case *ast.FuncDecl:
		g.checkFuncLOC(node)
		g.enterFunc()
	case *ast.FuncLit:
		g.forgetParams(node)
	case *ast.ForStmt:
		g.checkUnboundedFor(node)
	case *ast.BranchStmt:
		g.checkGoto(node)
	case *ast.CallExpr:
		g.checkCall(node)
	case *ast.AssignStmt:
		g.checkBlankAssign(node)
		g.trackContextAssign(node)
	case *ast.ValueSpec:
		g.trackContextSpec(node)
	case *ast.IfStmt:
		g.checkEmptyErrBranch(node)
	case *ast.SelectorExpr:
		g.checkUnsafe(node)
	}
}

// checkCall runs every rule that inspects a call expression.
func (g *goScanner) checkCall(call *ast.CallExpr) {
	g.checkAbort(call)
	g.checkSelfRecursion(call)
	g.checkDotUnsafeCall(call)
	g.checkContextSink(call)
	g.checkContextlessIO(call)
}

// checkUnboundedFor reports a for statement with no condition, the loop half of HISS-02.
func (g *goScanner) checkUnboundedFor(loop *ast.ForStmt) {
	if loop.Cond == nil {
		g.record("HISS-02", loop.Pos(), "", "Unbounded for loop without an exit condition (for {} / for ;; )")
	}
}

// checkGoto reports a goto, which HISS-01 forbids as non-DAG control flow.
func (g *goScanner) checkGoto(branch *ast.BranchStmt) {
	if branch.Tok == token.GOTO {
		g.record("HISS-01", branch.Pos(), "", "Legacy non-DAG control flow jump (goto)")
	}
}

func (g *goScanner) line(pos token.Pos) int {
	return g.fset.Position(pos).Line
}

func (g *goScanner) record(rule string, pos token.Pos, symbol, msg string) {
	recordViolation(g.rep, rule, g.rel, g.line(pos), symbol, msg)
}

// checkFuncLOC enforces the HISS-04 length bound on production functions. Test files
// are exempt, matching the funlen exclusion in .golangci.yml: table-driven 3D tests are
// long by construction.
func (g *goScanner) checkFuncLOC(fn *ast.FuncDecl) {
	if fn.Body == nil || g.isTest {
		return
	}
	loc := nodeLines(g.fset, fn)
	if loc > g.maxLOC {
		g.record("HISS-04", fn.Pos(), fn.Name.Name,
			fmt.Sprintf("Function '%s' (%d LOC) exceeds HISS-04 / NASA Rule 4 limit of %d LOC", fn.Name.Name, loc, g.maxLOC))
	}
}

// checkAbort enforces the HISS-07 abort policy (owner decision Q-014): panic and os.Exit end
// the process instead of returning an error, which library code must not do. Test files and
// the binary entry point main.main are the places allowed to abort.
//
// os.Exit is resolved through the file's imports, so an aliased or dot import is the same
// call and a receiver, parameter, named result or local that shadows the package name is
// not; os.Exit passed as a value is not a call and stays allowed, which is how an entry
// point hands its exit to a library.
//
// panic(http.ErrAbortHandler) is not reported (AbortsHTTPResponse): net/http documents it as
// the way a handler aborts its response, and the server recovers it.
func (g *goScanner) checkAbort(call *ast.CallExpr) {
	if g.isTest || g.inEntryPoint() {
		return
	}
	fun := ast.Unparen(call.Fun)
	if ident, ok := fun.(*ast.Ident); ok && ident.Name == "panic" {
		if !AbortsHTTPResponse(g.imports, g.enclosingFunc(), call) {
			g.record("HISS-07", call.Pos(), "", "Legacy panic invocation in production code path")
		}
		return
	}
	if local, ok := g.osExitCallee(fun); ok && !g.shadowed(local) {
		g.record("HISS-07", call.Pos(), "", "os.Exit ends the process from library code; return an error to main.main instead")
	}
}

// httpAbortSentinel is net/http.ErrAbortHandler, the value net/http documents a handler
// panics with to abort its response.
var httpAbortSentinel = map[goFunc]struct{}{{"net/http", "ErrAbortHandler"}: {}}

// AbortsHTTPResponse reports whether call, made inside fn (nil outside any function
// declaration), is a panic whose single argument is net/http.ErrAbortHandler.
//
// net/http documents that panic as the way a handler aborts its response: the server
// recovers it, drops the connection or resets the stream, and logs no stack trace. Recovery
// middleware needs it once a response is committed, because completing the truncated
// response instead would hand the client a partial body as a finished one.
//
// The argument is resolved through the file's imports im, so an aliased or dot import of
// net/http is the sentinel. A same-named local or package variable, another package's
// ErrAbortHandler, a binding made in fn that shadows the package name where the panic is, a
// wrapped or recovered value and a second argument are not, and the caller keeps reporting
// those panics. Whether a binding shadows is decided by scope (go_scope.go): one declared in
// another block of fn, or after the panic, leaves the package name in force.
func AbortsHTTPResponse(im GoImports, fn *ast.FuncDecl, call *ast.CallExpr) bool {
	if call == nil || len(call.Args) != 1 || call.Ellipsis.IsValid() {
		return false
	}
	if ident, ok := ast.Unparen(call.Fun).(*ast.Ident); !ok || ident.Name != "panic" {
		return false
	}
	_, _, local, ok := resolvePackageCall(im, call.Args[0], httpAbortSentinel)
	return ok && !bindsAt(fn, local, call.Pos())
}

// osExitCallee reports whether fun names os.Exit, and returns the identifier that reached
// package os: the package name of a selector, or Exit itself under a dot import.
func (g *goScanner) osExitCallee(fun ast.Expr) (string, bool) {
	_, _, local, ok := resolvePackageCall(g.imports, fun, osExitFunc)
	return local, ok
}

// shadowed reports whether a binding of name is in scope at the node the walk has reached: a
// receiver, a parameter or named result of an enclosing function or function literal, or a
// local declared earlier in an enclosing block. Any of these hides the package-level binding
// of the same name; a local of another block, or one declared later, does not (go_scope.go).
func (g *goScanner) shadowed(name string) bool {
	return g.scope.binds(name)
}

// inEntryPoint reports whether the walk is inside main.main, the binary entry point, which
// includes every closure it declares.
func (g *goScanner) inEntryPoint() bool {
	if g.pkg != "main" {
		return false
	}
	fn := g.enclosingFunc()
	return fn != nil && fn.Recv == nil && fn.Name != nil && fn.Name.Name == "main"
}

// checkBlankAssign flags an assignment that discards every result (`_ = f()`), the
// HISS-07 unchecked-error shape; partial discards such as `_, err := f()` keep the
// checked value and are not reported.
func (g *goScanner) checkBlankAssign(assign *ast.AssignStmt) {
	if g.isTest || len(assign.Lhs) == 0 {
		return
	}
	for _, lhs := range assign.Lhs {
		ident, ok := lhs.(*ast.Ident)
		if !ok || ident.Name != "_" {
			return
		}
	}
	// Only a discarded CALL can be discarding an error. The rule previously fired on any
	// all-blank assignment, so `_ = 1`, `_ = v` and `_ = <-ch` were each reported as a
	// "legacy unchecked error assignment" -- three shapes that cannot produce an error at
	// all. A rule that reports obviously-correct code is the kind that gets suppressed
	// wholesale, taking its true findings with it.
	//
	// Deciding which calls actually return an error needs type information, which this
	// syntax scanner does not build; errcheck does that with types and runs in the same
	// gate. This narrows the false positives without claiming the stronger check.
	if !discardsCallResult(assign.Rhs) {
		return
	}
	g.record("HISS-07", assign.Pos(), "", "Legacy unchecked error assignment")
}

// discardsCallResult reports whether any discarded expression is a call, which is the only
// shape on the right of a blank assignment that can carry an error.
func discardsCallResult(rhs []ast.Expr) bool {
	for i := 0; i < len(rhs); i++ {
		if _, ok := rhs[i].(*ast.CallExpr); ok {
			return true
		}
	}
	return false
}

// checkEmptyErrBranch flags `if err != nil { }` bodies that swallow the error.
func (g *goScanner) checkEmptyErrBranch(stmt *ast.IfStmt) {
	if g.isTest || stmt.Body == nil || len(stmt.Body.List) != 0 {
		return
	}
	if isErrNotNil(stmt.Cond) {
		g.record("HISS-07", stmt.Pos(), "", "Empty error branch silently swallows the error")
	}
}

func isErrNotNil(cond ast.Expr) bool {
	bin, ok := cond.(*ast.BinaryExpr)
	if !ok || bin.Op != token.NEQ {
		return false
	}
	lhs, lok := bin.X.(*ast.Ident)
	rhs, rok := bin.Y.(*ast.Ident)
	if !lok || !rok {
		return false
	}
	return strings.HasSuffix(strings.ToLower(lhs.Name), "err") && rhs.Name == "nil"
}

// unsafeExports lists the names package unsafe exports, which a dot import binds as bare
// identifiers in the file block.
var unsafeExports = map[string]struct{}{
	"Add": {}, "Alignof": {}, "Offsetof": {}, "Pointer": {}, "Sizeof": {},
	"Slice": {}, "SliceData": {}, "String": {}, "StringData": {},
}

// checkUnsafe flags a selector on package unsafe that is not covered by a SAFETY proof
// on the enclosing statement (HISS-09). The selector's package identifier is resolved
// through the file's imports, so `import u "unsafe"; u.Pointer(p)` is the same use as
// unsafe.Pointer(p), and a local or a different package spelled unsafe is not.
func (g *goScanner) checkUnsafe(sel *ast.SelectorExpr) {
	pkg, ok := sel.X.(*ast.Ident)
	if !ok || !g.imports.Binds(pkg.Name, "unsafe") {
		return
	}
	g.checkUnsafeUse(pkg.Name, sel.Pos(), "unsafe."+sel.Sel.Name)
}

// checkDotUnsafeCall flags a call to a dot-imported unsafe function or conversion, such
// as Pointer(&b[0]) under `import . "unsafe"`. The call is a bare identifier, so the
// selector check never sees it. A dot-imported name in a type position without a
// conversion, such as a parameter of type Pointer, is not reported.
func (g *goScanner) checkDotUnsafeCall(call *ast.CallExpr) {
	if !g.imports.DotImports("unsafe") {
		return
	}
	ident, ok := ast.Unparen(call.Fun).(*ast.Ident)
	if !ok {
		return
	}
	if _, exported := unsafeExports[ident.Name]; !exported {
		return
	}
	g.checkUnsafeUse(ident.Name, call.Pos(), "unsafe."+ident.Name)
}

// checkUnsafeUse records an unsafe use at pos unless local, the identifier that reached
// package unsafe, is shadowed in the enclosing function or a SAFETY proof covers it.
func (g *goScanner) checkUnsafeUse(local string, pos token.Pos, what string) {
	// A local of the same name shadows the package, so the expression reaches that value
	// and no unsafe operation occurs: `var unsafe shim; return unsafe.Pointer` reads a
	// plain struct field.
	if g.shadowed(local) {
		return
	}
	useLine := g.line(pos)
	stmtLine := g.enclosingStatementLine(useLine)
	if g.hasSafety(stmtLine, useLine) {
		return
	}
	g.record("HISS-09", pos, "", what+" without a preceding // SAFETY: proof comment")
}

// enclosingStatementLine returns the start line of the innermost statement or
// declaration on the ancestor stack, or fallback when there is none.
func (g *goScanner) enclosingStatementLine(fallback int) int {
	for i := len(g.stack) - 1; i >= 0; i-- {
		switch anc := g.stack[i].(type) {
		case *ast.BlockStmt:
			continue
		case ast.Stmt:
			return g.line(anc.Pos())
		case ast.Decl:
			return g.line(anc.Pos())
		}
	}
	return fallback
}

func (g *goScanner) hasSafety(stmtLine, useLine int) bool {
	from := stmtLine - 1
	if from < 1 {
		from = 1
	}
	for l := from; l <= useLine && l-from <= maxSafetyLookback; l++ {
		if _, ok := g.safety[l]; ok {
			return true
		}
	}
	return false
}

// safetyCommentLines maps the last line of every comment group stating a SAFETY proof,
// so a proof immediately above a statement (or inside it) exempts the statement.
func safetyCommentLines(fset *token.FileSet, file *ast.File) map[int]struct{} {
	lines := make(map[int]struct{})
	for _, group := range file.Comments {
		text, valid := validatedCommentGroupText(group)
		if valid && isSafetyProof(text) {
			lines[fset.Position(group.End()).Line] = struct{}{}
		}
	}
	return lines
}

// safetyMarker opens a SAFETY proof comment line.
const safetyMarker = "SAFETY:"

// safetyPlaceholders are first words that defer a proof instead of stating one.
var safetyPlaceholders = map[string]struct{}{"TODO": {}, "FIXME": {}, "XXX": {}, "TBD": {}}

// isSafetyProof reports whether comment text states a SAFETY proof: a line that opens
// with the marker, followed by a justification on that line or the lines after it in the
// same comment group.
//
// Matching the marker as a substring made the proof vacuous: a bare `// SAFETY:`, a
// deferred `// SAFETY: TODO`, and prose that merely names the marker mid-sentence each
// exempted the unsafe use beneath them. Whether the justification is truthful is still
// not decided here.
func isSafetyProof(text string) bool {
	lines := strings.Split(text, "\n")
	for i := 0; i < len(lines); i++ {
		rest, marked := strings.CutPrefix(strings.TrimSpace(lines[i]), safetyMarker)
		if !marked {
			continue
		}
		justification := strings.Fields(rest + " " + strings.Join(lines[i+1:], " "))
		if isJustification(justification) {
			return true
		}
	}
	return false
}

// isJustification reports whether the words after a SAFETY marker carry content: at least
// one letter or digit, and a first word that is not a placeholder.
func isJustification(words []string) bool {
	if len(words) == 0 {
		return false
	}
	first := strings.ToUpper(strings.TrimFunc(words[0], isProofPunct))
	if _, placeholder := safetyPlaceholders[first]; placeholder {
		return false
	}
	return strings.IndexFunc(strings.Join(words, " "), isProofWordRune) >= 0
}

// isProofPunct reports whether r is neither a letter nor a digit.
func isProofPunct(r rune) bool {
	return !isProofWordRune(r)
}

// isProofWordRune reports whether r can carry the content of a justification.
func isProofWordRune(r rune) bool {
	return unicode.IsLetter(r) || unicode.IsDigit(r)
}

// validatedCommentGroupText checks the delimiter invariant required by
// ast.CommentGroup.Text. go/parser can return an unterminated block comment in a
// partial AST with a valid End position but without the closing */ delimiter.
func validatedCommentGroupText(group *ast.CommentGroup) (string, bool) {
	if group == nil || len(group.List) == 0 {
		return "", false
	}
	for _, comment := range group.List {
		if !validCommentText(comment) {
			return "", false
		}
	}
	return group.Text(), true
}

func validCommentText(comment *ast.Comment) bool {
	if comment == nil {
		return false
	}
	text := comment.Text
	if strings.HasPrefix(text, "//") {
		return true
	}
	return len(text) >= 4 && strings.HasPrefix(text, "/*") && strings.HasSuffix(text, "*/")
}
