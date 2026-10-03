package hiss

import (
	"go/ast"
	"go/token"
)

// HISS-04 complexity limits. They are the one source of the cyclomatic, cognitive and
// statement defaults: the scanner, config.HISSComplexityCeiling and every projection derive
// from them, as every function-length default derives from DefaultMaxFuncLOC.
const (
	DefaultMaxCyclomatic = 10
	DefaultMaxCognitive  = 15
	DefaultMaxStatements = 50
)

// maxLogicalChainSteps bounds the walk over one chain of boolean operators (HISS-02). Each
// operand is visited at most twice, so the bound is far above any written expression.
const maxLogicalChainSteps = 4 * maxNodeStack

// maxStatementLists bounds the statement lists one function's count descends into (HISS-02).
const maxStatementLists = 1 << 16

// maxLiteralUnits bounds the function literals measured in one declaration (HISS-02).
const maxLiteralUnits = 4096

// FuncMetrics holds the HISS-04 measurements of one function.
type FuncMetrics struct {
	LOC        int
	Statements int
	Cyclomatic int
	Cognitive  int
}

// FuncUnit is one measured function: a declaration, or a function literal that no
// declaration or other literal encloses. A literal inside a function is part of that
// function's measurement, as gocyclo and gocognit count it, and is never measured twice.
type FuncUnit struct {
	// Node is the *ast.FuncDecl or *ast.FuncLit that was measured.
	Node ast.Node
	// Name is the declared name, the variable a package-level literal is bound to, or
	// "func literal" for a literal bound to nothing.
	Name    string
	Metrics FuncMetrics
}

// MeasureFile measures every function a parsed Go file defines, in declaration order.
func MeasureFile(fset *token.FileSet, file *ast.File) []FuncUnit {
	if file == nil {
		return nil
	}
	units := make([]FuncUnit, 0, len(file.Decls))
	for i := 0; i < len(file.Decls); i++ {
		units = append(units, MeasureDecl(fset, file.Decls[i])...)
	}
	return units
}

// MeasureDecl measures the functions one top-level declaration defines: the function a
// FuncDecl declares, or each outermost function literal inside a var or const group.
func MeasureDecl(fset *token.FileSet, decl ast.Decl) []FuncUnit {
	switch d := decl.(type) {
	case *ast.FuncDecl:
		if d == nil || d.Name == nil {
			return nil
		}
		return []FuncUnit{{Node: d, Name: d.Name.Name, Metrics: MeasureFunc(fset, d)}}
	case *ast.GenDecl:
		if d == nil {
			return nil
		}
		return measureLiterals(fset, d)
	default:
		return nil
	}
}

// measureLiterals measures each outermost function literal of a declaration group. The walk
// stops at a literal, so a literal nested in another is counted inside its enclosing one.
func measureLiterals(fset *token.FileSet, gen *ast.GenDecl) []FuncUnit {
	names := literalNames(gen)
	var units []FuncUnit
	ast.Inspect(gen, func(n ast.Node) bool {
		lit, isLit := n.(*ast.FuncLit)
		if !isLit {
			return len(units) < maxLiteralUnits
		}
		name := names[lit]
		if name == "" {
			name = "func literal"
		}
		units = append(units, FuncUnit{Node: lit, Name: name, Metrics: MeasureFunc(fset, lit)})
		return false
	})
	return units
}

// literalNames maps each function literal bound directly to a declared name to that name.
func literalNames(gen *ast.GenDecl) map[*ast.FuncLit]string {
	names := make(map[*ast.FuncLit]string)
	for _, spec := range gen.Specs {
		values, ok := spec.(*ast.ValueSpec)
		if !ok {
			continue
		}
		for i := 0; i < len(values.Values) && i < len(values.Names); i++ {
			if lit, isLit := values.Values[i].(*ast.FuncLit); isLit {
				names[lit] = values.Names[i].Name
			}
		}
	}
	return names
}

// MeasureFunc computes the HISS-04 metrics of a *ast.FuncDecl or *ast.FuncLit; any other
// node measures zero.
//
// Cyclomatic complexity follows gocyclo: one, plus one per if, for, range, non-default case
// or communication clause, && and ||. Cognitive complexity follows gocognit: if, for,
// range, switch, type switch and select add one plus their nesting depth, an else-if and a
// plain else add one, a labelled jump adds one, a run of like boolean operators adds one,
// and a plain function calling itself by name adds one. Nesting deepens only inside the body
// of those constructs and of a function literal, never inside a condition or an else block.
// The statement count follows funlen; countStatements states its rules.
//
// The walk holds an explicit ancestor stack bounded by maxNodeStack and stops descending past
// it. A body that deep also truncates the scanner's own walk of the file, so the scan report
// already records the file as a lower bound.
func MeasureFunc(fset *token.FileSet, fn ast.Node) FuncMetrics {
	v := &complexityVisitor{metrics: FuncMetrics{Cyclomatic: 1}}
	var body *ast.BlockStmt
	switch f := fn.(type) {
	case *ast.FuncDecl:
		if f == nil {
			return FuncMetrics{}
		}
		body = f.Body
		if f.Recv == nil {
			v.fn = f
		}
	case *ast.FuncLit:
		if f == nil {
			return FuncMetrics{}
		}
		body = f.Body
	default:
		return FuncMetrics{}
	}
	if body != nil {
		v.scope.enter(fn)
		ast.Inspect(body, v.visit)
		v.metrics.Statements = countStatements(body)
	}
	v.metrics.LOC = nodeLines(fset, fn)
	return v.metrics
}

// nodeLines counts the lines a node spans, both end lines included.
func nodeLines(fset *token.FileSet, n ast.Node) int {
	return fset.Position(n.End()).Line - fset.Position(n.Pos()).Line + 1
}

// complexityVisitor accumulates McCabe and cognitive counts while ast.Inspect walks a
// function body. The library walk is depth-first; the visitor keeps an explicit stack of
// enclosing nodes so nesting is measured without recursion of its own (HISS-01).
type complexityVisitor struct {
	stack []ast.Node
	// fn is the plain function being measured, for gocognit's direct-recursion increment;
	// nil for a method or a literal.
	fn      *ast.FuncDecl
	metrics FuncMetrics
	// scope tracks which names are bound at the node the walk has reached (go_scope.go).
	scope goScope
}

// visit is the ast.Inspect callback: a non-nil node is entered, nil pops the last one. A
// node refused at the depth bound is never pushed, and ast.Inspect sends no nil for it.
func (v *complexityVisitor) visit(n ast.Node) bool {
	if n == nil {
		if last := len(v.stack) - 1; last >= 0 {
			v.scope.leave(v.stack[last])
			v.stack = v.stack[:last]
		}
		return true
	}
	if len(v.stack) >= maxNodeStack {
		return false
	}
	v.countCyclomatic(n)
	v.countCognitive(n)
	v.scope.enter(n)
	v.stack = append(v.stack, n)
	return true
}

// countCyclomatic applies the gocyclo increments.
func (v *complexityVisitor) countCyclomatic(n ast.Node) {
	switch x := n.(type) {
	case *ast.IfStmt, *ast.ForStmt, *ast.RangeStmt:
		v.metrics.Cyclomatic++
	case *ast.CaseClause:
		if x.List != nil {
			v.metrics.Cyclomatic++
		}
	case *ast.CommClause:
		if x.Comm != nil {
			v.metrics.Cyclomatic++
		}
	case *ast.BinaryExpr:
		if isLogicalOp(x.Op) {
			v.metrics.Cyclomatic++
		}
	}
}

// countCognitive applies the gocognit increments.
func (v *complexityVisitor) countCognitive(n ast.Node) {
	switch x := n.(type) {
	case *ast.IfStmt:
		v.countIf(x)
	case *ast.ForStmt, *ast.RangeStmt, *ast.SwitchStmt, *ast.TypeSwitchStmt, *ast.SelectStmt:
		v.metrics.Cognitive += 1 + v.nesting(n)
	case *ast.BranchStmt:
		if x.Label != nil {
			v.metrics.Cognitive++
		}
	case *ast.BinaryExpr:
		// An operand of an enclosing boolean expression was counted with its chain.
		if _, chained := v.parent().(*ast.BinaryExpr); !chained && isLogicalOp(x.Op) {
			v.metrics.Cognitive += logicalOpRuns(x)
		}
	case *ast.CallExpr:
		v.countRecursion(x)
	}
}

// countIf adds an if statement's increment: one for an else-if, one plus the nesting depth
// otherwise, and one more for a plain else block.
func (v *complexityVisitor) countIf(x *ast.IfStmt) {
	if parent, ok := v.parent().(*ast.IfStmt); ok && parent.Else == ast.Stmt(x) {
		v.metrics.Cognitive++
	} else {
		v.metrics.Cognitive += 1 + v.nesting(x)
	}
	if _, plainElse := x.Else.(*ast.BlockStmt); plainElse {
		v.metrics.Cognitive++
	}
}

// countRecursion adds gocognit's increment for a plain function calling itself by name. A
// parameter, result or local of the same name in scope at the call shadows the function, so
// calling it is not recursion (go_scope.go); gocognit decides the same through the parser's
// scoped identifier resolution.
func (v *complexityVisitor) countRecursion(call *ast.CallExpr) {
	ident, ok := call.Fun.(*ast.Ident)
	if !ok || v.fn == nil || v.fn.Name == nil || ident.Name != v.fn.Name.Name {
		return
	}
	if !v.scope.binds(ident.Name) {
		v.metrics.Cognitive++
	}
}

// parent returns the innermost enclosing node, or nil at the top of the body.
func (v *complexityVisitor) parent() ast.Node {
	if len(v.stack) == 0 {
		return nil
	}
	return v.stack[len(v.stack)-1]
}

// nesting counts the enclosing constructs whose body holds n. A condition, an init clause
// and an else block sit at their construct's own level; only the body is one deeper.
func (v *complexityVisitor) nesting(n ast.Node) int {
	level := 0
	for i := 0; i < len(v.stack); i++ {
		child := n
		if i+1 < len(v.stack) {
			child = v.stack[i+1]
		}
		if body := nestingBody(v.stack[i]); body != nil && ast.Node(body) == child {
			level++
		}
	}
	return level
}

// nestingBody returns the body of a construct that deepens nesting, or nil for any other
// node. The statement count descends into the same bodies.
func nestingBody(n ast.Node) *ast.BlockStmt {
	switch x := n.(type) {
	case *ast.IfStmt:
		return x.Body
	case *ast.ForStmt:
		return x.Body
	case *ast.RangeStmt:
		return x.Body
	case *ast.SwitchStmt:
		return x.Body
	case *ast.TypeSwitchStmt:
		return x.Body
	case *ast.SelectStmt:
		return x.Body
	case *ast.FuncLit:
		return x.Body
	default:
		return nil
	}
}

// isLogicalOp reports whether op is && or ||.
func isLogicalOp(op token.Token) bool {
	return op == token.LAND || op == token.LOR
}

// logicalOpRuns counts the runs of like boolean operators in an expression chain read left
// to right, as gocognit does: a && b && c is one run, a && b || c two, a || b && c || d
// three. The chain is read in order with an explicit stack; a parenthesised operand is a
// separate expression and is counted when the walk reaches it.
func logicalOpRuns(root *ast.BinaryExpr) int {
	runs := 0
	var last token.Token
	stack := make([]*ast.BinaryExpr, 0, 8)
	cur := root
	for i := 0; i < maxLogicalChainSteps && (cur != nil || len(stack) > 0); i++ {
		if cur != nil {
			stack = append(stack, cur)
			cur = asBinary(cur.X)
			continue
		}
		node := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if isLogicalOp(node.Op) && node.Op != last {
			runs++
			last = node.Op
		}
		cur = asBinary(node.Y)
	}
	return runs
}

// asBinary returns e as a binary expression, or nil for any other expression.
func asBinary(e ast.Expr) *ast.BinaryExpr {
	if b, ok := e.(*ast.BinaryExpr); ok {
		return b
	}
	return nil
}

// countStatements counts a body's statements as funlen does: every statement in a list
// counts once, a block counts only its contents, and the count descends into the body of an
// if (not its init or else), for, range, switch, type switch and select, into each case
// clause, and into a function literal that is the first right-hand side of an assignment or
// the callee of a go or defer statement. A communication clause and a labelled statement
// count once without their contents. The walk keeps its own list stack (HISS-01).
func countStatements(body *ast.BlockStmt) int {
	if body == nil {
		return 0
	}
	lists := [][]ast.Stmt{body.List}
	total := 0
	for i := 0; i < maxStatementLists && len(lists) > 0; i++ {
		list := lists[len(lists)-1]
		lists = lists[:len(lists)-1]
		for _, stmt := range list {
			if _, block := stmt.(*ast.BlockStmt); !block {
				total++
			}
			if nested := nestedStatements(stmt); len(nested) > 0 {
				lists = append(lists, nested)
			}
		}
	}
	return total
}

// nestedStatements returns the statement list funlen descends into below stmt.
func nestedStatements(stmt ast.Stmt) []ast.Stmt {
	if body := nestingBody(stmt); body != nil {
		return body.List
	}
	switch s := stmt.(type) {
	case *ast.BlockStmt:
		return s.List
	case *ast.CaseClause:
		return s.Body
	case *ast.AssignStmt:
		if len(s.Rhs) > 0 {
			return literalStatements(s.Rhs[0])
		}
	case *ast.GoStmt:
		return calleeStatements(s.Call)
	case *ast.DeferStmt:
		return calleeStatements(s.Call)
	}
	return nil
}

// calleeStatements returns the body of a call whose callee is a function literal, or nil.
func calleeStatements(call *ast.CallExpr) []ast.Stmt {
	if call == nil {
		return nil
	}
	return literalStatements(call.Fun)
}

// literalStatements returns the body of a function literal expression, or nil.
func literalStatements(e ast.Expr) []ast.Stmt {
	if lit, ok := e.(*ast.FuncLit); ok && lit.Body != nil {
		return lit.Body.List
	}
	return nil
}
