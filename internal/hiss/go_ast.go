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
	g.record("HISS-07", assign.Pos(), "", "Legacy unchecked error assignment")
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
		if strings.Contains(group.Text(), "SAFETY:") {
			lines[fset.Position(group.End()).Line] = struct{}{}
		}
	}
	return lines
}
