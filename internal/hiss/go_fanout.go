// SPDX-FileCopyrightText: 2026 Lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package hiss

import (
	"fmt"
	"go/ast"
	"go/token"
	"strings"
)

// fanoutFacts holds the per-file and per-function declarations used to decide whether a go
// statement inside a loop body carries a visible scalar concurrency bound (HISS-06).
type fanoutFacts struct {
	consts     map[string]bool
	arrays     map[string]bool
	ints       map[string]bool
	errgroups  map[string]bool
	semaphores map[string]bool
	tokenChans map[string]bool
	hissBounds map[int]string
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

// checkGoFanout reports a go statement inside a for or range body when the enclosing function
// shows none of the five HISS-06 concurrency bounds.
func (g *goScanner) checkGoFanout(goStmt *ast.GoStmt) {
	loops, fnRoot := g.enclosingFanoutLoops()
	if len(loops) == 0 || fnRoot == nil {
		return
	}
	facts := g.collectFanoutFacts(fnRoot)
	if g.hasGoStmtLimiter(fnRoot, loops, goStmt, &facts) {
		return
	}
	for i := 0; i < len(loops); i++ {
		if !g.isLoopBounded(loops[i], &facts) {
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
		"Function %s (line %d) launches a goroutine per loop iteration without a visible scalar bound; bound concurrency with errgroup.Group.SetLimit, semaphore.Weighted.Acquire, a buffered token channel, a constant/array loop bound, or //hiss:bound",
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
func (g *goScanner) collectFanoutFacts(fnRoot ast.Node) fanoutFacts {
	facts := fanoutFacts{
		consts:     make(map[string]bool),
		arrays:     make(map[string]bool),
		ints:       make(map[string]bool),
		errgroups:  make(map[string]bool),
		semaphores: make(map[string]bool),
		tokenChans: make(map[string]bool),
		hissBounds: hissBoundComments(g.fset, g.file),
	}
	g.collectFileFanoutFacts(&facts)
	g.collectFuncFanoutFacts(fnRoot, &facts)
	return facts
}

// collectFileFanoutFacts records package-level constants and array variables declared in the
// file.
func (g *goScanner) collectFileFanoutFacts(facts *fanoutFacts) {
	if g.file == nil {
		return
	}
	for i := 0; i < len(g.file.Decls); i++ {
		if gen, ok := g.file.Decls[i].(*ast.GenDecl); ok {
			g.recordGenDeclFacts(gen, facts)
		}
	}
}

// recordGenDeclFacts records const and var specifications from one declaration group.
func (g *goScanner) recordGenDeclFacts(gen *ast.GenDecl, facts *fanoutFacts) {
	for i := 0; i < len(gen.Specs); i++ {
		spec, ok := gen.Specs[i].(*ast.ValueSpec)
		if !ok {
			continue
		}
		if gen.Tok == token.CONST {
			markSpecIdents(spec.Names, facts.consts)
			continue
		}
		if gen.Tok == token.VAR {
			g.recordValueSpecFacts(spec, facts)
		}
	}
}

// recordValueSpecFacts classifies a var ValueSpec into arrays, errgroups, semaphores, ints or
// buffered token channels.
func (g *goScanner) recordValueSpecFacts(spec *ast.ValueSpec, facts *fanoutFacts) {
	unmarkSpecIdents(spec.Names, facts.consts)
	if isArrayValueSpec(spec) {
		markSpecIdents(spec.Names, facts.arrays)
	}
	if g.isErrgroupSpec(spec) {
		markSpecIdents(spec.Names, facts.errgroups)
	}
	if g.isSemaphoreSpec(spec) {
		markSpecIdents(spec.Names, facts.semaphores)
	}
	if isIntValueSpec(spec) {
		markSpecIdents(spec.Names, facts.ints)
	}
	if isBufferedChanSpec(spec) {
		markSpecIdents(spec.Names, facts.tokenChans)
	}
}

// markSpecIdents sets target[name] = true for each non-blank identifier in names.
func markSpecIdents(names []*ast.Ident, target map[string]bool) {
	for i := 0; i < len(names); i++ {
		if names[i] != nil && names[i].Name != "_" {
			target[names[i].Name] = true
		}
	}
}

// unmarkSpecIdents removes shadowed names from target.
func unmarkSpecIdents(names []*ast.Ident, target map[string]bool) {
	for i := 0; i < len(names); i++ {
		if names[i] != nil {
			delete(target, names[i].Name)
		}
	}
}

// collectFuncFanoutFacts walks the enclosing function to record parameters and local bindings.
func (g *goScanner) collectFuncFanoutFacts(fnRoot ast.Node, facts *fanoutFacts) {
	g.recordFuncRootParams(fnRoot, facts)
	steps := 0
	ast.Inspect(fnRoot, func(n ast.Node) bool {
		if n == nil || steps >= maxNodeStack {
			return false
		}
		steps++
		switch node := n.(type) {
		case *ast.GenDecl:
			g.recordGenDeclFacts(node, facts)
		case *ast.AssignStmt:
			g.recordAssignFacts(node, facts)
		case *ast.FuncLit:
			if node.Type != nil {
				g.recordFieldListFacts(node.Type.Params, facts)
			}
		}
		return true
	})
}

// recordFuncRootParams records receiver and parameter types of the enclosing function root.
func (g *goScanner) recordFuncRootParams(fnRoot ast.Node, facts *fanoutFacts) {
	switch fn := fnRoot.(type) {
	case *ast.FuncDecl:
		g.recordFieldListFacts(fn.Recv, facts)
		if fn.Type != nil {
			g.recordFieldListFacts(fn.Type.Params, facts)
		}
	case *ast.FuncLit:
		if fn.Type != nil {
			g.recordFieldListFacts(fn.Type.Params, facts)
		}
	}
}

// recordFieldListFacts classifies each field in a receiver or parameter list.
func (g *goScanner) recordFieldListFacts(list *ast.FieldList, facts *fanoutFacts) {
	if list == nil {
		return
	}
	for i := 0; i < len(list.List); i++ {
		field := list.List[i]
		unmarkSpecIdents(field.Names, facts.consts)
		g.classifyFieldType(field.Type, field.Names, facts)
	}
}

// classifyFieldType records the declared type category of a parameter or receiver field.
func (g *goScanner) classifyFieldType(expr ast.Expr, names []*ast.Ident, facts *fanoutFacts) {
	switch {
	case isFixedArrayOrPtrType(expr):
		markSpecIdents(names, facts.arrays)
	case g.isErrgroupType(expr):
		markSpecIdents(names, facts.errgroups)
	case g.isSemaphoreType(expr):
		markSpecIdents(names, facts.semaphores)
	case isIntTypeExpr(expr):
		markSpecIdents(names, facts.ints)
	}
}

// recordErrgroupAssignment marks the LHS identifier as an errgroup if the assignment is errgroup.WithContext.
func (g *goScanner) recordErrgroupAssignment(assign *ast.AssignStmt, facts *fanoutFacts) {
	if len(assign.Rhs) != 1 || !g.isErrgroupWithContextCall(assign.Rhs[0]) {
		return
	}
	if ident, ok := assign.Lhs[0].(*ast.Ident); ok && ident.Name != "_" {
		delete(facts.consts, ident.Name)
		facts.errgroups[ident.Name] = true
	}
}

// recordAssignFacts classifies short variable declarations and assignments inside a function.
func (g *goScanner) recordAssignFacts(assign *ast.AssignStmt, facts *fanoutFacts) {
	if len(assign.Lhs) == 0 || len(assign.Rhs) == 0 {
		return
	}
	g.recordErrgroupAssignment(assign, facts)
	isDefine := assign.Tok == token.DEFINE
	limit := min(len(assign.Lhs), len(assign.Rhs))
	for i := 0; i < limit; i++ {
		ident, ok := assign.Lhs[i].(*ast.Ident)
		if !ok || ident.Name == "_" {
			continue
		}
		if isDefine {
			delete(facts.consts, ident.Name)
		}
		g.classifyAssignedValue(ident.Name, assign.Rhs[i], isDefine, facts)
	}
}

// classifyAssignedValue records the RHS category assigned to name.
func (g *goScanner) classifyAssignedValue(name string, rhs ast.Expr, defined bool, facts *fanoutFacts) {
	switch {
	case isFixedArrayValue(rhs):
		facts.arrays[name] = true
	case g.isErrgroupValue(rhs):
		facts.errgroups[name] = true
	case g.isSemaphoreValue(rhs):
		facts.semaphores[name] = true
	case isBufferedChanMake(rhs):
		facts.tokenChans[name] = true
	case defined && isIntCandidateValue(rhs):
		facts.ints[name] = true
	}
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
		return lit.Value != "0"
	}
	return true
}

// hasGoStmtLimiter checks bounds (a) errgroup SetLimit, (b) semaphore Acquire, and (c) buffered
// token channel send/receive for goStmt.
func (g *goScanner) hasGoStmtLimiter(fnRoot ast.Node, loops []ast.Stmt, goStmt *ast.GoStmt, facts *fanoutFacts) bool {
	outer := loops[len(loops)-1]
	if hasErrgroupSetLimit(fnRoot, outer.Pos(), facts) {
		return true
	}
	if hasSemaphoreAcquireInLoops(loops, goStmt.Pos(), facts) {
		return true
	}
	return hasTokenChannelBound(loops, goStmt, facts)
}

// hasErrgroupSetLimit reports whether an errgroup.Group in fnRoot calls SetLimit before loopPos.
func hasErrgroupSetLimit(fnRoot ast.Node, loopPos token.Pos, facts *fanoutFacts) bool {
	found := false
	steps := 0
	ast.Inspect(fnRoot, func(n ast.Node) bool {
		if n == nil || found || steps >= maxNodeStack {
			return false
		}
		steps++
		call, ok := n.(*ast.CallExpr)
		if ok && call.Pos() < loopPos && isErrgroupSetLimitCall(call, facts) {
			found = true
			return false
		}
		return true
	})
	return found
}

// isErrgroupSetLimitCall reports whether call invokes SetLimit with a positive bound on a known
// errgroup.Group variable.
func isErrgroupSetLimitCall(call *ast.CallExpr, facts *fanoutFacts) bool {
	if len(call.Args) != 1 || call.Ellipsis.IsValid() {
		return false
	}
	sel, ok := ast.Unparen(call.Fun).(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != "SetLimit" {
		return false
	}
	recv, ok := receiverBaseIdent(sel.X)
	return ok && facts.errgroups[recv] && isPositiveBoundArg(call.Args[0])
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

// hasSemaphoreAcquireInLoops reports whether any enclosing loop body calls Acquire or
// TryAcquire on a semaphore.Weighted before goPos.
func hasSemaphoreAcquireInLoops(loops []ast.Stmt, goPos token.Pos, facts *fanoutFacts) bool {
	for i := 0; i < len(loops); i++ {
		if bodyHasSemaphoreAcquire(loopBody(loops[i]), goPos, facts) {
			return true
		}
	}
	return false
}

// bodyHasSemaphoreAcquire walks one loop body (excluding nested go statements) for a semaphore
// Acquire call before goPos.
func bodyHasSemaphoreAcquire(body *ast.BlockStmt, goPos token.Pos, facts *fanoutFacts) bool {
	if body == nil {
		return false
	}
	found := false
	steps := 0
	ast.Inspect(body, func(n ast.Node) bool {
		if n == nil || found || steps >= maxNodeStack {
			return false
		}
		steps++
		if _, isGo := n.(*ast.GoStmt); isGo {
			return false
		}
		if call, ok := n.(*ast.CallExpr); ok && call.Pos() < goPos && isSemaphoreAcquireCall(call, facts) {
			found = true
			return false
		}
		return true
	})
	return found
}

// isSemaphoreAcquireCall reports whether call invokes Acquire or TryAcquire on a tracked
// semaphore.Weighted variable.
func isSemaphoreAcquireCall(call *ast.CallExpr, facts *fanoutFacts) bool {
	if len(call.Args) == 0 {
		return false
	}
	sel, ok := ast.Unparen(call.Fun).(*ast.SelectorExpr)
	if !ok || (sel.Sel.Name != "Acquire" && sel.Sel.Name != "TryAcquire") {
		return false
	}
	recv, ok := receiverBaseIdent(sel.X)
	return ok && facts.semaphores[recv]
}

// hasTokenChannelBound reports whether a buffered token channel is sent to in the loop body
// before goStmt and received from in the goroutine (deferred or at the end of its body).
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
		if ident, ok := ast.Unparen(send.Chan).(*ast.Ident); ok && facts.tokenChans[ident.Name] {
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

// isArrayRangeExpr reports whether a range expression has a fixed array type.
func isArrayRangeExpr(expr ast.Expr, facts *fanoutFacts) bool {
	if isFixedArrayValue(expr) {
		return true
	}
	if call, ok := ast.Unparen(expr).(*ast.CallExpr); ok && isFixedArrayOrPtrType(call.Fun) {
		return true
	}
	name, ok := receiverBaseIdent(expr)
	return ok && facts.arrays[name]
}

// isConstCounterLoop reports whether loop is a counter loop bounded by a constant expression.
func isConstCounterLoop(loop *ast.ForStmt, facts *fanoutFacts) bool {
	counter, ok := counterInitVar(loop.Init, facts)
	if !ok || !counterUpdatedInPost(loop.Post, counter) {
		return false
	}
	conjuncts := condConjuncts(loop.Cond)
	for i := 0; i < len(conjuncts); i++ {
		if bound, ok := counterCondBound(conjuncts[i], counter); ok && isConstBoundExpr(bound, facts) {
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

// counterInitVar returns the loop variable initialized in initStmt when its initial value is a
// constant expression.
func counterInitVar(initStmt ast.Stmt, facts *fanoutFacts) (string, bool) {
	assign, ok := initStmt.(*ast.AssignStmt)
	if !ok || len(assign.Lhs) != 1 || len(assign.Rhs) != 1 {
		return "", false
	}
	ident, ok := assign.Lhs[0].(*ast.Ident)
	if !ok || ident.Name == "_" || !isConstBoundExpr(assign.Rhs[0], facts) {
		return "", false
	}
	return ident.Name, true
}

// counterCondBound returns the bound expression compared against counter in cond.
func counterCondBound(cond ast.Expr, counter string) (ast.Expr, bool) {
	bin, ok := ast.Unparen(cond).(*ast.BinaryExpr)
	if !ok || !isCounterComparisonOp(bin.Op) {
		return nil, false
	}
	if left, ok := ast.Unparen(bin.X).(*ast.Ident); ok && left.Name == counter {
		return bin.Y, true
	}
	if right, ok := ast.Unparen(bin.Y).(*ast.Ident); ok && right.Name == counter {
		return bin.X, true
	}
	return nil, false
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

// counterUpdatedInPost reports whether postStmt increments or decrements counter.
func counterUpdatedInPost(postStmt ast.Stmt, counter string) bool {
	switch post := postStmt.(type) {
	case *ast.IncDecStmt:
		ident, ok := ast.Unparen(post.X).(*ast.Ident)
		return ok && ident.Name == counter
	case *ast.AssignStmt:
		if len(post.Lhs) != 1 || (post.Tok != token.ADD_ASSIGN && post.Tok != token.SUB_ASSIGN) {
			return false
		}
		ident, ok := ast.Unparen(post.Lhs[0]).(*ast.Ident)
		return ok && ident.Name == counter
	default:
		return false
	}
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
		if isConstCounterLoop(l, facts) {
			return true
		}
		counter, ok := counterInitVar(l.Init, facts)
		if !ok || !counterUpdatedInPost(l.Post, counter) {
			return false
		}
		bound, ok := counterCondBound(l.Cond, counter)
		return ok && matchesHissBoundTarget(bound, target, facts)
	case *ast.RangeStmt:
		return matchesHissBoundTarget(l.X, target, facts)
	default:
		return false
	}
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
