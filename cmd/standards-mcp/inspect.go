package main

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
)

// HISS-04 bounds as declared in AGENTS.md and .standards.yaml.
const (
	maxFuncLOC     = 75
	maxStatements  = 50
	maxCyclomatic  = 10
	maxCognitive   = 15
	maxFilesToScan = 500
)

// funcMetrics holds the HISS-04 measurements of one function declaration.
type funcMetrics struct {
	LOC        int
	Statements int
	Cyclomatic int
	Cognitive  int
}

// violations lists the HISS-04 bounds the metrics exceed, in report order.
func (m funcMetrics) violations() []string {
	var out []string
	if m.LOC > maxFuncLOC {
		out = append(out, "LOC")
	}
	if m.Statements > maxStatements {
		out = append(out, "Stmts")
	}
	if m.Cyclomatic > maxCyclomatic {
		out = append(out, "Cyclo")
	}
	if m.Cognitive > maxCognitive {
		out = append(out, "Cognitive")
	}
	return out
}

// complexityVisitor accumulates statement, McCabe and cognitive complexity counts while
// ast.Inspect walks a function body. The library walk is depth-first; the visitor keeps
// an explicit stack of enclosing nodes so nesting can be measured without recursion of
// its own (HISS-01).
type complexityVisitor struct {
	stack   []ast.Node
	metrics funcMetrics
}

// visit is the ast.Inspect callback: a non-nil node is entered, nil pops the last one.
func (v *complexityVisitor) visit(n ast.Node) bool {
	if n == nil {
		if len(v.stack) > 0 {
			v.stack = v.stack[:len(v.stack)-1]
		}
		return true
	}
	v.countStatement(n)
	v.countBranch(n)
	v.stack = append(v.stack, n)
	return true
}

// countStatement counts executable statements: every ast.Stmt except the block braces
// themselves, so nested statements are counted, not only the top-level list.
func (v *complexityVisitor) countStatement(n ast.Node) {
	if _, isStmt := n.(ast.Stmt); !isStmt {
		return
	}
	switch n.(type) {
	case *ast.BlockStmt, *ast.EmptyStmt:
		return
	}
	v.metrics.Statements++
}

// countBranch applies the McCabe (gocyclo) and cognitive (gocognit) increments.
func (v *complexityVisitor) countBranch(n ast.Node) {
	switch x := n.(type) {
	case *ast.IfStmt:
		v.metrics.Cyclomatic++
		if v.isElseIf(x) {
			v.metrics.Cognitive++ // else-if: +1, no nesting penalty
		} else {
			v.metrics.Cognitive += 1 + v.nesting()
		}
	case *ast.ForStmt, *ast.RangeStmt:
		v.metrics.Cyclomatic++
		v.metrics.Cognitive += 1 + v.nesting()
	case *ast.SwitchStmt, *ast.TypeSwitchStmt, *ast.SelectStmt:
		// McCabe counts the case clauses, not the switch itself.
		v.metrics.Cognitive += 1 + v.nesting()
	default:
		v.countClause(n)
	}
	v.countElse(n)
}

// countClause applies the increments of case clauses, boolean operators and labelled
// jumps.
func (v *complexityVisitor) countClause(n ast.Node) {
	switch x := n.(type) {
	case *ast.CaseClause:
		if x.List != nil {
			v.metrics.Cyclomatic++
		}
	case *ast.CommClause:
		if x.Comm != nil {
			v.metrics.Cyclomatic++
		}
	case *ast.BinaryExpr:
		v.countLogicalOperator(x)
	case *ast.BranchStmt:
		if x.Label != nil {
			v.metrics.Cognitive++ // labelled break/continue/goto
		}
	}
}

// countElse adds the cognitive increment for a plain else block.
func (v *complexityVisitor) countElse(n ast.Node) {
	ifStmt, ok := n.(*ast.IfStmt)
	if !ok || ifStmt.Else == nil {
		return
	}
	if _, chained := ifStmt.Else.(*ast.IfStmt); chained {
		return // counted when the chained if is entered
	}
	v.metrics.Cognitive++
}

// countLogicalOperator adds +1 per sequence of the same boolean operator.
func (v *complexityVisitor) countLogicalOperator(x *ast.BinaryExpr) {
	if x.Op != token.LAND && x.Op != token.LOR {
		return
	}
	v.metrics.Cyclomatic++
	if parent, ok := v.parent().(*ast.BinaryExpr); ok && parent.Op == x.Op {
		return // same operator as the enclosing expression: one sequence
	}
	v.metrics.Cognitive++
}

// isElseIf reports whether the if statement is the Else branch of its parent if.
func (v *complexityVisitor) isElseIf(x *ast.IfStmt) bool {
	parent, ok := v.parent().(*ast.IfStmt)
	return ok && parent.Else == ast.Stmt(x)
}

// parent returns the innermost enclosing node, or nil at the top of the body.
func (v *complexityVisitor) parent() ast.Node {
	if len(v.stack) == 0 {
		return nil
	}
	return v.stack[len(v.stack)-1]
}

// nesting counts the enclosing constructs that raise the cognitive nesting level. An
// else-if chain shares its parent's level, so chained ifs are skipped.
func (v *complexityVisitor) nesting() int {
	level := 0
	for i := 0; i < len(v.stack); i++ {
		switch node := v.stack[i].(type) {
		case *ast.IfStmt:
			if i == 0 || !v.isChainedAt(i, node) {
				level++
			}
		case *ast.ForStmt, *ast.RangeStmt, *ast.SwitchStmt, *ast.TypeSwitchStmt, *ast.SelectStmt, *ast.FuncLit:
			level++
		}
	}
	return level
}

// isChainedAt reports whether the if at stack index i is the Else branch of the if
// directly below it on the stack.
func (v *complexityVisitor) isChainedAt(i int, node *ast.IfStmt) bool {
	if i == 0 {
		return false
	}
	parent, ok := v.stack[i-1].(*ast.IfStmt)
	return ok && parent.Else == ast.Stmt(node)
}

// measureFunc computes the HISS-04 metrics of a function declaration.
func measureFunc(fset *token.FileSet, fn *ast.FuncDecl) funcMetrics {
	v := &complexityVisitor{metrics: funcMetrics{Cyclomatic: 1}}
	if fn.Body != nil {
		ast.Inspect(fn.Body, v.visit)
	}
	start := fset.Position(fn.Pos()).Line
	end := fset.Position(fn.End()).Line
	v.metrics.LOC = end - start + 1
	return v.metrics
}

// inspectionFiles lists every in-scope Go file or rejects incomplete inspection.
func (s *Server) inspectionFiles(path string) ([]string, error) {
	stat, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("failed stat on %s: %w", path, err)
	}
	if !stat.IsDir() {
		file, err := s.inspectableFile(path)
		if err != nil {
			return nil, err
		}
		return []string{file}, nil
	}
	entries, err := os.ReadDir(path)
	if err != nil {
		return nil, fmt.Errorf("failed reading directory: %w", err)
	}
	files := make([]string, 0, 10)
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") {
			continue
		}
		if len(files) == maxFilesToScan {
			return nil, fmt.Errorf("inspection exceeds maximum of %d Go files; select a smaller scope", maxFilesToScan)
		}
		file, err := s.inspectableFile(filepath.Join(path, entry.Name()))
		if err != nil {
			return nil, err
		}
		files = append(files, file)
	}
	return files, nil
}

// inspectableFile checks discovered children as well as explicitly requested paths.
func (s *Server) inspectableFile(path string) (string, error) {
	confined, err := s.confinePath(path)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(confined)
	if err != nil {
		return "", fmt.Errorf("inspect file %q: %w", confined, err)
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("inspect file %q: expected a regular file", confined)
	}
	return confined, nil
}

// inspectSymbolsAtPath inspects Go files at path without recursion.
func (s *Server) inspectSymbolsAtPath(path string) (string, error) {
	files, err := s.inspectionFiles(path)
	if err != nil {
		return "", err
	}
	if len(files) == 0 {
		return "No Go source files found for symbol inspection.", nil
	}
	fset := token.NewFileSet()
	var b strings.Builder
	b.WriteString("=== Go AST Symbol & HISS-04 Complexity Inspection ===\n\n")
	for _, file := range files {
		s.inspectSingleFile(fset, file, &b)
	}
	return b.String(), nil
}

// inspectSingleFile parses a single Go file and prints its top-level declarations.
func (s *Server) inspectSingleFile(fset *token.FileSet, filePath string, b *strings.Builder) {
	node, err := parser.ParseFile(fset, filePath, nil, parser.ParseComments)
	if err != nil {
		fmt.Fprintf(b, "File: %s (Parse error: %v)\n\n", filepath.Base(filePath), err)
		return
	}

	rel, err := filepath.Rel(s.rootDir, filePath)
	if err != nil {
		rel = filePath
	}
	fmt.Fprintf(b, "File: %s (Package: %s)\n", rel, node.Name.Name)

	declLimit := len(node.Decls)
	for d := 0; d < declLimit; d++ {
		switch decl := node.Decls[d].(type) {
		case *ast.FuncDecl:
			writeFuncReport(fset, decl, b)
		case *ast.GenDecl:
			writeTypeReport(decl, b)
		}
	}
	b.WriteString("\n")
}

// writeFuncReport prints one function's HISS-04 metrics and verdict.
func writeFuncReport(fset *token.FileSet, fn *ast.FuncDecl, b *strings.Builder) {
	m := measureFunc(fset, fn)
	status := "PASS"
	if bad := m.violations(); len(bad) > 0 {
		status = "HISS-04 WARN: " + strings.Join(bad, ",")
	}
	receiver := ""
	if fn.Recv != nil && len(fn.Recv.List) > 0 {
		receiver = "(recv) "
	}
	fmt.Fprintf(b, "  - %sFunc: %s | LOC: %d (<=%d) | Stmts: %d (<=%d) | Cyclo: %d (<=%d) | Cognitive: %d (<=%d) [%s]\n",
		receiver, fn.Name.Name, m.LOC, maxFuncLOC, m.Statements, maxStatements,
		m.Cyclomatic, maxCyclomatic, m.Cognitive, maxCognitive, status)
}

// writeTypeReport prints the type names declared by a type declaration group.
func writeTypeReport(decl *ast.GenDecl, b *strings.Builder) {
	if decl.Tok != token.TYPE {
		return
	}
	for _, spec := range decl.Specs {
		if ts, ok := spec.(*ast.TypeSpec); ok {
			fmt.Fprintf(b, "  - Type: %s\n", ts.Name.Name)
		}
	}
}
