package hiss

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"strings"
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
		fset:   fset,
		rel:    rel,
		rep:    rep,
		maxLOC: opts.MaxFuncLOC,
		isTest: strings.HasSuffix(rel, "_test.go"),
		safety: safetyCommentLines(fset, file),
	}
	g.walk(file)
}

type goScanner struct {
	fset   *token.FileSet
	rel    string
	rep    *ScanReport
	maxLOC int
	isTest bool
	safety map[int]struct{}
	stack  []ast.Node
}

// walk visits every node once with an explicit ancestor stack; the traversal itself is
// the standard library's ast.Inspect, this code holds no recursion.
func (g *goScanner) walk(file *ast.File) {
	ast.Inspect(file, func(n ast.Node) bool {
		if n == nil {
			if len(g.stack) > 0 {
				g.stack = g.stack[:len(g.stack)-1]
			}
			return true
		}
		if len(g.stack) >= maxNodeStack {
			g.rep.Truncated = true
			return false
		}
		g.inspect(n)
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

// CallTargetsEnclosing reports whether a call targets the enclosing function itself: a bare
// identifier for a plain function, or recv.method for a method. A same-named method on any
// other value -- the delegation idiom `return x.inner.Close()` -- is not recursion.
func CallTargetsEnclosing(fun ast.Expr, fnName, recv string) bool {
	// A generic call carries its type arguments as an index expression around the callee,
	// so Count[T](k) reaches here as IndexExpr{X: Ident("Count")}. Without unwrapping, an
	// explicitly instantiated self-call was invisible while the inferred Count(k) form was
	// reported -- the same recursion, detected or not according to whether the author
	// wrote the type argument.
	switch indexed := fun.(type) {
	case *ast.IndexExpr:
		fun = indexed.X
	case *ast.IndexListExpr:
		fun = indexed.X
	}
	switch expr := fun.(type) {
	case *ast.Ident:
		return recv == "" && expr.Name == fnName
	case *ast.SelectorExpr:
		if recv == "" || expr.Sel.Name != fnName {
			return false
		}
		x, isIdent := expr.X.(*ast.Ident)
		return isIdent && x.Name == recv
	default:
		return false
	}
}

// declaresLocal reports whether body declares name as a local identifier, by short variable
// declaration or by a var statement.
//
// A call to that name reaches the local, not the enclosing function, so it is not recursion.
// Without this, a closure that shadows its enclosing function's name was reported as direct
// recursion: the rule compared identifiers without asking what the identifier resolved to.
func declaresLocal(body *ast.BlockStmt, name string) bool {
	if body == nil || name == "" {
		return false
	}
	found := false
	ast.Inspect(body, func(n ast.Node) bool {
		if found || n == nil {
			return false
		}
		switch decl := n.(type) {
		case *ast.AssignStmt:
			if decl.Tok == token.DEFINE {
				found = identListDeclares(decl.Lhs, name)
			}
		case *ast.ValueSpec:
			found = identsDeclare(decl.Names, name)
		}
		return !found
	})
	return found
}

// identListDeclares reports whether any expression is an identifier with the given name.
func identListDeclares(exprs []ast.Expr, name string) bool {
	for i := 0; i < len(exprs); i++ {
		if ident, ok := exprs[i].(*ast.Ident); ok && ident.Name == name {
			return true
		}
	}
	return false
}

// identsDeclare reports whether any identifier carries the given name.
func identsDeclare(idents []*ast.Ident, name string) bool {
	for i := 0; i < len(idents); i++ {
		if idents[i] != nil && idents[i].Name == name {
			return true
		}
	}
	return false
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
// indirect recursion need a whole-program call graph and remain uncovered: that gap is
// real and is not claimed to be closed here.
func (g *goScanner) checkSelfRecursion(call *ast.CallExpr) {
	fn := g.enclosingFunc()
	if fn == nil || fn.Name == nil {
		return
	}
	if !CallTargetsEnclosing(call.Fun, fn.Name.Name, ReceiverName(fn)) {
		return
	}
	// A local of the same name shadows the function, so the call reaches the local rather
	// than recursing. Checked only once a name match is found, so the cost is paid only by
	// candidate findings.
	if declaresLocal(fn.Body, fn.Name.Name) {
		return
	}
	g.record("HISS-01", call.Pos(), fn.Name.Name,
		"Direct recursion in "+fn.Name.Name+"; the call graph must form an acyclic DAG")
}

func (g *goScanner) inspect(n ast.Node) {
	switch node := n.(type) {
	case *ast.FuncDecl:
		g.checkFuncLOC(node)
	case *ast.ForStmt:
		if node.Cond == nil {
			g.record("HISS-02", node.Pos(), "", "Unbounded for loop without an exit condition (for {} / for ;; )")
		}
	case *ast.BranchStmt:
		if node.Tok == token.GOTO {
			g.record("HISS-01", node.Pos(), "", "Legacy non-DAG control flow jump (goto)")
		}
	case *ast.CallExpr:
		g.checkPanic(node)
		g.checkSelfRecursion(node)
	case *ast.AssignStmt:
		g.checkBlankAssign(node)
	case *ast.IfStmt:
		g.checkEmptyErrBranch(node)
	case *ast.SelectorExpr:
		g.checkUnsafe(node)
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
	loc := g.line(fn.End()) - g.line(fn.Pos()) + 1
	if loc > g.maxLOC {
		g.record("HISS-04", fn.Pos(), fn.Name.Name,
			fmt.Sprintf("Function '%s' (%d LOC) exceeds HISS-04 / NASA Rule 4 limit of %d LOC", fn.Name.Name, loc, g.maxLOC))
	}
}

func (g *goScanner) checkPanic(call *ast.CallExpr) {
	if g.isTest {
		return
	}
	if ident, ok := call.Fun.(*ast.Ident); ok && ident.Name == "panic" {
		g.record("HISS-07", call.Pos(), "", "Legacy panic invocation in production code path")
	}
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

// checkUnsafe flags a use of package unsafe that is not covered by a SAFETY: comment
// on the enclosing statement (HISS-09).
func (g *goScanner) checkUnsafe(sel *ast.SelectorExpr) {
	pkg, ok := sel.X.(*ast.Ident)
	if !ok || pkg.Name != "unsafe" {
		return
	}
	// A local named unsafe shadows the package, so the selector reaches that value and no
	// unsafe operation occurs. The rule matched the identifier spelling rather than what it
	// resolved to, so `var unsafe shim; return unsafe.Pointer` was reported as needing a
	// SAFETY proof for reading a plain struct field.
	if fn := g.enclosingFunc(); fn != nil && declaresLocal(fn.Body, "unsafe") {
		return
	}
	useLine := g.line(sel.Pos())
	stmtLine := g.enclosingStatementLine(useLine)
	if g.hasSafety(stmtLine, useLine) {
		return
	}
	g.record("HISS-09", sel.Pos(), "", "unsafe."+sel.Sel.Name+" without a preceding // SAFETY: proof comment")
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

// safetyCommentLines maps the last line of every comment group containing a SAFETY:
// proof, so a proof immediately above a statement (or inside it) exempts the statement.
func safetyCommentLines(fset *token.FileSet, file *ast.File) map[int]struct{} {
	lines := make(map[int]struct{})
	for _, group := range file.Comments {
		text, valid := validatedCommentGroupText(group)
		if valid && strings.Contains(text, "SAFETY:") {
			lines[fset.Position(group.End()).Line] = struct{}{}
		}
	}
	return lines
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
