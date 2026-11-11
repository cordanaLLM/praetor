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
	consts     map[string]bool
	posConsts  map[string]bool
	arrays     map[string]token.Pos
	ints       map[string]bool
	errgroups  map[string]token.Pos
	semaphores map[string]token.Pos
	tokenChans map[string]token.Pos
	hissBounds map[int]string
	loops      []ast.Stmt
	fnRoot     ast.Node
	goPos      token.Pos
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
	delete(f.ints, name)
	delete(f.errgroups, name)
	delete(f.semaphores, name)
	delete(f.tokenChans, name)
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

// checkGoFanout reports a go statement inside a for or range body when any enclosing loop lacks
// a HISS-06 concurrency bound.
func (g *goScanner) checkGoFanout(goStmt *ast.GoStmt) {
	loops, fnRoot := g.enclosingFanoutLoops()
	if len(loops) == 0 || fnRoot == nil {
		return
	}
	facts := g.collectFanoutFacts(fnRoot, loops, goStmt.Pos())
	for i := 0; i < len(loops); i++ {
		if !g.isLoopBounded(loops[i], &facts) && !g.hasGoStmtLimiter(loops[:i+1], goStmt, &facts) {
			fnName := g.enclosingFunctionName()
			line := g.line(goStmt.Pos())
			g.record("HISS-06", goStmt.Pos(), fnName, fanoutFindingMessage(fnName, line))
			return
		}
	}
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

// collectFanoutFacts gathers file-level and function-level declarations for HISS-06.
func (g *goScanner) collectFanoutFacts(fnRoot ast.Node, loops []ast.Stmt, goPos token.Pos) fanoutFacts {
	facts := fanoutFacts{
		consts:     make(map[string]bool),
		posConsts:  make(map[string]bool),
		arrays:     make(map[string]token.Pos),
		ints:       make(map[string]bool),
		errgroups:  make(map[string]token.Pos),
		semaphores: make(map[string]token.Pos),
		tokenChans: make(map[string]token.Pos),
		hissBounds: hissBoundComments(g.fset, g.file),
		loops:      loops,
		fnRoot:     fnRoot,
		goPos:      goPos,
	}
	fileDecls := g.collectFileFanoutFacts(&facts)
	g.collectFuncFanoutFacts(fnRoot, fileDecls, &facts)
	return facts
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
			g.recordGenDeclFacts(gen, facts)
		}
	}
	return fileDecls
}

// recordGenDeclFacts records const and var specifications from one declaration group.
func (g *goScanner) recordGenDeclFacts(gen *ast.GenDecl, facts *fanoutFacts) {
	for i := 0; i < len(gen.Specs); i++ {
		spec, ok := gen.Specs[i].(*ast.ValueSpec)
		if !ok {
			continue
		}
		if gen.Tok == token.CONST {
			recordConstSpecFacts(spec, i, facts)
			continue
		}
		if gen.Tok == token.VAR {
			g.recordValueSpecFacts(spec, facts)
		}
	}
}

// recordConstSpecFacts marks declared constants and tracks which ones can serve as positive steps.
func recordConstSpecFacts(spec *ast.ValueSpec, specIndex int, facts *fanoutFacts) {
	for i := 0; i < len(spec.Names); i++ {
		ident := spec.Names[i]
		if ident == nil || ident.Name == "_" {
			continue
		}
		facts.consts[ident.Name] = true
		if isPositiveConstSpecValue(spec, i, specIndex) {
			facts.posConsts[ident.Name] = true
		}
	}
}

// isPositiveConstSpecValue reports whether the i-th name of a const ValueSpec can represent a
// nonzero positive integer step.
func isPositiveConstSpecValue(spec *ast.ValueSpec, idx, specIndex int) bool {
	if len(spec.Values) == 0 {
		return specIndex > 0
	}
	if idx >= len(spec.Values) {
		return false
	}
	val := ast.Unparen(spec.Values[idx])
	if ident, ok := val.(*ast.Ident); ok && ident.Name == "iota" {
		return specIndex > 0
	}
	return isPositiveBoundArg(val)
}

// recordValueSpecFacts classifies a var ValueSpec into arrays, errgroups, semaphores, ints or
// buffered token channels.
func (g *goScanner) recordValueSpecFacts(spec *ast.ValueSpec, facts *fanoutFacts) {
	if isArrayValueSpec(spec) {
		markSpecPos(spec.Names, facts.arrays)
	}
	if g.isErrgroupSpec(spec) {
		markSpecPos(spec.Names, facts.errgroups)
	}
	if g.isSemaphoreSpec(spec) {
		markSpecPos(spec.Names, facts.semaphores)
	}
	if isIntValueSpec(spec) {
		markSpecBool(spec.Names, facts.ints)
	}
	if isBufferedChanSpec(spec) {
		markSpecPos(spec.Names, facts.tokenChans)
	}
}

// markSpecBool sets target[name] = true for each non-blank identifier in names.
func markSpecBool(names []*ast.Ident, target map[string]bool) {
	for i := 0; i < len(names); i++ {
		if names[i] != nil && names[i].Name != "_" {
			target[names[i].Name] = true
		}
	}
}

// markSpecPos records each non-blank identifier's declaring token.Pos in target.
func markSpecPos(names []*ast.Ident, target map[string]token.Pos) {
	for i := 0; i < len(names); i++ {
		if names[i] != nil && names[i].Name != "_" {
			target[names[i].Name] = names[i].Pos()
		}
	}
}

// collectFuncFanoutFacts walks fnRoot to record parameters and local bindings, then drops any
// name declared more than once across fnRoot or shadowing a file-level binding.
func (g *goScanner) collectFuncFanoutFacts(fnRoot ast.Node, fileDecls map[string]int, facts *fanoutFacts) {
	localDecls := countFuncDecls(fnRoot)
	for name := range localDecls {
		facts.clearName(name)
	}
	steps := 0
	ast.Inspect(fnRoot, func(n ast.Node) bool {
		if n == nil || steps >= maxNodeStack {
			return false
		}
		steps++
		g.recordFuncNodeFacts(n, facts)
		return true
	})
	for name, count := range localDecls {
		if count+fileDecls[name] > 1 {
			facts.clearName(name)
		}
	}
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

// countFuncDecls counts how many times each non-blank identifier is declared inside fnRoot.
func countFuncDecls(fnRoot ast.Node) map[string]int {
	counts := make(map[string]int)
	steps := 0
	ast.Inspect(fnRoot, func(n ast.Node) bool {
		if n == nil || steps >= maxNodeStack {
			return false
		}
		steps++
		countNodeDecls(n, counts)
		return true
	})
	return counts
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
	switch {
	case isFixedArrayOrPtrType(expr):
		markSpecPos(names, facts.arrays)
	case g.isErrgroupType(expr):
		markSpecPos(names, facts.errgroups)
	case g.isSemaphoreType(expr):
		markSpecPos(names, facts.semaphores)
	case isIntTypeExpr(expr):
		markSpecBool(names, facts.ints)
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
	case g.isErrgroupValue(rhs):
		recordBoundPos(facts.errgroups, name, pos, facts)
	case g.isSemaphoreValue(rhs):
		recordBoundPos(facts.semaphores, name, pos, facts)
	case isBufferedChanMake(rhs):
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

// isArrayValueSpec reports whether a var specification declares or initializes a fixed array.
func isArrayValueSpec(spec *ast.ValueSpec) bool {
	if isFixedArrayOrPtrType(spec.Type) {
		return true
	}
	for i := 0; i < len(spec.Values); i++ {
		if isFixedArrayValue(spec.Values[i]) {
			return true
		}
	}
	return false
}

// isFixedArrayOrPtrType reports whether expr is [N]T, [...]T, or a pointer to one.
func isFixedArrayOrPtrType(expr ast.Expr) bool {
	if expr == nil {
		return false
	}
	expr = ast.Unparen(expr)
	if star, ok := expr.(*ast.StarExpr); ok {
		expr = ast.Unparen(star.X)
	}
	arr, ok := expr.(*ast.ArrayType)
	return ok && arr.Len != nil
}

// isFixedArrayValue reports whether expr is a composite literal or conversion of a fixed array.
func isFixedArrayValue(expr ast.Expr) bool {
	if expr == nil {
		return false
	}
	expr = ast.Unparen(expr)
	if u, ok := expr.(*ast.UnaryExpr); ok && u.Op == token.AND {
		expr = ast.Unparen(u.X)
	}
	if comp, ok := expr.(*ast.CompositeLit); ok {
		return isFixedArrayOrPtrType(comp.Type)
	}
	return false
}

// isIntValueSpec reports whether a var specification has an integer type or integer value.
func isIntValueSpec(spec *ast.ValueSpec) bool {
	if isIntTypeExpr(spec.Type) {
		return true
	}
	for i := 0; i < len(spec.Values); i++ {
		if isIntCandidateValue(spec.Values[i]) {
			return true
		}
	}
	return false
}

// isIntTypeExpr reports whether expr names a signed or unsigned integer type.
func isIntTypeExpr(expr ast.Expr) bool {
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

// isErrgroupSpec reports whether spec declares an errgroup.Group or *errgroup.Group.
func (g *goScanner) isErrgroupSpec(spec *ast.ValueSpec) bool {
	if g.isErrgroupType(spec.Type) {
		return true
	}
	for i := 0; i < len(spec.Values); i++ {
		if g.isErrgroupValue(spec.Values[i]) {
			return true
		}
	}
	return false
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

// isSemaphoreSpec reports whether spec declares a semaphore.Weighted or *semaphore.Weighted.
func (g *goScanner) isSemaphoreSpec(spec *ast.ValueSpec) bool {
	if g.isSemaphoreType(spec.Type) {
		return true
	}
	for i := 0; i < len(spec.Values); i++ {
		if g.isSemaphoreValue(spec.Values[i]) {
			return true
		}
	}
	return false
}

// isSemaphoreType reports whether expr spells semaphore.Weighted or *semaphore.Weighted.
func (g *goScanner) isSemaphoreType(expr ast.Expr) bool {
	return g.matchesPkgType(expr, "golang.org/x/sync/semaphore", "semaphore", "Weighted")
}

// isSemaphoreValue reports whether expr constructs a *semaphore.Weighted via NewWeighted or
// composite literal.
func (g *goScanner) isSemaphoreValue(expr ast.Expr) bool {
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
		return g.matchesPkgSelector(x.Fun, "golang.org/x/sync/semaphore", "semaphore", "NewWeighted")
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

// isBufferedChanSpec reports whether spec initializes a buffered channel.
func isBufferedChanSpec(spec *ast.ValueSpec) bool {
	for i := 0; i < len(spec.Values); i++ {
		if isBufferedChanMake(spec.Values[i]) {
			return true
		}
	}
	return false
}

// isBufferedChanMake reports whether expr is make(chan T, cap) with a positive capacity.
func isBufferedChanMake(expr ast.Expr) bool {
	call, ok := ast.Unparen(expr).(*ast.CallExpr)
	if !ok || len(call.Args) != 2 || call.Ellipsis.IsValid() {
		return false
	}
	ident, ok := ast.Unparen(call.Fun).(*ast.Ident)
	if !ok || ident.Name != "make" {
		return false
	}
	_, isChan := ast.Unparen(call.Args[0]).(*ast.ChanType)
	return isChan && isPositiveBoundArg(call.Args[1])
}

// isPositiveBoundArg reports whether expr is not a zero or negative integer literal.
func isPositiveBoundArg(expr ast.Expr) bool {
	if expr == nil {
		return false
	}
	expr = ast.Unparen(expr)
	if u, ok := expr.(*ast.UnaryExpr); ok && u.Op == token.SUB {
		return false
	}
	if lit, ok := expr.(*ast.BasicLit); ok && lit.Kind == token.INT {
		val, err := strconv.ParseUint(lit.Value, 0, 64)
		return err == nil && val > 0
	}
	return true
}

// hasGoStmtLimiter checks semaphore and token-channel bounds for a raw go statement across loops.
func (g *goScanner) hasGoStmtLimiter(loops []ast.Stmt, goStmt *ast.GoStmt, facts *fanoutFacts) bool {
	if g.hasSemaphoreAcquireInLoops(loops, goStmt, facts) {
		return true
	}
	return hasTokenChannelBound(loops, goStmt, facts)
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

// hasSemaphoreAcquireInLoops reports whether any loop in loops structurally dominates goStmt
// with a verified semaphore acquisition.
func (g *goScanner) hasSemaphoreAcquireInLoops(loops []ast.Stmt, goStmt *ast.GoStmt, facts *fanoutFacts) bool {
	for i := 0; i < len(loops); i++ {
		if g.loopDominatesWithSemaphore(loopBody(loops[i]), goStmt, facts) {
			return true
		}
	}
	return false
}

// loopDominatesWithSemaphore walks the ancestor chain on g.stack from body to goStmt and reports
// whether a successful semaphore acquisition dominates goStmt.
func (g *goScanner) loopDominatesWithSemaphore(body *ast.BlockStmt, goStmt *ast.GoStmt, facts *fanoutFacts) bool {
	start := g.stackNodeIndex(body)
	if start < 0 {
		return false
	}
	for idx := start; idx < len(g.stack); idx++ {
		nextNode := ast.Node(goStmt)
		if idx+1 < len(g.stack) {
			nextNode = g.stack[idx+1]
		}
		if g.stackFrameAcquires(g.stack[idx], nextNode, goStmt.Pos(), facts) {
			return true
		}
	}
	return false
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

// stackFrameAcquires checks one ancestor node on the path from the loop body to goStmt.
func (g *goScanner) stackFrameAcquires(frame, nextNode ast.Node, goPos token.Pos, facts *fanoutFacts) bool {
	switch node := frame.(type) {
	case *ast.BlockStmt:
		return g.stmtsAcquireBefore(node.List, goPos, facts)
	case *ast.CaseClause:
		return g.stmtsAcquireBefore(node.Body, goPos, facts)
	case *ast.CommClause:
		return g.stmtsAcquireBefore(node.Body, goPos, facts)
	case *ast.IfStmt:
		return nextNode == node.Body && ifBodyAcquiresSemaphore(node, facts)
	default:
		return false
	}
}

// stmtsAcquireBefore reports whether any statement preceding goPos in stmts dominates subsequent
// statements with a successful semaphore acquisition.
func (g *goScanner) stmtsAcquireBefore(stmts []ast.Stmt, goPos token.Pos, facts *fanoutFacts) bool {
	for i := 0; i < len(stmts); i++ {
		if stmts[i].End() > goPos {
			return false
		}
		if g.stmtDominatesWithAcquire(stmts[i], facts) {
			return true
		}
	}
	return false
}

// stmtDominatesWithAcquire reports whether stmt acquires a tracked semaphore on every path that
// falls through stmt to later statements in the same block.
func (g *goScanner) stmtDominatesWithAcquire(stmt ast.Stmt, facts *fanoutFacts) bool {
	if labeled, ok := stmt.(*ast.LabeledStmt); ok {
		stmt = labeled.Stmt
	}
	switch s := stmt.(type) {
	case *ast.ExprStmt:
		return isSemaphoreMethodCall(s.X, "Acquire", facts)
	case *ast.AssignStmt:
		return len(s.Lhs) == 1 && len(s.Rhs) == 1 && isSemaphoreMethodCall(s.Rhs[0], "Acquire", facts)
	case *ast.IfStmt:
		return g.ifGuardDominatesWithAcquire(s, facts)
	default:
		return false
	}
}

// ifGuardDominatesWithAcquire checks `if err := sem.Acquire(...); err != nil { <terminates> }`,
// `if sem.Acquire(...) != nil { <terminates> }`, and `if !sem.TryAcquire(n) { <terminates> }`.
func (g *goScanner) ifGuardDominatesWithAcquire(ifStmt *ast.IfStmt, facts *fanoutFacts) bool {
	if !g.blockTerminates(ifStmt.Body) {
		return false
	}
	if ifStmt.Init != nil {
		if errName, ok := assignSemaphoreCallIdent(ifStmt.Init, "Acquire", facts); ok {
			return isIdentNilComparison(ifStmt.Cond, errName, token.NEQ)
		}
		if okName, ok := assignSemaphoreCallIdent(ifStmt.Init, "TryAcquire", facts); ok {
			return isNegatedBoolIdent(ifStmt.Cond, okName)
		}
		return false
	}
	if isAcquireNilComparison(ifStmt.Cond, token.NEQ, facts) {
		return true
	}
	return isNegatedTryAcquire(ifStmt.Cond, facts)
}

// ifBodyAcquiresSemaphore checks whether entering ifStmt.Body proves a successful acquisition:
// `if sem.TryAcquire(n) { ... }` or `if err := sem.Acquire(...); err == nil { ... }`.
func ifBodyAcquiresSemaphore(ifStmt *ast.IfStmt, facts *fanoutFacts) bool {
	if ifStmt.Init != nil {
		if errName, ok := assignSemaphoreCallIdent(ifStmt.Init, "Acquire", facts); ok {
			return isIdentNilComparison(ifStmt.Cond, errName, token.EQL)
		}
		if okName, ok := assignSemaphoreCallIdent(ifStmt.Init, "TryAcquire", facts); ok {
			return isPositiveBoolIdent(ifStmt.Cond, okName)
		}
		return false
	}
	if isAcquireNilComparison(ifStmt.Cond, token.EQL, facts) {
		return true
	}
	return isPositiveTryAcquire(ifStmt.Cond, facts)
}

// assignSemaphoreCallIdent extracts the non-blank LHS variable bound to sem.<method>(...) in initStmt.
func assignSemaphoreCallIdent(initStmt ast.Stmt, method string, facts *fanoutFacts) (string, bool) {
	assign, ok := initStmt.(*ast.AssignStmt)
	if !ok || len(assign.Lhs) != 1 || len(assign.Rhs) != 1 {
		return "", false
	}
	ident, ok := assign.Lhs[0].(*ast.Ident)
	if !ok || ident.Name == "_" || !isSemaphoreMethodCall(assign.Rhs[0], method, facts) {
		return "", false
	}
	return ident.Name, true
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

// isAcquireNilComparison reports whether cond compares sem.Acquire(...) against nil with op.
func isAcquireNilComparison(cond ast.Expr, op token.Token, facts *fanoutFacts) bool {
	bin, ok := ast.Unparen(cond).(*ast.BinaryExpr)
	if !ok || bin.Op != op {
		return false
	}
	return (isSemaphoreMethodCall(bin.X, "Acquire", facts) && isNamedIdent(bin.Y, "nil")) ||
		(isNamedIdent(bin.X, "nil") && isSemaphoreMethodCall(bin.Y, "Acquire", facts))
}

// isNegatedTryAcquire reports whether cond is `!sem.TryAcquire(n)` or `sem.TryAcquire(n) == false`.
func isNegatedTryAcquire(cond ast.Expr, facts *fanoutFacts) bool {
	cond = ast.Unparen(cond)
	if u, ok := cond.(*ast.UnaryExpr); ok && u.Op == token.NOT {
		return isSemaphoreMethodCall(u.X, "TryAcquire", facts)
	}
	bin, ok := cond.(*ast.BinaryExpr)
	if !ok || bin.Op != token.EQL {
		return false
	}
	return (isSemaphoreMethodCall(bin.X, "TryAcquire", facts) && isNamedIdent(bin.Y, "false")) ||
		(isNamedIdent(bin.X, "false") && isSemaphoreMethodCall(bin.Y, "TryAcquire", facts))
}

// isPositiveTryAcquire reports whether cond is `sem.TryAcquire(n)` or `sem.TryAcquire(n) == true`.
func isPositiveTryAcquire(cond ast.Expr, facts *fanoutFacts) bool {
	cond = ast.Unparen(cond)
	if isSemaphoreMethodCall(cond, "TryAcquire", facts) {
		return true
	}
	bin, ok := cond.(*ast.BinaryExpr)
	if !ok || bin.Op != token.EQL {
		return false
	}
	return (isSemaphoreMethodCall(bin.X, "TryAcquire", facts) && isNamedIdent(bin.Y, "true")) ||
		(isNamedIdent(bin.X, "true") && isSemaphoreMethodCall(bin.Y, "TryAcquire", facts))
}

// isNegatedBoolIdent reports whether cond is `!name` or `name == false`.
func isNegatedBoolIdent(cond ast.Expr, name string) bool {
	cond = ast.Unparen(cond)
	if u, ok := cond.(*ast.UnaryExpr); ok && u.Op == token.NOT {
		return isNamedIdent(u.X, name)
	}
	bin, ok := cond.(*ast.BinaryExpr)
	if !ok || bin.Op != token.EQL {
		return false
	}
	return (isNamedIdent(bin.X, name) && isNamedIdent(bin.Y, "false")) ||
		(isNamedIdent(bin.X, "false") && isNamedIdent(bin.Y, name))
}

// isPositiveBoolIdent reports whether cond is `name` or `name == true`.
func isPositiveBoolIdent(cond ast.Expr, name string) bool {
	cond = ast.Unparen(cond)
	if isNamedIdent(cond, name) {
		return true
	}
	bin, ok := cond.(*ast.BinaryExpr)
	if !ok || bin.Op != token.EQL {
		return false
	}
	return (isNamedIdent(bin.X, name) && isNamedIdent(bin.Y, "true")) ||
		(isNamedIdent(bin.X, "true") && isNamedIdent(bin.Y, name))
}

// isNamedIdent reports whether expr unwraps to an *ast.Ident with Name == want.
func isNamedIdent(expr ast.Expr, want string) bool {
	ident, ok := ast.Unparen(expr).(*ast.Ident)
	return ok && ident.Name == want
}

// isSemaphoreMethodCall reports whether expr calls method ("Acquire" or "TryAcquire") with a
// positive weight on an unshadowed semaphore.Weighted declared outside the enclosing loops.
func isSemaphoreMethodCall(expr ast.Expr, method string, facts *fanoutFacts) bool {
	call, ok := ast.Unparen(expr).(*ast.CallExpr)
	if !ok || call.Ellipsis.IsValid() {
		return false
	}
	if !validSemaphoreArgs(method, call.Args) {
		return false
	}
	sel, ok := ast.Unparen(call.Fun).(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != method {
		return false
	}
	recv, ok := receiverBaseIdent(sel.X)
	return ok && facts.isSemaphore(recv)
}

// validSemaphoreArgs checks the argument count and positive weight for Acquire and TryAcquire.
func validSemaphoreArgs(method string, args []ast.Expr) bool {
	switch method {
	case "Acquire":
		return len(args) == 2 && isPositiveBoundArg(args[1])
	case "TryAcquire":
		return len(args) == 1 && isPositiveBoundArg(args[0])
	default:
		return false
	}
}

// maxTermDepth bounds the iterative branch-termination proof stack (HISS-02).
const maxTermDepth = 16

// blockTerminates reports whether block unconditionally exits the current loop iteration, loop,
// or function via return, break, continue, panic, os.Exit or a testing/log Fatal call. Iterative (HISS-01).
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

// isLeafTerminator reports whether stmt directly terminates control flow (return, break,
// continue, panic, os.Exit, or *.Fatal/Fatalf/Fatalln/FailNow).
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

// isTerminatingCall recognizes panic, os.Exit, and receiver Fatal/Fatalf/Fatalln/FailNow calls.
func (g *goScanner) isTerminatingCall(expr ast.Expr) bool {
	call, ok := ast.Unparen(expr).(*ast.CallExpr)
	if !ok {
		return false
	}
	if ident, ok := ast.Unparen(call.Fun).(*ast.Ident); ok {
		return ident.Name == "panic" && len(call.Args) == 1
	}
	if g.matchesPkgSelector(call.Fun, "os", "os", "Exit") {
		return len(call.Args) == 1
	}
	sel, ok := ast.Unparen(call.Fun).(*ast.SelectorExpr)
	if !ok {
		return false
	}
	if _, ok := receiverBaseIdent(sel.X); !ok {
		return false
	}
	switch sel.Sel.Name {
	case "FailNow":
		return len(call.Args) == 0
	case "Fatal", "Fatalf", "Fatalln":
		return len(call.Args) >= 1
	default:
		return false
	}
}

// hasTokenChannelBound reports whether a buffered token channel declared outside the enclosing
// loops is sent to before goStmt and received from in the goroutine (deferred or at body end).
func hasTokenChannelBound(loops []ast.Stmt, goStmt *ast.GoStmt, facts *fanoutFacts) bool {
	lit, ok := ast.Unparen(goStmt.Call.Fun).(*ast.FuncLit)
	if !ok || lit.Body == nil || len(lit.Body.List) == 0 {
		return false
	}
	chans := tokenSendsBeforeGo(loops, goStmt.Pos(), facts)
	for i := 0; i < len(chans); i++ {
		if goroutineReceivesToken(lit.Body, chans[i]) {
			return true
		}
	}
	return false
}

// tokenSendsBeforeGo returns the names of buffered token channels sent to inside loops before
// goPos.
func tokenSendsBeforeGo(loops []ast.Stmt, goPos token.Pos, facts *fanoutFacts) []string {
	var sent []string
	for i := 0; i < len(loops); i++ {
		sent = appendLoopTokenSends(sent, loopBody(loops[i]), goPos, facts)
	}
	return sent
}

// appendLoopTokenSends collects buffered channel sends before goPos in one loop body.
func appendLoopTokenSends(sent []string, body *ast.BlockStmt, goPos token.Pos, facts *fanoutFacts) []string {
	if body == nil {
		return sent
	}
	steps := 0
	ast.Inspect(body, func(n ast.Node) bool {
		if n == nil || steps >= maxNodeStack {
			return false
		}
		steps++
		if _, isGo := n.(*ast.GoStmt); isGo {
			return false
		}
		send, ok := n.(*ast.SendStmt)
		if !ok || send.Pos() >= goPos {
			return true
		}
		if ident, ok := ast.Unparen(send.Chan).(*ast.Ident); ok && facts.isTokenChan(ident.Name) {
			sent = append(sent, ident.Name)
		}
		return true
	})
	return sent
}

// goroutineReceivesToken reports whether the goroutine body receives from ch in a defer
// statement or in its final statement.
func goroutineReceivesToken(body *ast.BlockStmt, ch string) bool {
	for i := 0; i < len(body.List); i++ {
		if def, ok := body.List[i].(*ast.DeferStmt); ok && nodeReceivesFromChan(def.Call, ch) {
			return true
		}
	}
	last := body.List[len(body.List)-1]
	return nodeReceivesFromChan(last, ch)
}

// nodeReceivesFromChan reports whether root contains a channel receive <-ch.
func nodeReceivesFromChan(root ast.Node, ch string) bool {
	if root == nil {
		return false
	}
	found := false
	steps := 0
	ast.Inspect(root, func(n ast.Node) bool {
		if n == nil || found || steps >= maxNodeStack {
			return false
		}
		steps++
		u, ok := n.(*ast.UnaryExpr)
		if !ok || u.Op != token.ARROW {
			return true
		}
		if ident, ok := ast.Unparen(u.X).(*ast.Ident); ok && ident.Name == ch {
			found = true
			return false
		}
		return true
	})
	return found
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
		if isConstCounterLoop(l, facts) {
			return true
		}
	}
	target, ok := g.loopHissBound(loop, facts)
	return ok && isHissBoundLoop(loop, target, facts)
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

// isConstCounterLoop reports whether loop is a counter loop bounded by a constant expression with
// a nonzero constant step advancing toward that bound.
func isConstCounterLoop(loop *ast.ForStmt, facts *fanoutFacts) bool {
	counter, initExpr, ok := counterInit(loop.Init, facts)
	if !ok {
		return false
	}
	step, ok := counterStep(loop.Post, counter, facts)
	if !ok {
		return false
	}
	conjuncts := condConjuncts(loop.Cond)
	for i := 0; i < len(conjuncts); i++ {
		bound, op, ok := counterCondBound(conjuncts[i], counter)
		if ok && isConstBoundExpr(bound, facts) && stepApproachesBound(op, step, initExpr, bound) {
			return true
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
func counterInit(initStmt ast.Stmt, facts *fanoutFacts) (string, ast.Expr, bool) {
	assign, ok := initStmt.(*ast.AssignStmt)
	if !ok || len(assign.Lhs) != 1 || len(assign.Rhs) != 1 {
		return "", nil, false
	}
	ident, ok := assign.Lhs[0].(*ast.Ident)
	if !ok || ident.Name == "_" || !isConstBoundExpr(assign.Rhs[0], facts) {
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
// constant.
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
// left). != accepts only ++ or -- (unit step) and rejects literal bounds in the opposite direction.
func stepApproachesBound(op token.Token, step counterStepInfo, initExpr, boundExpr ast.Expr) bool {
	switch op {
	case token.LSS, token.LEQ:
		return step.dir > 0
	case token.GTR, token.GEQ:
		return step.dir < 0
	case token.NEQ:
		if !step.unit {
			return false
		}
		initVal, okInit := signedIntLiteral(initExpr)
		boundVal, okBound := signedIntLiteral(boundExpr)
		if !okInit || !okBound || initVal == boundVal {
			return true
		}
		if initVal < boundVal {
			return step.dir > 0
		}
		return step.dir < 0
	default:
		return false
	}
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
// const, len of array/string literal, or arithmetic combination of constants).
func isConstBoundExpr(expr ast.Expr, facts *fanoutFacts) bool {
	if expr == nil {
		return false
	}
	valid, hasLeaf, steps := true, false, 0
	ast.Inspect(expr, func(n ast.Node) bool {
		if n == nil || !valid {
			return false
		}
		steps++
		if steps > maxNodeStack {
			valid = false
			return false
		}
		descend, leaf, ok := classifyConstNode(n, facts)
		valid = ok
		hasLeaf = hasLeaf || leaf
		return descend && ok
	})
	return valid && hasLeaf
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
	if len(call.Args) != 1 || call.Ellipsis.IsValid() {
		return false
	}
	ident, ok := ast.Unparen(call.Fun).(*ast.Ident)
	if !ok || ident.Name != "len" {
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
func isHissBoundLoop(loop ast.Stmt, target string, facts *fanoutFacts) bool {
	switch l := loop.(type) {
	case *ast.ForStmt:
		return isConstCounterLoop(l, facts) || isTargetCounterLoop(l, target, facts)
	case *ast.RangeStmt:
		return matchesHissBoundTarget(l.X, target, facts)
	default:
		return false
	}
}

// isTargetCounterLoop reports whether loop advances a constant-initialized counter toward target.
func isTargetCounterLoop(loop *ast.ForStmt, target string, facts *fanoutFacts) bool {
	counter, initExpr, ok := counterInit(loop.Init, facts)
	if !ok {
		return false
	}
	step, ok := counterStep(loop.Post, counter, facts)
	if !ok {
		return false
	}
	bound, op, ok := counterCondBound(loop.Cond, counter)
	return ok && matchesHissBoundTarget(bound, target, facts) && stepApproachesBound(op, step, initExpr, bound)
}

// matchesHissBoundTarget reports whether expr is a constant or the explicit int variable named
// in target.
func matchesHissBoundTarget(expr ast.Expr, target string, facts *fanoutFacts) bool {
	if isConstBoundExpr(expr, facts) {
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
