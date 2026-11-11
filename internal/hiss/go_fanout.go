// SPDX-FileCopyrightText: 2026 Lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package hiss

import (
	"fmt"
	"go/ast"
	"go/token"
	"strconv"
	"strings"
)

// fanoutFacts holds the per-file and per-function declarations used to decide whether a go
// statement inside a loop body carries a visible scalar concurrency bound (HISS-06).
//
// Array, channel, semaphore and errgroup facts record the declaring identifier's token.Pos.
// Because the scanner parses with parser.SkipObjectResolution (ast.Object is deprecated), a
// name declared more than once in one function body (or shadowing a file-level declaration)
// carries no array, channel, semaphore or errgroup fact at all: as a conservative stand-in for
// lexical resolution without ast.Object, a shadowed limiter makes the go statement reported,
// never exempt.
type fanoutFacts struct {
	consts        map[string]bool
	posConsts     map[string]bool
	redeclared    map[string]bool
	arrays        map[string]token.Pos
	nonZeroArrays map[string]token.Pos
	ints          map[string]bool
	errgroups     map[string]token.Pos
	semaphores    map[string]token.Pos
	tokenChans    map[string]token.Pos
	hissBounds    map[int]string
	loops         []ast.Stmt
	fnRoot        ast.Node
	goPos         token.Pos
	lenRedeclared bool
}

// validBoundDecl reports whether declPos is valid, precedes goPos when declared inside fnRoot,
// and lies outside every enclosing loop in loops.
func (f *fanoutFacts) validBoundDecl(declPos token.Pos) bool {
	if !declPos.IsValid() {
		return false
	}
	if f.fnRoot != nil && f.fnRoot.Pos() <= declPos && declPos < f.fnRoot.End() && declPos >= f.goPos {
		return false
	}
	for i := 0; i < len(f.loops); i++ {
		if f.loops[i].Pos() <= declPos && declPos < f.loops[i].End() {
			return false
		}
	}
	return true
}

// isArray reports whether name resolves to an unshadowed fixed array declared outside loops.
func (f *fanoutFacts) isArray(name string) bool {
	return f.validBoundDecl(f.arrays[name])
}

// isNonZeroArray reports whether name resolves to an unshadowed nonzero-length fixed array declared outside loops.
func (f *fanoutFacts) isNonZeroArray(name string) bool {
	return f.validBoundDecl(f.nonZeroArrays[name])
}

// isSemaphore reports whether name resolves to an unshadowed semaphore declared outside loops.
func (f *fanoutFacts) isSemaphore(name string) bool {
	return f.validBoundDecl(f.semaphores[name])
}

// isTokenChan reports whether name resolves to an unshadowed buffered channel declared outside loops.
func (f *fanoutFacts) isTokenChan(name string) bool {
	return f.validBoundDecl(f.tokenChans[name])
}

// clearName removes all tracked facts for name when a local declaration shadows or redeclares it.
func (f *fanoutFacts) clearName(name string) {
	delete(f.consts, name)
	delete(f.posConsts, name)
	delete(f.arrays, name)
	delete(f.nonZeroArrays, name)
	delete(f.ints, name)
	delete(f.errgroups, name)
	delete(f.semaphores, name)
	delete(f.tokenChans, name)
}

// inspectBounded walks root up to maxNodeStack AST nodes and returns false when the bound stops
// the traversal before completion. The bound is local to the inspected subtree and does not mark
// the repository-wide ScanReport truncated.
func (*goScanner) inspectBounded(root ast.Node, visit func(ast.Node) bool) bool {
	if root == nil {
		return true
	}
	steps := 0
	truncated := false
	ast.Inspect(root, func(n ast.Node) bool {
		if n == nil || truncated {
			return false
		}
		if steps >= maxNodeStack {
			truncated = true
			return false
		}
		steps++
		return visit(n)
	})
	return !truncated
}

// checkGoFanoutNode dispatches HISS-06 bounded-concurrency checks when the walk reaches a go
// statement. Both production files, generated files and _test.go files bind.
func (g *goScanner) checkGoFanoutNode(n ast.Node) {
	goStmt, ok := n.(*ast.GoStmt)
	if !ok {
		return
	}
	g.checkGoFanout(goStmt)
}

// checkGoFanout reports a go statement inside a for or range body when the enclosing function's
// fact or declaration walk hits maxNodeStack or any enclosing loop lacks a HISS-06 concurrency bound.
func (g *goScanner) checkGoFanout(goStmt *ast.GoStmt) {
	loops, fnRoot := g.enclosingFanoutLoops()
	if len(loops) == 0 || fnRoot == nil {
		return
	}
	facts, ok := g.collectFanoutFacts(fnRoot, loops, goStmt.Pos())
	if !ok {
		g.recordFanoutBoundExceeded(goStmt)
		return
	}
	for i := 0; i < len(loops); i++ {
		if !g.isLoopBounded(loops[i], &facts) && !g.hasGoStmtLimiter(loops[:i+1], goStmt, &facts) {
			g.recordFanoutViolation(goStmt)
			return
		}
	}
}

// recordFanoutBoundExceeded records a local HISS-06 diagnostic naming maxNodeStack when a
// function's declaration or fact walk exceeds the HISS-06 analysis bound.
func (g *goScanner) recordFanoutBoundExceeded(goStmt *ast.GoStmt) {
	g.record("HISS-06", goStmt.Pos(), g.enclosingFunctionName(),
		fmt.Sprintf("fan-out bound not provable: function exceeds the HISS-06 analysis bound of %d nodes", maxNodeStack))
}

// recordFanoutViolation records a HISS-06 diagnostic on goStmt.
func (g *goScanner) recordFanoutViolation(goStmt *ast.GoStmt) {
	fnName := g.enclosingFunctionName()
	line := g.line(goStmt.Pos())
	g.record("HISS-06", goStmt.Pos(), fnName, fanoutFindingMessage(fnName, line))
}

// fanoutFindingMessage formats the HISS-06 diagnostic naming the function, line and accepted
// bound forms.
func fanoutFindingMessage(fnName string, line int) string {
	if fnName == "" {
		fnName = "func literal"
	}
	return fmt.Sprintf(
		"Function %s (line %d) launches a goroutine per loop iteration without a visible scalar bound; bound concurrency with semaphore.Weighted.Acquire, a buffered token channel, a constant/array loop bound, or //hiss:bound",
		fnName, line,
	)
}

// enclosingFanoutLoops returns the enclosing for/range statements (innermost first) and the
// enclosing function root for the current node on the scanner stack.
func (g *goScanner) enclosingFanoutLoops() ([]ast.Stmt, ast.Node) {
	var loops []ast.Stmt
	var fnRoot ast.Node
	for i := len(g.stack) - 1; i >= 0; i-- {
		node := g.stack[i]
		switch x := node.(type) {
		case *ast.FuncDecl:
			return loops, x
		case *ast.FuncLit:
			fnRoot = x
		case *ast.ForStmt, *ast.RangeStmt:
			if stmt, ok := node.(ast.Stmt); ok && i+1 < len(g.stack) && g.stack[i+1] == loopBody(node) {
				loops = append(loops, stmt)
			}
		}
	}
	return loops, fnRoot
}

// loopBody returns the body block of a for or range statement, or nil for any other node.
func loopBody(n ast.Node) *ast.BlockStmt {
	switch loop := n.(type) {
	case *ast.ForStmt:
		return loop.Body
	case *ast.RangeStmt:
		return loop.Body
	default:
		return nil
	}
}

// collectFanoutFacts gathers file-level and function-level declarations for HISS-06, returning
// false when any bounded AST walk hits maxNodeStack.
func (g *goScanner) collectFanoutFacts(fnRoot ast.Node, loops []ast.Stmt, goPos token.Pos) (fanoutFacts, bool) {
	facts := fanoutFacts{
		consts:        make(map[string]bool),
		posConsts:     make(map[string]bool),
		redeclared:    make(map[string]bool),
		arrays:        make(map[string]token.Pos),
		nonZeroArrays: make(map[string]token.Pos),
		ints:          make(map[string]bool),
		errgroups:     make(map[string]token.Pos),
		semaphores:    make(map[string]token.Pos),
		tokenChans:    make(map[string]token.Pos),
		hissBounds:    hissBoundComments(g.fset, g.file),
		loops:         loops,
		fnRoot:        fnRoot,
		goPos:         goPos,
	}
	fileDecls := g.collectFileFanoutFacts(&facts)
	if !g.collectFuncFanoutFacts(fnRoot, fileDecls, &facts) {
		return fanoutFacts{}, false
	}
	return facts, true
}

// collectFileFanoutFacts records package-level constants and variables declared in the file and
// returns the declaration counts of package-level value and type names.
func (g *goScanner) collectFileFanoutFacts(facts *fanoutFacts) map[string]int {
	fileDecls := make(map[string]int)
	if g.file == nil {
		return fileDecls
	}
	for i := 0; i < len(g.file.Decls); i++ {
		if gen, ok := g.file.Decls[i].(*ast.GenDecl); ok {
			countGenDeclSpecs(gen, fileDecls)
			if gen.Tok == token.CONST {
				g.recordGenDeclFacts(gen, facts)
			}
		}
	}
	for i := 0; i < len(g.file.Decls); i++ {
		if gen, ok := g.file.Decls[i].(*ast.GenDecl); ok && gen.Tok == token.VAR {
			g.recordGenDeclFacts(gen, facts)
		}
	}
	return fileDecls
}

// recordGenDeclFacts records const and var specifications from one declaration group.
func (g *goScanner) recordGenDeclFacts(gen *ast.GenDecl, facts *fanoutFacts) {
	iotaActive := false
	for i := 0; i < len(gen.Specs); i++ {
		spec, ok := gen.Specs[i].(*ast.ValueSpec)
		if !ok {
			continue
		}
		if gen.Tok == token.CONST {
			iotaActive = recordConstSpecFacts(spec, i, iotaActive, facts)
			continue
		}
		if gen.Tok == token.VAR {
			g.recordValueSpecFacts(spec, facts)
		}
	}
}

// recordConstSpecFacts marks declared constants and tracks which ones hold a positive literal
// value, returning whether an implicit subsequent spec inherits iota.
func recordConstSpecFacts(spec *ast.ValueSpec, specIndex int, iotaActive bool, facts *fanoutFacts) bool {
	if len(spec.Values) > 0 {
		iotaActive = len(spec.Values) == 1 && isNamedIdent(spec.Values[0], "iota")
	}
	for i := 0; i < len(spec.Names); i++ {
		ident := spec.Names[i]
		if ident == nil || ident.Name == "_" || facts.redeclared[ident.Name] {
			continue
		}
		facts.consts[ident.Name] = true
		if isPositiveConstSpecValue(spec, i, specIndex, iotaActive, facts) {
			facts.posConsts[ident.Name] = true
		}
	}
	return iotaActive
}

// isPositiveConstSpecValue reports whether the idx-th name of a const ValueSpec is initialized
// to a positive integer literal (or a nonzero iota / earlier positive literal constant).
func isPositiveConstSpecValue(spec *ast.ValueSpec, idx, specIndex int, iotaActive bool, facts *fanoutFacts) bool {
	if len(spec.Values) == 0 {
		return iotaActive && specIndex > 0
	}
	if idx >= len(spec.Values) {
		return false
	}
	val := ast.Unparen(spec.Values[idx])
	if isNamedIdent(val, "iota") {
		return specIndex > 0
	}
	return isPositiveConstStep(val, facts)
}

// recordValueSpecFacts classifies each declared name in a var ValueSpec from its paired
// initializer, or from the shared spec type when Values is empty.
func (g *goScanner) recordValueSpecFacts(spec *ast.ValueSpec, facts *fanoutFacts) {
	for i := 0; i < len(spec.Names); i++ {
		ident := spec.Names[i]
		if ident == nil || ident.Name == "_" {
			continue
		}
		var val ast.Expr
		if i < len(spec.Values) {
			val = spec.Values[i]
		}
		g.classifySpecBinding(ident, spec.Type, val, facts)
	}
}

// classifySpecBinding assigns at most one bound category to ident from its spec type or own initializer.
func (g *goScanner) classifySpecBinding(ident *ast.Ident, typ, val ast.Expr, facts *fanoutFacts) {
	name, pos := ident.Name, ident.Pos()
	if recordSpecArrayFact(name, pos, typ, val, facts) || g.recordSpecLimiterFact(name, pos, typ, val, facts) {
		return
	}
	if isSpecIntBinding(typ, val) {
		facts.ints[name] = true
	}
}

// recordSpecArrayFact records fixed-array and nonzero-array facts for one spec binding when matched.
func recordSpecArrayFact(name string, pos token.Pos, typ, val ast.Expr, facts *fanoutFacts) bool {
	if !isFixedArrayOrPtrType(typ) && !isFixedArrayValue(val) {
		return false
	}
	recordBoundPos(facts.arrays, name, pos, facts)
	if isNonZeroArrayOrPtrType(typ, facts) || isNonZeroArrayValue(val, facts) {
		recordBoundPos(facts.nonZeroArrays, name, pos, facts)
	}
	return true
}

// recordSpecLimiterFact records errgroup, semaphore or buffered token-channel facts for one spec binding.
func (g *goScanner) recordSpecLimiterFact(name string, pos token.Pos, typ, val ast.Expr, facts *fanoutFacts) bool {
	switch {
	case g.isErrgroupType(typ) || g.isErrgroupValue(val):
		recordBoundPos(facts.errgroups, name, pos, facts)
		return true
	case (val == nil && g.isSemaphoreType(typ)) || g.isSemaphoreValue(val, g.line(pos), facts):
		recordBoundPos(facts.semaphores, name, pos, facts)
		return true
	case g.isBufferedChanMake(val, g.line(pos), facts):
		recordBoundPos(facts.tokenChans, name, pos, facts)
		return true
	default:
		return false
	}
}

// isSpecIntBinding reports whether a spec binding has an integer type or integer literal initializer.
func isSpecIntBinding(typ, val ast.Expr) bool {
	return isIntTypeExpr(typ) || (typ == nil && isIntCandidateValue(val))
}

// collectFuncFanoutFacts walks fnRoot to record parameters and local bindings, drops shadowed or
// redeclared names, and returns false when either AST walk reaches maxNodeStack.
func (g *goScanner) collectFuncFanoutFacts(fnRoot ast.Node, fileDecls map[string]int, facts *fanoutFacts) bool {
	localDecls, ok := g.countFuncDecls(fnRoot)
	if !ok {
		return false
	}
	for name, count := range localDecls {
		facts.clearName(name)
		if count+fileDecls[name] > 1 {
			facts.redeclared[name] = true
		}
	}
	facts.lenRedeclared = localDecls["len"]+fileDecls["len"] > 0
	if !g.inspectBounded(fnRoot, func(n ast.Node) bool {
		g.recordFuncNodeFacts(n, facts)
		return true
	}) {
		return false
	}
	for name := range facts.redeclared {
		facts.clearName(name)
	}
	return true
}

// recordFuncNodeFacts records fan-out facts contributed by one AST node inside fnRoot.
func (g *goScanner) recordFuncNodeFacts(n ast.Node, facts *fanoutFacts) {
	switch node := n.(type) {
	case *ast.FuncDecl:
		g.recordFieldListFacts(node.Recv, facts)
		g.recordSignatureFacts(node.Type, facts)
	case *ast.FuncLit:
		g.recordSignatureFacts(node.Type, facts)
	case *ast.GenDecl:
		g.recordGenDeclFacts(node, facts)
	case *ast.AssignStmt:
		g.recordAssignFacts(node, facts)
	}
}

// recordSignatureFacts records parameter and named-result types of a function signature.
func (g *goScanner) recordSignatureFacts(ft *ast.FuncType, facts *fanoutFacts) {
	if ft == nil {
		return
	}
	g.recordFieldListFacts(ft.Params, facts)
	g.recordFieldListFacts(ft.Results, facts)
}

// countFuncDecls counts how many times each non-blank identifier is declared inside fnRoot and
// reports whether the traversal completed within maxNodeStack.
func (g *goScanner) countFuncDecls(fnRoot ast.Node) (map[string]int, bool) {
	counts := make(map[string]int)
	complete := g.inspectBounded(fnRoot, func(n ast.Node) bool {
		countNodeDecls(n, counts)
		return true
	})
	return counts, complete
}

// countNodeDecls increments counts for each identifier declared directly by n.
func countNodeDecls(n ast.Node, counts map[string]int) {
	switch node := n.(type) {
	case *ast.FuncDecl:
		countFieldListDecls(node.Recv, counts)
		countFuncTypeDecls(node.Type, counts)
	case *ast.FuncLit:
		countFuncTypeDecls(node.Type, counts)
	case *ast.GenDecl:
		countGenDeclSpecs(node, counts)
	case *ast.AssignStmt:
		if node.Tok == token.DEFINE {
			countExprIdentDecls(node.Lhs, counts)
		}
	case *ast.RangeStmt:
		if node.Tok == token.DEFINE {
			countExprIdentDecls([]ast.Expr{node.Key, node.Value}, counts)
		}
	}
}

// countFuncTypeDecls counts type parameter, parameter and named-result declarations.
func countFuncTypeDecls(ft *ast.FuncType, counts map[string]int) {
	if ft == nil {
		return
	}
	countFieldListDecls(ft.TypeParams, counts)
	countFieldListDecls(ft.Params, counts)
	countFieldListDecls(ft.Results, counts)
}

// countFieldListDecls counts every named field identifier in list.
func countFieldListDecls(list *ast.FieldList, counts map[string]int) {
	if list == nil {
		return
	}
	for i := 0; i < len(list.List); i++ {
		countIdentListDecls(list.List[i].Names, counts)
	}
}

// countGenDeclSpecs counts value and type declarations in gen.
func countGenDeclSpecs(gen *ast.GenDecl, counts map[string]int) {
	for i := 0; i < len(gen.Specs); i++ {
		switch spec := gen.Specs[i].(type) {
		case *ast.ValueSpec:
			countIdentListDecls(spec.Names, counts)
		case *ast.TypeSpec:
			countIdentDecl(spec.Name, counts)
		}
	}
}

// countExprIdentDecls counts non-blank *ast.Ident nodes in exprs.
func countExprIdentDecls(exprs []ast.Expr, counts map[string]int) {
	for i := 0; i < len(exprs); i++ {
		if ident, ok := exprs[i].(*ast.Ident); ok {
			countIdentDecl(ident, counts)
		}
	}
}

// countIdentListDecls counts non-blank identifiers in idents.
func countIdentListDecls(idents []*ast.Ident, counts map[string]int) {
	for i := 0; i < len(idents); i++ {
		countIdentDecl(idents[i], counts)
	}
}

// countIdentDecl increments counts for one non-blank identifier.
func countIdentDecl(ident *ast.Ident, counts map[string]int) {
	if ident != nil && ident.Name != "_" {
		counts[ident.Name]++
	}
}

// recordFieldListFacts classifies each field in a receiver, parameter or result list.
func (g *goScanner) recordFieldListFacts(list *ast.FieldList, facts *fanoutFacts) {
	if list == nil {
		return
	}
	for i := 0; i < len(list.List); i++ {
		field := list.List[i]
		g.classifyFieldType(field.Type, field.Names, facts)
	}
}

// classifyFieldType records the declared type category of a parameter, result or receiver field.
func (g *goScanner) classifyFieldType(expr ast.Expr, names []*ast.Ident, facts *fanoutFacts) {
	for i := 0; i < len(names); i++ {
		if names[i] != nil && names[i].Name != "_" {
			g.classifySpecBinding(names[i], expr, nil, facts)
		}
	}
}

// recordAssignFacts classifies short variable declarations and assignments inside a function.
func (g *goScanner) recordAssignFacts(assign *ast.AssignStmt, facts *fanoutFacts) {
	if len(assign.Lhs) == 0 || len(assign.Rhs) == 0 {
		return
	}
	isDefine := assign.Tok == token.DEFINE
	if assign.Tok != token.DEFINE && assign.Tok != token.ASSIGN {
		return
	}
	limit := min(len(assign.Lhs), len(assign.Rhs))
	for i := 0; i < limit; i++ {
		ident, ok := assign.Lhs[i].(*ast.Ident)
		if !ok || ident.Name == "_" {
			continue
		}
		g.classifyAssignedValue(ident, assign.Rhs[i], isDefine, facts)
	}
	if !isDefine && len(assign.Lhs) > len(assign.Rhs) {
		clearTrailingAssignedIdents(assign.Lhs[limit:], facts)
	}
}

// clearTrailingAssignedIdents clears limiter facts on extra LHS names of a multi-value plain assignment.
func clearTrailingAssignedIdents(lhs []ast.Expr, facts *fanoutFacts) {
	for i := 0; i < len(lhs); i++ {
		if ident, ok := lhs[i].(*ast.Ident); ok && ident.Name != "_" {
			delete(facts.errgroups, ident.Name)
			delete(facts.semaphores, ident.Name)
			delete(facts.tokenChans, ident.Name)
		}
	}
}

// classifyAssignedValue records or invalidates the RHS category assigned to ident.
func (g *goScanner) classifyAssignedValue(ident *ast.Ident, rhs ast.Expr, defined bool, facts *fanoutFacts) {
	name, pos := ident.Name, ident.Pos()
	switch {
	case isFixedArrayValue(rhs):
		recordBoundPos(facts.arrays, name, pos, facts)
		if isNonZeroArrayValue(rhs, facts) {
			recordBoundPos(facts.nonZeroArrays, name, pos, facts)
		}
	case g.isErrgroupValue(rhs):
		recordBoundPos(facts.errgroups, name, pos, facts)
	case g.isSemaphoreValue(rhs, g.line(pos), facts):
		recordBoundPos(facts.semaphores, name, pos, facts)
	case g.isBufferedChanMake(rhs, g.line(pos), facts):
		recordBoundPos(facts.tokenChans, name, pos, facts)
	default:
		if defined && isIntCandidateValue(rhs) {
			facts.ints[name] = true
		}
		if !defined {
			delete(facts.errgroups, name)
			delete(facts.semaphores, name)
			delete(facts.tokenChans, name)
		}
	}
}

// recordBoundPos stores pos for name unless an earlier write already placed name inside an
// enclosing loop, so a post-loop reassignment cannot mask an in-loop limiter allocation.
func recordBoundPos(target map[string]token.Pos, name string, pos token.Pos, facts *fanoutFacts) {
	if prev, exists := target[name]; exists && !facts.validBoundDecl(prev) {
		return
	}
	target[name] = pos
}

// unwrapArrayType unwraps parens and an optional leading * to extract a fixed *ast.ArrayType.
func unwrapArrayType(expr ast.Expr) (*ast.ArrayType, bool) {
	if expr == nil {
		return nil, false
	}
	expr = ast.Unparen(expr)
	if star, ok := expr.(*ast.StarExpr); ok {
		expr = ast.Unparen(star.X)
	}
	arr, ok := expr.(*ast.ArrayType)
	if !ok || arr.Len == nil {
		return nil, false
	}
	return arr, true
}

// isFixedArrayOrPtrType reports whether expr is [N]T, [...]T, or a pointer to one.
func isFixedArrayOrPtrType(expr ast.Expr) bool {
	_, ok := unwrapArrayType(expr)
	return ok
}

// isNonZeroArrayOrPtrType reports whether expr spells [N]T or *[N]T with a proven positive N.
func isNonZeroArrayOrPtrType(expr ast.Expr, facts *fanoutFacts) bool {
	arr, ok := unwrapArrayType(expr)
	return ok && isPositiveConstStep(arr.Len, facts)
}

// unwrapArrayCompositeLit unwraps parens and & to return a fixed-array *ast.CompositeLit.
func unwrapArrayCompositeLit(expr ast.Expr) (*ast.CompositeLit, bool) {
	if expr == nil {
		return nil, false
	}
	expr = ast.Unparen(expr)
	if u, ok := expr.(*ast.UnaryExpr); ok && u.Op == token.AND {
		expr = ast.Unparen(u.X)
	}
	comp, ok := expr.(*ast.CompositeLit)
	if !ok || !isFixedArrayOrPtrType(comp.Type) {
		return nil, false
	}
	return comp, true
}

// isFixedArrayValue reports whether expr is a composite literal of a fixed array or pointer to one.
func isFixedArrayValue(expr ast.Expr) bool {
	_, ok := unwrapArrayCompositeLit(expr)
	return ok
}

// isNonZeroArrayValue reports whether expr is a composite literal of a nonzero-length fixed array.
func isNonZeroArrayValue(expr ast.Expr, facts *fanoutFacts) bool {
	comp, ok := unwrapArrayCompositeLit(expr)
	if !ok {
		return false
	}
	arr, ok := unwrapArrayType(comp.Type)
	if !ok {
		return false
	}
	if _, isEllipsis := arr.Len.(*ast.Ellipsis); isEllipsis {
		return len(comp.Elts) > 0
	}
	return isPositiveConstStep(arr.Len, facts)
}

// isIntTypeExpr reports whether expr names a signed or unsigned integer type.
func isIntTypeExpr(expr ast.Expr) bool {
	if expr == nil {
		return false
	}
	ident, ok := ast.Unparen(expr).(*ast.Ident)
	if !ok {
		return false
	}
	switch ident.Name {
	case "int", "int8", "int16", "int32", "int64",
		"uint", "uint8", "uint16", "uint32", "uint64", "uintptr":
		return true
	default:
		return false
	}
}

// isIntCandidateValue reports whether expr can initialize an integer bound variable.
func isIntCandidateValue(expr ast.Expr) bool {
	if expr == nil {
		return false
	}
	switch x := ast.Unparen(expr).(type) {
	case *ast.BasicLit:
		return x.Kind == token.INT
	case *ast.Ident, *ast.BinaryExpr, *ast.UnaryExpr, *ast.CallExpr, *ast.SelectorExpr:
		return true
	default:
		return false
	}
}

// isErrgroupType reports whether expr spells errgroup.Group or *errgroup.Group.
func (g *goScanner) isErrgroupType(expr ast.Expr) bool {
	return g.matchesPkgType(expr, "golang.org/x/sync/errgroup", "errgroup", "Group")
}

// isErrgroupValue reports whether expr constructs an errgroup.Group or *errgroup.Group.
func (g *goScanner) isErrgroupValue(expr ast.Expr) bool {
	if expr == nil {
		return false
	}
	expr = ast.Unparen(expr)
	if u, ok := expr.(*ast.UnaryExpr); ok && u.Op == token.AND {
		expr = ast.Unparen(u.X)
	}
	switch x := expr.(type) {
	case *ast.CompositeLit:
		return g.isErrgroupType(x.Type)
	case *ast.CallExpr:
		if g.isErrgroupWithContextCall(x) {
			return true
		}
		ident, ok := ast.Unparen(x.Fun).(*ast.Ident)
		return ok && ident.Name == "new" && len(x.Args) == 1 && g.isErrgroupType(x.Args[0])
	default:
		return false
	}
}

// isErrgroupWithContextCall reports whether expr is a call to errgroup.WithContext.
func (g *goScanner) isErrgroupWithContextCall(expr ast.Expr) bool {
	call, ok := ast.Unparen(expr).(*ast.CallExpr)
	if !ok {
		return false
	}
	return g.matchesPkgSelector(call.Fun, "golang.org/x/sync/errgroup", "errgroup", "WithContext")
}

// isSemaphoreType reports whether expr spells semaphore.Weighted or *semaphore.Weighted.
func (g *goScanner) isSemaphoreType(expr ast.Expr) bool {
	return g.matchesPkgType(expr, "golang.org/x/sync/semaphore", "semaphore", "Weighted")
}

// isSemaphoreValue reports whether expr constructs a *semaphore.Weighted via NewWeighted with a
// bounded weight (isConstBoundExpr or //hiss:bound on declLine) or via composite literal.
func (g *goScanner) isSemaphoreValue(expr ast.Expr, declLine int, facts *fanoutFacts) bool {
	if expr == nil {
		return false
	}
	expr = ast.Unparen(expr)
	if u, ok := expr.(*ast.UnaryExpr); ok && u.Op == token.AND {
		expr = ast.Unparen(u.X)
	}
	switch x := expr.(type) {
	case *ast.CompositeLit:
		return g.isSemaphoreType(x.Type)
	case *ast.CallExpr:
		return len(x.Args) == 1 && !x.Ellipsis.IsValid() &&
			g.matchesPkgSelector(x.Fun, "golang.org/x/sync/semaphore", "semaphore", "NewWeighted") &&
			g.isNonZeroChanCap(x.Args[0], declLine, facts)
	default:
		return false
	}
}

// matchesPkgType reports whether expr is pkg.Type or *pkg.Type for importPath/defaultPkg.
func (g *goScanner) matchesPkgType(expr ast.Expr, importPath, defaultPkg, typeName string) bool {
	if expr == nil {
		return false
	}
	expr = ast.Unparen(expr)
	if star, ok := expr.(*ast.StarExpr); ok {
		expr = ast.Unparen(star.X)
	}
	return g.matchesPkgSelector(expr, importPath, defaultPkg, typeName)
}

// matchesPkgSelector reports whether expr selects symbol from importPath (including aliases and
// dot imports, or an unimported defaultPkg in standalone snippets).
func (g *goScanner) matchesPkgSelector(expr ast.Expr, importPath, defaultPkg, symbol string) bool {
	if expr == nil {
		return false
	}
	switch x := ast.Unparen(expr).(type) {
	case *ast.SelectorExpr:
		pkg, ok := ast.Unparen(x.X).(*ast.Ident)
		if !ok || x.Sel.Name != symbol {
			return false
		}
		if g.imports.Binds(pkg.Name, importPath) {
			return true
		}
		_, bound := g.imports.Path(pkg.Name)
		return !bound && pkg.Name == defaultPkg
	case *ast.Ident:
		return x.Name == symbol && g.imports.DotImports(importPath)
	default:
		return false
	}
}

// isBufferedChanMake reports whether expr is make(chan T, cap) with a bounded non-zero capacity.
func (g *goScanner) isBufferedChanMake(expr ast.Expr, declLine int, facts *fanoutFacts) bool {
	call, ok := ast.Unparen(expr).(*ast.CallExpr)
	if !ok || len(call.Args) != 2 || call.Ellipsis.IsValid() {
		return false
	}
	ident, ok := ast.Unparen(call.Fun).(*ast.Ident)
	if !ok || ident.Name != "make" {
		return false
	}
	_, isChan := ast.Unparen(call.Args[0]).(*ast.ChanType)
	return isChan && g.isNonZeroChanCap(call.Args[1], declLine, facts)
}

// isNonZeroChanCap reports whether a channel capacity or semaphore weight argument is non-zero,
// non-negative, and proven bounded by isConstBoundExpr or a //hiss:bound comment on declLine.
func (g *goScanner) isNonZeroChanCap(expr ast.Expr, declLine int, facts *fanoutFacts) bool {
	if expr == nil || isNonPositiveCapLiteral(expr) {
		return false
	}
	if facts != nil && facts.hissBounds[declLine] != "" {
		return true
	}
	return g.isConstBoundExpr(expr, facts)
}

// isNonPositiveCapLiteral reports whether expr is a zero or negative integer literal or unary negation.
func isNonPositiveCapLiteral(expr ast.Expr) bool {
	if val, ok := signedIntLiteral(expr); ok {
		return val <= 0
	}
	u, ok := ast.Unparen(expr).(*ast.UnaryExpr)
	return ok && u.Op == token.SUB
}

// isPositiveWeight reports whether expr is a proven positive semaphore weight: a positive integer
// literal, a declared constant initialized to a positive literal, or len(x) of a fixed array with
// nonzero length.
func isPositiveWeight(expr ast.Expr, facts *fanoutFacts) bool {
	if isPositiveConstStep(expr, facts) {
		return true
	}
	call, ok := ast.Unparen(expr).(*ast.CallExpr)
	if !ok || len(call.Args) != 1 || call.Ellipsis.IsValid() || !isNamedIdent(call.Fun, "len") {
		return false
	}
	return !facts.lenRedeclared && isNonZeroArrayExpr(call.Args[0], facts)
}

// isNonZeroArrayExpr reports whether expr evaluates to a fixed array with proven nonzero length.
func isNonZeroArrayExpr(expr ast.Expr, facts *fanoutFacts) bool {
	if isNonZeroArrayValue(expr, facts) {
		return true
	}
	if call, ok := ast.Unparen(expr).(*ast.CallExpr); ok && isNonZeroArrayOrPtrType(call.Fun, facts) {
		return true
	}
	name, ok := receiverBaseIdent(expr)
	return ok && facts.isNonZeroArray(name)
}

// hasGoStmtLimiter checks semaphore and token-channel bounds for a raw go statement across loops.
func (g *goScanner) hasGoStmtLimiter(loops []ast.Stmt, goStmt *ast.GoStmt, facts *fanoutFacts) bool {
	if g.hasSemaphoreAcquireInLoops(loops, goStmt, facts) {
		return true
	}
	return g.hasTokenChannelBound(loops, goStmt, facts)
}

// receiverBaseIdent unwraps parens and unary &/* to return the receiver identifier name.
func receiverBaseIdent(expr ast.Expr) (string, bool) {
	for i := 0; i < maxReceiverTypeDepth && expr != nil; i++ {
		expr = ast.Unparen(expr)
		switch x := expr.(type) {
		case *ast.Ident:
			return x.Name, true
		case *ast.StarExpr:
			expr = x.X
		case *ast.UnaryExpr:
			if x.Op != token.AND {
				return "", false
			}
			expr = x.X
		default:
			return "", false
		}
	}
	return "", false
}

// dominatingStep represents one statement or branch header on the ancestor path from an enclosing
// loop body to a go statement that completes on every path reaching that go statement.
type dominatingStep struct {
	loopBody *ast.BlockStmt
	stmt     ast.Stmt
	nextStmt ast.Stmt
	ifBody   *ast.IfStmt
	endPos   token.Pos
}

// walkDominatingSteps walks the ancestor chain on g.stack from each loop body in loops to goStmt
// and reports whether accept matches any dominating statement or branch condition.
func (g *goScanner) walkDominatingSteps(loops []ast.Stmt, goStmt *ast.GoStmt, accept func(dominatingStep) bool) bool {
	for i := 0; i < len(loops); i++ {
		body := loopBody(loops[i])
		if g.loopPathDominates(body, goStmt, accept) {
			return true
		}
	}
	return false
}

// loopPathDominates visits every dominating step between body and goStmt along g.stack.
func (g *goScanner) loopPathDominates(body *ast.BlockStmt, goStmt *ast.GoStmt, accept func(dominatingStep) bool) bool {
	start := g.stackNodeIndex(body)
	if start < 0 {
		return false
	}
	for idx := start; idx < len(g.stack); idx++ {
		nextNode := ast.Node(goStmt)
		if idx+1 < len(g.stack) {
			nextNode = g.stack[idx+1]
		}
		if stackFrameDominates(body, g.stack[idx], nextNode, goStmt.Pos(), accept) {
			return true
		}
	}
	return false
}

// stackFrameDominates emits dominating steps from one ancestor node on g.stack.
func stackFrameDominates(body *ast.BlockStmt, frame, nextNode ast.Node, goPos token.Pos, accept func(dominatingStep) bool) bool {
	if ifStmt, ok := frame.(*ast.IfStmt); ok {
		return nextNode == ifStmt.Body && accept(dominatingStep{
			loopBody: body, ifBody: ifStmt, endPos: ifStmt.Body.Pos(),
		})
	}
	if comm, ok := frame.(*ast.CommClause); ok && comm.Comm != nil {
		if accept(dominatingStep{loopBody: body, stmt: unwrapLabeledStmt(comm.Comm), endPos: comm.Comm.End()}) {
			return true
		}
	}
	stmts := frameStmtList(frame)
	for i := 0; i < len(stmts) && stmts[i].End() <= goPos; i++ {
		if accept(dominatingStep{
			loopBody: body,
			stmt:     unwrapLabeledStmt(stmts[i]),
			nextStmt: dominatingNextStmt(stmts, i, goPos),
			endPos:   stmts[i].End(),
		}) {
			return true
		}
	}
	return false
}

// dominatingNextStmt returns the unwrapped statement immediately following stmts[idx] when it also
// completes at or before goPos.
func dominatingNextStmt(stmts []ast.Stmt, idx int, goPos token.Pos) ast.Stmt {
	if idx+1 >= len(stmts) || stmts[idx+1].End() > goPos {
		return nil
	}
	return unwrapLabeledStmt(stmts[idx+1])
}

// frameStmtList returns the statement slice of a block, switch case or select comm clause.
func frameStmtList(frame ast.Node) []ast.Stmt {
	switch node := frame.(type) {
	case *ast.BlockStmt:
		return node.List
	case *ast.CaseClause:
		return node.Body
	case *ast.CommClause:
		return node.Body
	default:
		return nil
	}
}

// unwrapLabeledStmt strips a single label wrapper from stmt when present.
func unwrapLabeledStmt(stmt ast.Stmt) ast.Stmt {
	if labeled, ok := stmt.(*ast.LabeledStmt); ok {
		return labeled.Stmt
	}
	return stmt
}

// stackNodeIndex returns the index of target on g.stack, or -1 if absent.
func (g *goScanner) stackNodeIndex(target ast.Node) int {
	if target == nil {
		return -1
	}
	for i := len(g.stack) - 1; i >= 0; i-- {
		if g.stack[i] == target {
			return i
		}
	}
	return -1
}

// hasSemaphoreAcquireInLoops reports whether a dominating semaphore acquisition stays held until
// goStmt and is released by the launched goroutine.
func (g *goScanner) hasSemaphoreAcquireInLoops(loops []ast.Stmt, goStmt *ast.GoStmt, facts *fanoutFacts) bool {
	return g.walkDominatingSteps(loops, goStmt, func(step dominatingStep) bool {
		sem, ok := g.stepAcquiresSemaphore(step, facts)
		if !ok {
			return false
		}
		return !g.hasReleaseBetween(step.loopBody, step.endPos, goStmt.Pos(), func(n ast.Node) bool {
			return isSemaphoreReleaseSelector(n, sem)
		}) && g.goroutineReleasesSemaphore(goStmt, sem, facts)
	})
}

// stepAcquiresSemaphore extracts the acquired semaphore identifier from a dominating step.
func (g *goScanner) stepAcquiresSemaphore(step dominatingStep, facts *fanoutFacts) (string, bool) {
	if step.ifBody != nil {
		return ifBodyAcquiresSemaphore(step.ifBody, facts)
	}
	return g.stmtDominatesWithAcquire(step.stmt, step.nextStmt, facts)
}

// hasReleaseBetween reports whether any node in body overlapping [from, to) satisfies match before
// the launch or hits maxNodeStack before completing the scan.
func (g *goScanner) hasReleaseBetween(body *ast.BlockStmt, from, to token.Pos, match func(ast.Node) bool) bool {
	found := false
	complete := g.inspectBounded(body, func(n ast.Node) bool {
		if found || n.Pos() >= to || n.End() <= from {
			return false
		}
		if n.Pos() >= from && match(n) {
			found = true
			return false
		}
		return true
	})
	return found || !complete
}

// isSemaphoreReleaseSelector reports whether n is a sem.Release selector.
func isSemaphoreReleaseSelector(n ast.Node, sem string) bool {
	sel, ok := n.(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != "Release" {
		return false
	}
	recv, ok := receiverBaseIdent(sel.X)
	return ok && recv == sem
}

// goroutineReleasesSemaphore reports whether goStmt launches a function literal whose body
// releases sem in a top-level defer statement or in its final statement.
func (g *goScanner) goroutineReleasesSemaphore(goStmt *ast.GoStmt, sem string, facts *fanoutFacts) bool {
	return goroutineBodyCleanup(goStmt, func(node ast.Node) bool {
		return g.nodeReleasesSemaphore(node, sem, facts)
	})
}

// nodeReleasesSemaphore reports whether root unconditionally contains a positive-weight
// sem.Release call within maxNodeStack.
func (g *goScanner) nodeReleasesSemaphore(root ast.Node, sem string, facts *fanoutFacts) bool {
	found := false
	complete := g.inspectBounded(root, func(n ast.Node) bool {
		if found || isConditionalOrAsyncNode(n) {
			return false
		}
		if call, ok := n.(*ast.CallExpr); ok {
			if target, ok := semaphoreMethodCallTarget(call, "Release", facts); ok && target == sem {
				found = true
				return false
			}
		}
		return true
	})
	return found && complete
}

// goroutineBodyCleanup reports whether goStmt launches a function literal that executes a matching
// cleanup in a top-level defer statement or in its final statement.
func goroutineBodyCleanup(goStmt *ast.GoStmt, match func(ast.Node) bool) bool {
	lit, ok := ast.Unparen(goStmt.Call.Fun).(*ast.FuncLit)
	if !ok || lit.Body == nil || len(lit.Body.List) == 0 {
		return false
	}
	for i := 0; i < len(lit.Body.List); i++ {
		if def, ok := lit.Body.List[i].(*ast.DeferStmt); ok && match(def.Call) {
			return true
		}
	}
	return match(lit.Body.List[len(lit.Body.List)-1])
}

// isConditionalOrAsyncNode reports whether n introduces conditional, looping or asynchronous flow
// that prevents treating an enclosed cleanup as unconditional.
func isConditionalOrAsyncNode(n ast.Node) bool {
	switch n.(type) {
	case *ast.IfStmt, *ast.ForStmt, *ast.RangeStmt, *ast.SwitchStmt, *ast.TypeSwitchStmt, *ast.SelectStmt, *ast.GoStmt:
		return true
	default:
		return false
	}
}

// stmtDominatesWithAcquire returns the acquired semaphore name when stmt (paired with nextStmt in
// the same block for split assignments) acquires a tracked semaphore on every fallthrough path.
func (g *goScanner) stmtDominatesWithAcquire(stmt, nextStmt ast.Stmt, facts *fanoutFacts) (string, bool) {
	switch s := stmt.(type) {
	case *ast.AssignStmt:
		return g.splitAcquireDominates(s, nextStmt, facts)
	case *ast.IfStmt:
		return g.ifGuardDominatesWithAcquire(s, facts)
	default:
		return "", false
	}
}

// splitAcquireDominates matches `err := sem.Acquire(ctx, n)` (or `=`) when nextStmt in the same
// block is `if err != nil { <terminates> }` on that error identifier.
func (g *goScanner) splitAcquireDominates(assign *ast.AssignStmt, nextStmt ast.Stmt, facts *fanoutFacts) (string, bool) {
	errName, sem, ok := assignSemaphoreCallIdent(assign, "Acquire", facts)
	if !ok {
		return "", false
	}
	guard, ok := nextStmt.(*ast.IfStmt)
	if !ok || guard.Init != nil || !isIdentNilComparison(guard.Cond, errName, token.NEQ) {
		return "", false
	}
	if !g.blockTerminates(guard.Body) {
		return "", false
	}
	return sem, true
}

// ifGuardDominatesWithAcquire checks `if err := sem.Acquire(...); err != nil { <terminates> }`,
// `if sem.Acquire(...) != nil { <terminates> }`, and `if !sem.TryAcquire(n) { <terminates> }`.
func (g *goScanner) ifGuardDominatesWithAcquire(ifStmt *ast.IfStmt, facts *fanoutFacts) (string, bool) {
	if !g.blockTerminates(ifStmt.Body) {
		return "", false
	}
	if ifStmt.Init != nil {
		return initGuardSemaphoreTarget(ifStmt, facts, token.NEQ, isNegatedBoolIdent)
	}
	if sem, ok := acquireNilComparisonTarget(ifStmt.Cond, token.NEQ, facts); ok {
		return sem, true
	}
	return negatedTryAcquireTarget(ifStmt.Cond, facts)
}

// ifBodyAcquiresSemaphore checks whether entering ifStmt.Body proves a successful acquisition:
// `if sem.TryAcquire(n) { ... }` or `if err := sem.Acquire(...); err == nil { ... }`.
func ifBodyAcquiresSemaphore(ifStmt *ast.IfStmt, facts *fanoutFacts) (string, bool) {
	if ifStmt.Init != nil {
		return initGuardSemaphoreTarget(ifStmt, facts, token.EQL, isPositiveBoolIdent)
	}
	if sem, ok := acquireNilComparisonTarget(ifStmt.Cond, token.EQL, facts); ok {
		return sem, true
	}
	return positiveTryAcquireTarget(ifStmt.Cond, facts)
}

// initGuardSemaphoreTarget matches an if-initializer Acquire or TryAcquire checked in ifStmt.Cond.
func initGuardSemaphoreTarget(ifStmt *ast.IfStmt, facts *fanoutFacts, nilOp token.Token, boolPred func(ast.Expr, string) bool) (string, bool) {
	if errName, sem, ok := assignSemaphoreCallIdent(ifStmt.Init, "Acquire", facts); ok && isIdentNilComparison(ifStmt.Cond, errName, nilOp) {
		return sem, true
	}
	if okName, sem, ok := assignSemaphoreCallIdent(ifStmt.Init, "TryAcquire", facts); ok && boolPred(ifStmt.Cond, okName) {
		return sem, true
	}
	return "", false
}

// assignSemaphoreCallIdent extracts the non-blank LHS variable and semaphore receiver bound to
// sem.<method>(...) in initStmt.
func assignSemaphoreCallIdent(initStmt ast.Stmt, method string, facts *fanoutFacts) (string, string, bool) {
	assign, ok := initStmt.(*ast.AssignStmt)
	if !ok || (assign.Tok != token.DEFINE && assign.Tok != token.ASSIGN) || len(assign.Lhs) != 1 || len(assign.Rhs) != 1 {
		return "", "", false
	}
	ident, ok := assign.Lhs[0].(*ast.Ident)
	if !ok || ident.Name == "_" {
		return "", "", false
	}
	sem, ok := semaphoreMethodCallTarget(assign.Rhs[0], method, facts)
	if !ok {
		return "", "", false
	}
	return ident.Name, sem, true
}

// isIdentNilComparison reports whether cond compares name against nil with op (== or !=).
func isIdentNilComparison(cond ast.Expr, name string, op token.Token) bool {
	bin, ok := ast.Unparen(cond).(*ast.BinaryExpr)
	if !ok || bin.Op != op {
		return false
	}
	return (isNamedIdent(bin.X, name) && isNamedIdent(bin.Y, "nil")) ||
		(isNamedIdent(bin.X, "nil") && isNamedIdent(bin.Y, name))
}

// acquireNilComparisonTarget extracts the semaphore name when cond compares sem.Acquire(...) against nil with op.
func acquireNilComparisonTarget(cond ast.Expr, op token.Token, facts *fanoutFacts) (string, bool) {
	bin, ok := ast.Unparen(cond).(*ast.BinaryExpr)
	if !ok || bin.Op != op {
		return "", false
	}
	if isNamedIdent(bin.Y, "nil") {
		return semaphoreMethodCallTarget(bin.X, "Acquire", facts)
	}
	if isNamedIdent(bin.X, "nil") {
		return semaphoreMethodCallTarget(bin.Y, "Acquire", facts)
	}
	return "", false
}

// negatedTryAcquireTarget extracts the semaphore name when cond is `!sem.TryAcquire(n)` or `sem.TryAcquire(n) == false`.
func negatedTryAcquireTarget(cond ast.Expr, facts *fanoutFacts) (string, bool) {
	cond = ast.Unparen(cond)
	if u, ok := cond.(*ast.UnaryExpr); ok && u.Op == token.NOT {
		return semaphoreMethodCallTarget(u.X, "TryAcquire", facts)
	}
	return tryAcquireBoolComparisonTarget(cond, "false", facts)
}

// positiveTryAcquireTarget extracts the semaphore name when cond is `sem.TryAcquire(n)` or `sem.TryAcquire(n) == true`.
func positiveTryAcquireTarget(cond ast.Expr, facts *fanoutFacts) (string, bool) {
	cond = ast.Unparen(cond)
	if sem, ok := semaphoreMethodCallTarget(cond, "TryAcquire", facts); ok {
		return sem, true
	}
	return tryAcquireBoolComparisonTarget(cond, "true", facts)
}

// tryAcquireBoolComparisonTarget matches `sem.TryAcquire(n) == boolLit` in either operand order.
func tryAcquireBoolComparisonTarget(cond ast.Expr, boolLit string, facts *fanoutFacts) (string, bool) {
	bin, ok := cond.(*ast.BinaryExpr)
	if !ok || bin.Op != token.EQL {
		return "", false
	}
	if isNamedIdent(bin.Y, boolLit) {
		return semaphoreMethodCallTarget(bin.X, "TryAcquire", facts)
	}
	if isNamedIdent(bin.X, boolLit) {
		return semaphoreMethodCallTarget(bin.Y, "TryAcquire", facts)
	}
	return "", false
}

// isNegatedBoolIdent reports whether cond is `!name` or `name == false`.
func isNegatedBoolIdent(cond ast.Expr, name string) bool {
	cond = ast.Unparen(cond)
	if u, ok := cond.(*ast.UnaryExpr); ok && u.Op == token.NOT {
		return isNamedIdent(u.X, name)
	}
	return isBoolIdentComparison(cond, name, "false")
}

// isPositiveBoolIdent reports whether cond is `name` or `name == true`.
func isPositiveBoolIdent(cond ast.Expr, name string) bool {
	cond = ast.Unparen(cond)
	if isNamedIdent(cond, name) {
		return true
	}
	return isBoolIdentComparison(cond, name, "true")
}

// isBoolIdentComparison reports whether cond compares name against boolLit with ==.
func isBoolIdentComparison(cond ast.Expr, name, boolLit string) bool {
	bin, ok := cond.(*ast.BinaryExpr)
	if !ok || bin.Op != token.EQL {
		return false
	}
	return (isNamedIdent(bin.X, name) && isNamedIdent(bin.Y, boolLit)) ||
		(isNamedIdent(bin.X, boolLit) && isNamedIdent(bin.Y, name))
}

// isNamedIdent reports whether expr unwraps to an *ast.Ident with Name == want.
func isNamedIdent(expr ast.Expr, want string) bool {
	if expr == nil {
		return false
	}
	ident, ok := ast.Unparen(expr).(*ast.Ident)
	return ok && ident.Name == want
}

// semaphoreMethodCallTarget returns the semaphore identifier when expr calls method ("Acquire",
// "TryAcquire" or "Release") with a proven positive weight on an unshadowed semaphore.Weighted
// declared outside the enclosing loops.
func semaphoreMethodCallTarget(expr ast.Expr, method string, facts *fanoutFacts) (string, bool) {
	call, ok := ast.Unparen(expr).(*ast.CallExpr)
	if !ok || call.Ellipsis.IsValid() || !validSemaphoreArgs(method, call.Args, facts) {
		return "", false
	}
	sel, ok := ast.Unparen(call.Fun).(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != method {
		return "", false
	}
	recv, ok := receiverBaseIdent(sel.X)
	if !ok || !facts.isSemaphore(recv) {
		return "", false
	}
	return recv, true
}

// validSemaphoreArgs checks the argument count and positive weight for Acquire, TryAcquire and Release.
func validSemaphoreArgs(method string, args []ast.Expr, facts *fanoutFacts) bool {
	switch method {
	case "Acquire":
		return len(args) == 2 && isPositiveWeight(args[1], facts)
	case "TryAcquire", "Release":
		return len(args) == 1 && isPositiveWeight(args[0], facts)
	default:
		return false
	}
}

// maxTermDepth bounds the iterative branch-termination proof stack (HISS-02).
const maxTermDepth = 16

// blockTerminates reports whether block unconditionally exits the current loop iteration, loop,
// or function via return, break, continue, panic, os.Exit, testing Fatal*/FailNow, or log.Fatal*.
func (g *goScanner) blockTerminates(block *ast.BlockStmt) bool {
	if block == nil {
		return false
	}
	todo := []ast.Stmt{block}
	for steps := 0; len(todo) > 0; steps++ {
		if steps >= maxTermDepth {
			return false
		}
		cur := todo[len(todo)-1]
		todo = todo[:len(todo)-1]
		var ok bool
		todo, ok = g.expandTerminatingStmt(cur, todo)
		if !ok {
			return false
		}
	}
	return true
}

// expandTerminatingStmt checks one statement on the termination worklist or pushes sub-branches.
func (g *goScanner) expandTerminatingStmt(stmt ast.Stmt, todo []ast.Stmt) ([]ast.Stmt, bool) {
	switch s := stmt.(type) {
	case *ast.BlockStmt:
		return g.expandTerminatingBlock(s, todo)
	case *ast.IfStmt:
		if s.Else == nil {
			return nil, false
		}
		return append(todo, s.Body, s.Else), true
	case *ast.LabeledStmt:
		return append(todo, s.Stmt), true
	default:
		return todo, g.isLeafTerminator(s)
	}
}

// expandTerminatingBlock succeeds when any statement in b is a leaf terminator, or pushes the
// final statement onto todo.
func (g *goScanner) expandTerminatingBlock(b *ast.BlockStmt, todo []ast.Stmt) ([]ast.Stmt, bool) {
	if b == nil || len(b.List) == 0 {
		return nil, false
	}
	for i := 0; i < len(b.List); i++ {
		if g.isLeafTerminator(b.List[i]) {
			return todo, true
		}
	}
	return append(todo, b.List[len(b.List)-1]), true
}

// isLeafTerminator reports whether stmt directly terminates control flow.
func (g *goScanner) isLeafTerminator(stmt ast.Stmt) bool {
	switch s := stmt.(type) {
	case *ast.ReturnStmt:
		return true
	case *ast.BranchStmt:
		return s.Tok == token.BREAK || s.Tok == token.CONTINUE
	case *ast.ExprStmt:
		return g.isTerminatingCall(s.X)
	default:
		return false
	}
}

// isTerminatingCall recognizes panic, os.Exit, imported log.Fatal*, and testing-parameter
// Fatal/Fatalf/Fatalln/FailNow calls.
func (g *goScanner) isTerminatingCall(expr ast.Expr) bool {
	call, ok := ast.Unparen(expr).(*ast.CallExpr)
	if !ok {
		return false
	}
	if ident, ok := ast.Unparen(call.Fun).(*ast.Ident); ok && ident.Name == "panic" {
		return len(call.Args) == 1 && !call.Ellipsis.IsValid() && !bindsAt(g.enclosingFunc(), "panic", call.Pos())
	}
	if g.isUnshadowedOSExit(call) {
		return true
	}
	sel, ok := ast.Unparen(call.Fun).(*ast.SelectorExpr)
	return ok && g.isTerminatingSelectorCall(call, sel)
}

// isUnshadowedOSExit reports whether call invokes os.Exit(code) without shadowing the os binding.
func (g *goScanner) isUnshadowedOSExit(call *ast.CallExpr) bool {
	if len(call.Args) != 1 || call.Ellipsis.IsValid() || !g.matchesPkgSelector(call.Fun, "os", "os", "Exit") {
		return false
	}
	if sel, ok := ast.Unparen(call.Fun).(*ast.SelectorExpr); ok {
		if pkg, ok := ast.Unparen(sel.X).(*ast.Ident); ok {
			return !bindsAt(g.enclosingFunc(), pkg.Name, call.Pos())
		}
	}
	return !bindsAt(g.enclosingFunc(), "Exit", call.Pos())
}

// isTerminatingSelectorCall checks imported log.Fatal* and testing-parameter Fatal*/FailNow calls.
func (g *goScanner) isTerminatingSelectorCall(call *ast.CallExpr, sel *ast.SelectorExpr) bool {
	switch sel.Sel.Name {
	case "FailNow":
		return len(call.Args) == 0 && !call.Ellipsis.IsValid() && g.isTestingParamReceiver(sel.X, call.Pos())
	case "Fatal", "Fatalf", "Fatalln":
		return len(call.Args) >= 1 && (g.isImportedLogReceiver(sel.X, call.Pos()) || g.isTestingParamReceiver(sel.X, call.Pos()))
	default:
		return false
	}
}

// isTestingParamReceiver reports whether expr's base identifier is the enclosing testing parameter at pos.
func (g *goScanner) isTestingParamReceiver(expr ast.Expr, pos token.Pos) bool {
	recv, ok := receiverBaseIdent(expr)
	return ok && recv != "" && recv == g.enclosingTestingParam(pos)
}

// isImportedLogReceiver reports whether expr is an unshadowed package identifier bound to "log".
func (g *goScanner) isImportedLogReceiver(expr ast.Expr, pos token.Pos) bool {
	pkg, ok := ast.Unparen(expr).(*ast.Ident)
	if !ok || !g.imports.Binds(pkg.Name, "log") {
		return false
	}
	return !bindsAt(g.enclosingFunc(), pkg.Name, pos)
}

// enclosingTestingParam returns the enclosing FuncDecl's testing parameter (*testing.T,
// *testing.B or testing.TB, resolved through FileImports) when unshadowed at pos.
func (g *goScanner) enclosingTestingParam(pos token.Pos) string {
	fn := g.enclosingFunc()
	if fn == nil || fn.Type == nil {
		return ""
	}
	param := hotTestingParam(fn, g.imports)
	if param == "" || testingParamShadowedAt(fn, param, pos) {
		return ""
	}
	return param
}

// testingParamShadowedAt reports whether an inner scope inside fn rebinds param at pos.
func testingParamShadowedAt(fn *ast.FuncDecl, param string, pos token.Pos) bool {
	var s goScope
	shadowed, reached := false, false
	s.inspect(fn, func(n ast.Node) bool {
		if !reached && n.Pos() >= pos {
			shadowed, reached = s.count(param) > 1, true
		}
		return !reached
	})
	return shadowed || !reached
}

// hasTokenChannelBound reports whether a dominating send on a buffered token channel declared
// outside the enclosing loops stays unreceived before goStmt and is paired with a deferred or
// final-statement receive in goStmt.
func (g *goScanner) hasTokenChannelBound(loops []ast.Stmt, goStmt *ast.GoStmt, facts *fanoutFacts) bool {
	return g.walkDominatingSteps(loops, goStmt, func(step dominatingStep) bool {
		ch, ok := stepSendsToken(step, facts)
		if !ok || g.hasReleaseBetween(step.loopBody, step.endPos, goStmt.Pos(), func(n ast.Node) bool {
			return isTokenChanReceive(n, ch)
		}) {
			return false
		}
		return goroutineBodyCleanup(goStmt, func(node ast.Node) bool {
			return g.nodeReceivesFromChan(node, ch)
		})
	})
}

// isTokenChanReceive reports whether n receives from ch via <-ch or range ch.
func isTokenChanReceive(n ast.Node, ch string) bool {
	switch x := n.(type) {
	case *ast.UnaryExpr:
		return x.Op == token.ARROW && isNamedIdent(x.X, ch)
	case *ast.RangeStmt:
		return isNamedIdent(x.X, ch)
	default:
		return false
	}
}

// stepSendsToken extracts the buffered token channel name when step is a dominating channel send.
func stepSendsToken(step dominatingStep, facts *fanoutFacts) (string, bool) {
	send, ok := step.stmt.(*ast.SendStmt)
	if !ok {
		return "", false
	}
	ident, ok := ast.Unparen(send.Chan).(*ast.Ident)
	if !ok || !facts.isTokenChan(ident.Name) {
		return "", false
	}
	return ident.Name, true
}

// nodeReceivesFromChan reports whether root unconditionally contains a channel receive <-ch within
// maxNodeStack.
func (g *goScanner) nodeReceivesFromChan(root ast.Node, ch string) bool {
	found := false
	complete := g.inspectBounded(root, func(n ast.Node) bool {
		if found || isConditionalOrAsyncNode(n) {
			return false
		}
		u, ok := n.(*ast.UnaryExpr)
		if !ok || u.Op != token.ARROW {
			return true
		}
		if isNamedIdent(u.X, ch) {
			found = true
			return false
		}
		return true
	})
	return found && complete
}

// isLoopBounded checks bounds (d) array range / constant counter loop and (e) //hiss:bound
// worker pool loop on one enclosing loop.
func (g *goScanner) isLoopBounded(loop ast.Stmt, facts *fanoutFacts) bool {
	switch l := loop.(type) {
	case *ast.RangeStmt:
		if isArrayRangeExpr(l.X, facts) {
			return true
		}
	case *ast.ForStmt:
		if g.isConstCounterLoop(l, facts) {
			return true
		}
	}
	target, ok := g.loopHissBound(loop, facts)
	return ok && g.isHissBoundLoop(loop, target, facts)
}

// isArrayRangeExpr reports whether a range expression has an unshadowed fixed array type declared
// outside the enclosing loops.
func isArrayRangeExpr(expr ast.Expr, facts *fanoutFacts) bool {
	if isFixedArrayValue(expr) {
		return true
	}
	if call, ok := ast.Unparen(expr).(*ast.CallExpr); ok && isFixedArrayOrPtrType(call.Fun) {
		return true
	}
	name, ok := receiverBaseIdent(expr)
	return ok && facts.isArray(name)
}

// counterStepInfo describes a loop counter's direction (+1 or -1) and whether its magnitude is 1.
type counterStepInfo struct {
	dir  int
	unit bool
}

// validCounterLoop validates a counter loop's constant initializer, post-step, and absence of
// counter writes or address-taking in loop.Body.
func (g *goScanner) validCounterLoop(loop *ast.ForStmt, facts *fanoutFacts) (string, ast.Expr, counterStepInfo, bool) {
	counter, initExpr, ok := g.counterInit(loop.Init, facts)
	if !ok {
		return "", nil, counterStepInfo{}, false
	}
	step, ok := counterStep(loop.Post, counter, facts)
	if !ok {
		return "", nil, counterStepInfo{}, false
	}
	if g.bodyInvalidatesCounter(loop.Body, counter) {
		return "", nil, counterStepInfo{}, false
	}
	return counter, initExpr, step, true
}

// isConstCounterLoop reports whether loop is a counter loop bounded by a constant expression with
// a nonzero constant step advancing toward that bound and an unmodified counter in loop.Body.
func (g *goScanner) isConstCounterLoop(loop *ast.ForStmt, facts *fanoutFacts) bool {
	counter, initExpr, step, ok := g.validCounterLoop(loop, facts)
	if !ok {
		return false
	}
	conjuncts := condConjuncts(loop.Cond)
	for i := 0; i < len(conjuncts); i++ {
		bound, op, ok := counterCondBound(conjuncts[i], counter)
		if ok && g.isConstBoundExpr(bound, facts) && stepApproachesBound(op, step, initExpr, bound) {
			return true
		}
	}
	return false
}

// bodyInvalidatesCounter reports whether body writes to counter, takes &counter, or hits
// maxNodeStack before completing the scan.
func (g *goScanner) bodyInvalidatesCounter(body *ast.BlockStmt, counter string) bool {
	if body == nil {
		return false
	}
	invalid := false
	complete := g.inspectBounded(body, func(n ast.Node) bool {
		if invalid {
			return false
		}
		if lit, ok := n.(*ast.FuncLit); ok && funcTypeBindsName(lit.Type, counter) {
			return false
		}
		if nodeInvalidatesCounter(n, counter) {
			invalid = true
			return false
		}
		return true
	})
	return invalid || !complete
}

// nodeInvalidatesCounter reports whether n directly writes to counter or takes &counter.
func nodeInvalidatesCounter(n ast.Node, counter string) bool {
	switch x := n.(type) {
	case *ast.AssignStmt:
		return exprListNamesIdent(x.Lhs, counter)
	case *ast.IncDecStmt:
		return isNamedIdent(x.X, counter)
	case *ast.RangeStmt:
		return isNamedIdent(x.Key, counter) || isNamedIdent(x.Value, counter)
	case *ast.UnaryExpr:
		return x.Op == token.AND && isNamedIdent(x.X, counter)
	default:
		return false
	}
}

// exprListNamesIdent reports whether any expression in exprs unwraps to an identifier named want.
func exprListNamesIdent(exprs []ast.Expr, want string) bool {
	for i := 0; i < len(exprs); i++ {
		if isNamedIdent(exprs[i], want) {
			return true
		}
	}
	return false
}

// funcTypeBindsName reports whether ft declares name as a parameter or named result.
func funcTypeBindsName(ft *ast.FuncType, name string) bool {
	if ft == nil {
		return false
	}
	return fieldListBindsName(ft.Params, name) || fieldListBindsName(ft.Results, name)
}

// fieldListBindsName reports whether list declares an identifier equal to name.
func fieldListBindsName(list *ast.FieldList, name string) bool {
	if list == nil {
		return false
	}
	for i := 0; i < len(list.List); i++ {
		for j := 0; j < len(list.List[i].Names); j++ {
			if ident := list.List[i].Names[j]; ident != nil && ident.Name == name {
				return true
			}
		}
	}
	return false
}

// maxCondConjuncts bounds the && terms one loop condition is split into (HISS-02).
const maxCondConjuncts = 16

// condConjuncts splits cond on && into its terms: a loop runs only while every term holds, so
// one term comparing the counter with a constant bounds the loop. Iterative (HISS-01).
func condConjuncts(cond ast.Expr) []ast.Expr {
	var terms []ast.Expr
	stack := []ast.Expr{cond}
	for len(stack) > 0 && len(terms)+len(stack) <= maxCondConjuncts {
		top := ast.Unparen(stack[len(stack)-1])
		stack = stack[:len(stack)-1]
		if bin, ok := top.(*ast.BinaryExpr); ok && bin.Op == token.LAND {
			stack = append(stack, bin.X, bin.Y)
			continue
		}
		terms = append(terms, top)
	}
	return terms
}

// counterInit returns the loop variable initialized in initStmt and its initial expression when
// initialized to a constant expression.
func (g *goScanner) counterInit(initStmt ast.Stmt, facts *fanoutFacts) (string, ast.Expr, bool) {
	assign, ok := initStmt.(*ast.AssignStmt)
	if !ok || len(assign.Lhs) != 1 || len(assign.Rhs) != 1 {
		return "", nil, false
	}
	ident, ok := assign.Lhs[0].(*ast.Ident)
	if !ok || ident.Name == "_" || !g.isConstBoundExpr(assign.Rhs[0], facts) {
		return "", nil, false
	}
	return ident.Name, assign.Rhs[0], true
}

// counterCondBound returns the bound expression and counter-on-left relational operator when cond
// compares counter against a bound, flipping the operator when counter sits on the right.
func counterCondBound(cond ast.Expr, counter string) (ast.Expr, token.Token, bool) {
	bin, ok := ast.Unparen(cond).(*ast.BinaryExpr)
	if !ok || !isCounterComparisonOp(bin.Op) {
		return nil, token.ILLEGAL, false
	}
	if left, ok := ast.Unparen(bin.X).(*ast.Ident); ok && left.Name == counter {
		return bin.Y, bin.Op, true
	}
	if right, ok := ast.Unparen(bin.Y).(*ast.Ident); ok && right.Name == counter {
		return bin.X, flipComparisonOp(bin.Op), true
	}
	return nil, token.ILLEGAL, false
}

// isCounterComparisonOp reports whether op is a relational operator bounding a counter loop.
func isCounterComparisonOp(op token.Token) bool {
	switch op {
	case token.LSS, token.LEQ, token.GTR, token.GEQ, token.NEQ:
		return true
	default:
		return false
	}
}

// flipComparisonOp reverses a relational operator when the loop counter is on the right-hand side.
func flipComparisonOp(op token.Token) token.Token {
	switch op {
	case token.LSS:
		return token.GTR
	case token.LEQ:
		return token.GEQ
	case token.GTR:
		return token.LSS
	case token.GEQ:
		return token.LEQ
	default:
		return op
	}
}

// counterStep extracts the direction and unit-step flag of postStmt when it updates counter by
// ++/-- or +=/-= with a positive integer literal or declared constant.
func counterStep(postStmt ast.Stmt, counter string, facts *fanoutFacts) (counterStepInfo, bool) {
	switch post := postStmt.(type) {
	case *ast.IncDecStmt:
		if !isNamedIdent(post.X, counter) {
			return counterStepInfo{}, false
		}
		if post.Tok == token.INC {
			return counterStepInfo{dir: 1, unit: true}, true
		}
		if post.Tok == token.DEC {
			return counterStepInfo{dir: -1, unit: true}, true
		}
		return counterStepInfo{}, false
	case *ast.AssignStmt:
		return assignCounterStep(post, counter, facts)
	default:
		return counterStepInfo{}, false
	}
}

// assignCounterStep extracts the step direction of a += or -= post statement on counter.
func assignCounterStep(post *ast.AssignStmt, counter string, facts *fanoutFacts) (counterStepInfo, bool) {
	if len(post.Lhs) != 1 || len(post.Rhs) != 1 {
		return counterStepInfo{}, false
	}
	if post.Tok != token.ADD_ASSIGN && post.Tok != token.SUB_ASSIGN {
		return counterStepInfo{}, false
	}
	if !isNamedIdent(post.Lhs[0], counter) || !isPositiveConstStep(post.Rhs[0], facts) {
		return counterStepInfo{}, false
	}
	if post.Tok == token.SUB_ASSIGN {
		return counterStepInfo{dir: -1, unit: false}, true
	}
	return counterStepInfo{dir: 1, unit: false}, true
}

// isPositiveConstStep reports whether expr is a positive integer literal or declared positive
// literal constant.
func isPositiveConstStep(expr ast.Expr, facts *fanoutFacts) bool {
	if expr == nil {
		return false
	}
	expr = ast.Unparen(expr)
	if u, ok := expr.(*ast.UnaryExpr); ok && u.Op == token.ADD {
		expr = ast.Unparen(u.X)
	}
	switch x := expr.(type) {
	case *ast.BasicLit:
		if x.Kind != token.INT {
			return false
		}
		val, err := strconv.ParseUint(x.Value, 0, 64)
		return err == nil && val > 0
	case *ast.Ident:
		return facts.posConsts[x.Name]
	default:
		return false
	}
}

// stepApproachesBound reports whether step advances toward bound under op (with counter on the
// left). != requires integer-literal endpoints and a unit step (++ or --) not moving away.
func stepApproachesBound(op token.Token, step counterStepInfo, initExpr, boundExpr ast.Expr) bool {
	switch op {
	case token.LSS, token.LEQ:
		return step.dir > 0
	case token.GTR, token.GEQ:
		return step.dir < 0
	case token.NEQ:
		return neqStepApproachesLiteralBound(step, initExpr, boundExpr)
	default:
		return false
	}
}

// neqStepApproachesLiteralBound checks != counter termination: both endpoints must be integer
// literals read by signedIntLiteral, and a unit step must not move away from the bound.
func neqStepApproachesLiteralBound(step counterStepInfo, initExpr, boundExpr ast.Expr) bool {
	if !step.unit {
		return false
	}
	initVal, okInit := signedIntLiteral(initExpr)
	boundVal, okBound := signedIntLiteral(boundExpr)
	if !okInit || !okBound {
		return false
	}
	if initVal == boundVal {
		return true
	}
	if initVal < boundVal {
		return step.dir > 0
	}
	return step.dir < 0
}

// signedIntLiteral parses a plain or +/- signed integer literal into an int64.
func signedIntLiteral(expr ast.Expr) (int64, bool) {
	if expr == nil {
		return 0, false
	}
	expr = ast.Unparen(expr)
	neg := false
	if u, ok := expr.(*ast.UnaryExpr); ok && (u.Op == token.SUB || u.Op == token.ADD) {
		neg = u.Op == token.SUB
		expr = ast.Unparen(u.X)
	}
	lit, ok := expr.(*ast.BasicLit)
	if !ok || lit.Kind != token.INT {
		return 0, false
	}
	val, err := strconv.ParseInt(lit.Value, 0, 64)
	if err != nil {
		return 0, false
	}
	if neg {
		return -val, true
	}
	return val, true
}

// isConstBoundExpr reports whether expr is a constant integer expression (literal, declared
// const, len of array/string literal, or arithmetic combination of constants) within maxNodeStack.
func (g *goScanner) isConstBoundExpr(expr ast.Expr, facts *fanoutFacts) bool {
	if expr == nil {
		return false
	}
	valid, hasLeaf := true, false
	complete := g.inspectBounded(expr, func(n ast.Node) bool {
		if !valid {
			return false
		}
		descend, leaf, ok := classifyConstNode(n, facts)
		valid = ok
		hasLeaf = hasLeaf || leaf
		return descend && ok
	})
	return complete && valid && hasLeaf
}

// classifyConstNode decides one AST node inside isConstBoundExpr.
func classifyConstNode(n ast.Node, facts *fanoutFacts) (descend, leaf, ok bool) {
	switch x := n.(type) {
	case *ast.BasicLit:
		return false, true, x.Kind == token.INT || x.Kind == token.CHAR
	case *ast.Ident:
		return false, true, facts.consts[x.Name]
	case *ast.CallExpr:
		return false, true, isConstLenCall(x, facts)
	case *ast.BinaryExpr, *ast.UnaryExpr, *ast.ParenExpr:
		return true, false, true
	default:
		return false, false, false
	}
}

// isConstLenCall reports whether call is len(x) on a fixed array or string literal.
func isConstLenCall(call *ast.CallExpr, facts *fanoutFacts) bool {
	if len(call.Args) != 1 || call.Ellipsis.IsValid() || facts.lenRedeclared {
		return false
	}
	if !isNamedIdent(call.Fun, "len") {
		return false
	}
	if lit, ok := ast.Unparen(call.Args[0]).(*ast.BasicLit); ok && lit.Kind == token.STRING {
		return true
	}
	return isArrayRangeExpr(call.Args[0], facts)
}

// loopHissBound returns the target identifier or constant named by a //hiss:bound comment on or
// immediately above loop.
func (g *goScanner) loopHissBound(loop ast.Stmt, facts *fanoutFacts) (string, bool) {
	loopLine := g.line(loop.Pos())
	from := max(loopLine-1, 1)
	for l := from; l <= loopLine && l-from <= maxSafetyLookback; l++ {
		if target, ok := facts.hissBounds[l]; ok {
			return target, true
		}
	}
	return "", false
}

// isHissBoundLoop reports whether loop is bounded by a constant or by the explicit int variable
// named in target.
func (g *goScanner) isHissBoundLoop(loop ast.Stmt, target string, facts *fanoutFacts) bool {
	switch l := loop.(type) {
	case *ast.ForStmt:
		return g.isConstCounterLoop(l, facts) || g.isTargetCounterLoop(l, target, facts)
	case *ast.RangeStmt:
		return g.matchesHissBoundTarget(l.X, target, facts)
	default:
		return false
	}
}

// isTargetCounterLoop reports whether loop advances an unmodified constant-initialized counter
// toward target.
func (g *goScanner) isTargetCounterLoop(loop *ast.ForStmt, target string, facts *fanoutFacts) bool {
	counter, initExpr, step, ok := g.validCounterLoop(loop, facts)
	if !ok {
		return false
	}
	bound, op, ok := counterCondBound(loop.Cond, counter)
	return ok && g.matchesHissBoundTarget(bound, target, facts) && stepApproachesBound(op, step, initExpr, bound)
}

// matchesHissBoundTarget reports whether expr is a constant or the explicit int variable named
// in target.
func (g *goScanner) matchesHissBoundTarget(expr ast.Expr, target string, facts *fanoutFacts) bool {
	if g.isConstBoundExpr(expr, facts) {
		return true
	}
	ident, ok := ast.Unparen(expr).(*ast.Ident)
	return ok && ident.Name == target && (facts.ints[target] || facts.consts[target])
}

// hissBoundComments maps comment end lines to the target identifier or constant stated in a
// valid //hiss:bound directive. Comments are read from comment.Text directly because
// ast.CommentGroup.Text strips //tool:directive lines.
func hissBoundComments(fset *token.FileSet, file *ast.File) map[int]string {
	bounds := make(map[int]string)
	if fset == nil || file == nil {
		return bounds
	}
	for i := 0; i < len(file.Comments); i++ {
		group := file.Comments[i]
		if group == nil {
			continue
		}
		if target, ok := groupHissBound(group); ok {
			bounds[fset.Position(group.End()).Line] = target
		}
	}
	return bounds
}

// groupHissBound extracts a validated //hiss:bound target from a comment group.
func groupHissBound(group *ast.CommentGroup) (string, bool) {
	for i := 0; i < len(group.List); i++ {
		comment := group.List[i]
		if !validCommentText(comment) {
			continue
		}
		if target, ok := parseHissBoundComment(comment.Text); ok {
			return target, true
		}
	}
	return "", false
}

// parseHissBoundComment parses one //hiss:bound directive line into its target name.
func parseHissBoundComment(raw string) (string, bool) {
	text := strings.TrimPrefix(raw, "//")
	if strings.HasPrefix(raw, "/*") && strings.HasSuffix(raw, "*/") {
		text = raw[2 : len(raw)-2]
	}
	lines := strings.Split(text, "\n")
	for i := 0; i < len(lines); i++ {
		rest, ok := strings.CutPrefix(strings.TrimSpace(lines[i]), "hiss:bound")
		if !ok {
			continue
		}
		if target, valid := parseHissBoundFields(rest); valid {
			return target, true
		}
	}
	return "", false
}

// parseHissBoundFields validates that a hiss:bound directive names a target, a positive bound
// value, and a non-placeholder reason.
func parseHissBoundFields(rest string) (string, bool) {
	rest = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(rest), ":"))
	fields := strings.Fields(rest)
	if len(fields) == 0 {
		return "", false
	}
	target, value, reason := splitHissBoundTokens(fields)
	if target == "" || !hasPositiveDigit(value) || !isJustification(reason) {
		return "", false
	}
	return target, true
}

// splitHissBoundTokens splits the tokens after hiss:bound into target, bound value and reason.
func splitHissBoundTokens(fields []string) (target, value string, reason []string) {
	first := fields[0]
	if lhs, rhs, ok := strings.Cut(first, "<="); ok {
		return lhs, rhs, fields[1:]
	}
	if lhs, rhs, ok := strings.Cut(first, "="); ok {
		return lhs, rhs, fields[1:]
	}
	if hasPositiveDigit(first) {
		return first, first, fields[1:]
	}
	if len(fields) < 2 {
		return "", "", nil
	}
	target = strings.TrimSuffix(first, ":")
	value = strings.TrimLeft(fields[1], "<=:")
	return target, value, fields[2:]
}

// hasPositiveDigit reports whether s contains at least one positive ASCII digit and no leading
// minus sign.
func hasPositiveDigit(s string) bool {
	if s == "" || strings.HasPrefix(s, "-") {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] >= '1' && s[i] <= '9' {
			return true
		}
	}
	return false
}
