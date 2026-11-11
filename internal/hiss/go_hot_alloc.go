// SPDX-FileCopyrightText: 2026 Lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package hiss

import (
	"context"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/cordanaLLM/praetor/internal/contextopt"
	"github.com/cordanaLLM/praetor/internal/util"
)

const (
	hotAllocRuleID   = "HISS-03"
	hotMarkerTag     = "hiss:hot"
	maxHotAllocFiles = 20000
	maxHotAllocDecls = 4000
	maxDocLines      = 128
	maxDocComments   = 4000
	maxHotAllocStmts = 10000
)

type hotPathFunction struct {
	file   string
	symbol string
	name   string
	line   int
	isBare bool
}

// checkHotAllocations checks marked hot paths across directories so missing allocation tests are reported.
func checkHotAllocations(ctx context.Context, rep *ScanReport, root string, paths []string) error {
	byDir, dirs := groupFilesByDir(paths)
	for i := 0; i < len(dirs) && i < maxHotAllocFiles; i++ {
		if err := ctx.Err(); err != nil {
			rep.Truncated = true
			return err
		}
		if err := checkDirHotAlloc(ctx, rep, root, byDir[dirs[i]]); err != nil {
			if ctx.Err() != nil {
				rep.Truncated = true
			}
			return err
		}
	}
	return nil
}

// groupFilesByDir groups source paths by directory and sorts the groups for deterministic scanning.
func groupFilesByDir(paths []string) (map[string][]string, []string) {
	byDir := make(map[string][]string)
	for i := 0; i < len(paths) && i < maxHotAllocFiles; i++ {
		dir := filepath.Dir(paths[i])
		byDir[dir] = append(byDir[dir], paths[i])
	}
	dirs := make([]string, 0, len(byDir))
	for dir := range byDir {
		dirs = append(dirs, dir)
	}
	sort.Strings(dirs)
	return byDir, dirs
}

// checkDirHotAlloc matches marked production functions against zero-allocation tests in their directory.
func checkDirHotAlloc(ctx context.Context, rep *ScanReport, root string, files []string) error {
	var prodFiles, testFiles []string
	for i := 0; i < len(files) && i < maxHotAllocFiles; i++ {
		if strings.HasSuffix(files[i], "_test.go") {
			testFiles = append(testFiles, files[i])
		} else {
			prodFiles = append(prodFiles, files[i])
		}
	}
	fset := token.NewFileSet()
	targets, err := collectHotTargets(ctx, fset, root, prodFiles, rep)
	if err != nil {
		return err
	}
	if len(targets) == 0 {
		return nil
	}
	tests, err := findZeroAllocTests(ctx, fset, root, testFiles)
	if err != nil {
		return err
	}
	reportMissingHotTests(rep, targets, tests)
	return nil
}

// reportMissingHotTests reports bare markers and marked functions without a matching zero-allocation test.
func reportMissingHotTests(rep *ScanReport, targets []hotPathFunction, tests []string) {
	testMap := make(map[string]bool, len(tests))
	for i := 0; i < len(tests) && i < maxHotAllocFiles; i++ {
		testMap[tests[i]] = true
	}
	for i := 0; i < len(targets) && i < maxHotAllocFiles; i++ {
		target := targets[i]
		if target.isBare {
			recordViolation(rep, hotAllocRuleID, target.file, target.line, target.symbol,
				fmt.Sprintf("//hiss:hot marker on %s requires a reason explaining why the function is on the hot path", target.symbol))
			continue
		}
		if !testMap[target.name] {
			msg := fmt.Sprintf("//hiss:hot function %s has no testing.AllocsPerRun test asserting zero allocations; add a test calling testing.AllocsPerRun(n, f) and checking if allocs != 0 { t.Fatalf(...) }", target.symbol)
			recordViolation(rep, hotAllocRuleID, target.file, target.line, target.symbol, msg)
		}
	}
}

// parseHotFile reads filePath under ctx and MaxScanFileSize and parses its Go AST.
func parseHotFile(ctx context.Context, fset *token.FileSet, root, filePath string) (*ast.File, string, error) {
	rel, relErr := filepath.Rel(root, filePath)
	if relErr != nil {
		return nil, "", fmt.Errorf("relativize %s: %w", filePath, relErr)
	}
	fsRoot := root
	fsName := filepath.ToSlash(rel)
	if rel == "." {
		fsRoot = filepath.Dir(filePath)
		fsName = filepath.Base(filePath)
	} else if _, err := util.ConfinePath(root, rel); err != nil {
		return nil, "", fmt.Errorf("confine %s: %w", filePath, err)
	}
	data, err := contextopt.ReadFS(ctx, os.DirFS(fsRoot), fsName, MaxScanFileSize)
	if err != nil {
		return nil, "", err
	}
	file, err := parser.ParseFile(fset, filePath, data, parser.ParseComments|parser.SkipObjectResolution)
	if err != nil {
		return nil, "", err
	}
	return file, filepath.ToSlash(rel), nil
}

// collectHotTargets parses production files under ctx and collects marked functions for allocation checks.
func collectHotTargets(ctx context.Context, fset *token.FileSet, root string, prodFiles []string, rep *ScanReport) ([]hotPathFunction, error) {
	var targets []hotPathFunction
	for i := 0; i < len(prodFiles) && i < maxHotAllocFiles; i++ {
		file, rel, err := parseHotFile(ctx, fset, root, prodFiles[i])
		if err != nil {
			return nil, fmt.Errorf("parse %s: %w", prodFiles[i], err)
		}
		targets = append(targets, collectFileHotFunctions(fset, file, rel, rep)...)
	}
	return targets, nil
}

// collectFileHotFunctions collects marked declarations and reports markers attached to other syntax.
func collectFileHotFunctions(fset *token.FileSet, file *ast.File, rel string, rep *ScanReport) []hotPathFunction {
	funcDocs := make(map[*ast.CommentGroup]bool)
	var targets []hotPathFunction
	for i := 0; i < len(file.Decls) && i < maxHotAllocDecls; i++ {
		fn, ok := file.Decls[i].(*ast.FuncDecl)
		if !ok || fn.Doc == nil {
			continue
		}
		funcDocs[fn.Doc] = true
		found, isBare := parseDocCommentHot(fn.Doc)
		if !found {
			continue
		}
		name, symbol := funcDeclSymbol(fn)
		if fn.Recv == nil {
			name = "func:" + name
		} else {
			// Methods with the same name on different receiver types remain ambiguous.
			name = "method:" + name
		}
		targets = append(targets, hotPathFunction{
			file:   rel,
			symbol: symbol,
			name:   name,
			line:   fset.Position(fn.Pos()).Line,
			isBare: isBare,
		})
	}
	checkNonFunctionMarkers(fset, file, rel, funcDocs, rep)
	return targets
}

// checkNonFunctionMarkers reports hot markers outside function documentation because they cannot identify a target.
func checkNonFunctionMarkers(fset *token.FileSet, file *ast.File, rel string, funcDocs map[*ast.CommentGroup]bool, rep *ScanReport) {
	for i := 0; i < len(file.Comments) && i < maxDocComments; i++ {
		cg := file.Comments[i]
		if funcDocs[cg] {
			continue
		}
		for j := 0; j < len(cg.List) && j < maxDocLines; j++ {
			lines := strings.Split(cg.List[j].Text, "\n")
			for k := 0; k < len(lines) && k < maxDocLines; k++ {
				if matched, _, _ := parseHotCommentLine(lines[k]); matched {
					line := fset.Position(cg.List[j].Pos()).Line + k
					recordViolation(rep, hotAllocRuleID, rel, line, "", "//hiss:hot marker is only valid on function or method declarations; ignored on non-function")
				}
			}
		}
	}
}

// parseDocCommentHot finds a hot marker in a documentation group and identifies a missing reason.
func parseDocCommentHot(cg *ast.CommentGroup) (bool, bool) {
	if cg == nil {
		return false, false
	}
	for i := 0; i < len(cg.List) && i < maxDocLines; i++ {
		lines := strings.Split(cg.List[i].Text, "\n")
		for j := 0; j < len(lines) && j < maxDocLines; j++ {
			if matched, _, bare := parseHotCommentLine(lines[j]); matched {
				return true, bare
			}
		}
	}
	return false, false
}

// parseHotCommentLine recognizes a hot marker and extracts its reason without accepting a longer tag.
func parseHotCommentLine(line string) (bool, string, bool) {
	trimmed := strings.TrimSpace(line)
	trimmed = strings.TrimPrefix(trimmed, "//")
	trimmed = strings.TrimPrefix(trimmed, "/*")
	trimmed = strings.TrimPrefix(trimmed, "*")
	trimmed = strings.TrimSuffix(trimmed, "*/")
	trimmed = strings.TrimSpace(trimmed)
	if !strings.HasPrefix(trimmed, hotMarkerTag) {
		return false, "", false
	}
	rest := strings.TrimPrefix(trimmed, hotMarkerTag)
	if len(rest) > 0 && rest[0] != ' ' && rest[0] != '\t' && rest[0] != ':' {
		return false, "", false
	}
	rest = strings.TrimPrefix(rest, ":")
	rest = strings.TrimSpace(rest)
	if rest == "" {
		return true, "", true
	}
	return true, rest, false
}

// funcDeclSymbol derives a function name and receiver-qualified symbol for findings.
func funcDeclSymbol(fn *ast.FuncDecl) (string, string) {
	name := fn.Name.Name
	if fn.Recv == nil || len(fn.Recv.List) == 0 {
		return name, name
	}
	recv := fn.Recv.List[0].Type
	if star, ok := recv.(*ast.StarExpr); ok {
		if id, ok := star.X.(*ast.Ident); ok {
			return name, "(*" + id.Name + ")." + name
		}
	}
	if id, ok := recv.(*ast.Ident); ok {
		return name, id.Name + "." + name
	}
	return name, name
}

// findZeroAllocTests collects functions covered by zero-allocation assertions in test files.
func findZeroAllocTests(ctx context.Context, fset *token.FileSet, root string, testFiles []string) ([]string, error) {
	var results []string
	for i := 0; i < len(testFiles) && i < maxHotAllocFiles; i++ {
		file, _, err := parseHotFile(ctx, fset, root, testFiles[i])
		if err != nil {
			return nil, fmt.Errorf("parse test %s: %w", testFiles[i], err)
		}
		results = append(results, findZeroAllocTestsInFile(file)...)
	}
	return results, nil
}

// hotTestContext carries file import bindings and the enclosing function's test parameter.
type hotTestContext struct {
	imports   GoImports
	testParam string
}

// firstParamName returns the first non-blank identifier in names, or "" when none is bound.
func firstParamName(names []*ast.Ident) string {
	for i := 0; i < len(names) && i < maxHotAllocDecls; i++ {
		if names[i] != nil && names[i].Name != "_" {
			return names[i].Name
		}
	}
	return ""
}

// hotTestingParam finds the first named testing parameter accepted by allocation assertions.
func hotTestingParam(fn *ast.FuncDecl, imports GoImports) string {
	if fn.Type.Params == nil {
		return ""
	}
	for i := 0; i < len(fn.Type.Params.List) && i < maxHotAllocDecls; i++ {
		field := fn.Type.Params.List[i]
		if name := firstParamName(field.Names); name != "" && isHotTestingType(field.Type, imports) {
			return name
		}
	}
	return ""
}

// isHotTestingType recognizes *T, *B and TB qualified by a local import name bound to "testing".
func isHotTestingType(expr ast.Expr, imports GoImports) bool {
	pointer := false
	if star, ok := expr.(*ast.StarExpr); ok {
		expr = star.X
		pointer = true
	}
	sel, ok := expr.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	pkg, ok := sel.X.(*ast.Ident)
	if !ok || !imports.Binds(pkg.Name, "testing") {
		return false
	}
	return (pointer && (sel.Sel.Name == "T" || sel.Sel.Name == "B")) || (!pointer && sel.Sel.Name == "TB")
}

// findZeroAllocTestsInFile walks function bodies with their testing parameter and imports.
func findZeroAllocTestsInFile(file *ast.File) []string {
	var tested []string
	imports := FileImports(file)
	for i := 0; i < len(file.Decls) && i < maxHotAllocDecls; i++ {
		fn, ok := file.Decls[i].(*ast.FuncDecl)
		if !ok || fn.Body == nil {
			continue
		}
		ctx := hotTestContext{imports: imports, testParam: hotTestingParam(fn, imports)}
		ast.Inspect(fn.Body, func(n ast.Node) bool {
			if block, ok := n.(*ast.BlockStmt); ok {
				tested = append(tested, inspectBlockForAllocTests(block, ctx)...)
			}
			return true
		})
	}
	return tested
}

// inspectBlockForAllocTests examines statements for allocation tests and their assertions.
func inspectBlockForAllocTests(block *ast.BlockStmt, ctx hotTestContext) []string {
	var tested []string
	stmts := block.List
	for i := 0; i < len(stmts) && i < maxHotAllocStmts; i++ {
		switch s := stmts[i].(type) {
		case *ast.AssignStmt:
			tested = append(tested, checkAssignAllocTest(stmts, i, s, ctx)...)
		case *ast.DeclStmt:
			tested = append(tested, checkDeclAllocTest(stmts, i, s, ctx)...)
		case *ast.IfStmt:
			tested = append(tested, checkIfAllocTest(s, ctx)...)
		}
	}
	return tested
}

// checkAssignAllocTest recognizes an allocation measurement assigned for a later assertion.
func checkAssignAllocTest(stmts []ast.Stmt, idx int, assign *ast.AssignStmt, ctx hotTestContext) []string {
	if len(assign.Lhs) == 0 || len(assign.Rhs) == 0 {
		return nil
	}
	call, ok := assign.Rhs[0].(*ast.CallExpr)
	if !ok {
		return nil
	}
	targets, valid := checkAllocsCall(call, ctx)
	if !valid {
		return nil
	}
	id, ok := assign.Lhs[0].(*ast.Ident)
	if !ok || id.Name == "_" {
		return nil
	}
	if hasZeroAssertion(stmts[idx+1:], id.Name, nil, ctx.testParam) {
		return targets
	}
	return nil
}

// checkDeclAllocTest recognizes an allocation measurement declared for a later assertion.
func checkDeclAllocTest(stmts []ast.Stmt, idx int, declStmt *ast.DeclStmt, ctx hotTestContext) []string {
	gen, ok := declStmt.Decl.(*ast.GenDecl)
	if !ok || gen.Tok != token.VAR {
		return nil
	}
	for i := 0; i < len(gen.Specs) && i < maxHotAllocDecls; i++ {
		spec, ok := gen.Specs[i].(*ast.ValueSpec)
		if ok {
			if targets := checkValueSpecAllocTest(stmts, idx, spec, ctx); len(targets) > 0 {
				return targets
			}
		}
	}
	return nil
}

// checkValueSpecAllocTest checks one var ValueSpec for a zero-asserted AllocsPerRun measurement.
func checkValueSpecAllocTest(stmts []ast.Stmt, idx int, spec *ast.ValueSpec, ctx hotTestContext) []string {
	if len(spec.Names) == 0 || len(spec.Values) == 0 || spec.Names[0].Name == "_" {
		return nil
	}
	call, ok := spec.Values[0].(*ast.CallExpr)
	if !ok {
		return nil
	}
	targets, valid := checkAllocsCall(call, ctx)
	if valid && hasZeroAssertion(stmts[idx+1:], spec.Names[0].Name, nil, ctx.testParam) {
		return targets
	}
	return nil
}

// checkIfInitAllocTest recognizes an allocation measurement in an if initializer.
func checkIfInitAllocTest(ifStmt *ast.IfStmt, ctx hotTestContext) []string {
	assign, ok := ifStmt.Init.(*ast.AssignStmt)
	if !ok || len(assign.Lhs) == 0 || len(assign.Rhs) == 0 {
		return nil
	}
	call, ok := assign.Rhs[0].(*ast.CallExpr)
	if !ok {
		return nil
	}
	id, ok := assign.Lhs[0].(*ast.Ident)
	if !ok || id.Name == "_" {
		return nil
	}
	targets, valid := checkAllocsCall(call, ctx)
	if valid && isZeroCondition(ifStmt.Cond, id.Name, nil) {
		return targets
	}
	return nil
}

// checkIfCondAllocTest recognizes an allocation measurement used directly in an if condition.
func checkIfCondAllocTest(ifStmt *ast.IfStmt, ctx hotTestContext) []string {
	bin, ok := unwrapCond(ifStmt.Cond).(*ast.BinaryExpr)
	if !ok {
		return nil
	}
	call, ok := bin.X.(*ast.CallExpr)
	if !ok {
		return nil
	}
	targets, valid := checkAllocsCall(call, ctx)
	if valid && isZeroCondition(bin, "", call) {
		return targets
	}
	return nil
}

// checkIfAllocTest checks whether an if statement establishes a zero-allocation assertion.
func checkIfAllocTest(ifStmt *ast.IfStmt, ctx hotTestContext) []string {
	if !bodyFailsTest(ifStmt.Body, ctx.testParam) {
		return nil
	}
	if targets := checkIfInitAllocTest(ifStmt, ctx); len(targets) > 0 {
		return targets
	}
	return checkIfCondAllocTest(ifStmt, ctx)
}

type zeroAssertionWalk struct {
	varName   string
	call      *ast.CallExpr
	testParam string
	found     bool
	stopped   bool
}

// hasZeroAssertion looks for a failure assertion while the measured allocation binding still holds.
func hasZeroAssertion(stmts []ast.Stmt, varName string, call *ast.CallExpr, testParam string) bool {
	if varName == "_" {
		return false
	}
	w := zeroAssertionWalk{varName: varName, call: call, testParam: testParam}
	for i := 0; i < len(stmts) && i < maxHotAllocStmts && !w.found && !w.stopped; i++ {
		ast.Inspect(stmts[i], w.visit)
	}
	return w.found
}

// visit inspects one AST node in source order, halting when the binding is reassigned or shadowed.
func (w *zeroAssertionWalk) visit(n ast.Node) bool {
	if w.found || w.stopped || n == nil {
		return false
	}
	if ifStmt, ok := n.(*ast.IfStmt); ok {
		if rebindsHotVar(ifStmt.Init, w.varName) {
			w.stopped = true
			return false
		}
		if isZeroCondition(ifStmt.Cond, w.varName, w.call) && bodyFailsTest(ifStmt.Body, w.testParam) {
			w.found = true
			return false
		}
		return true
	}
	if rebindsHotVar(n, w.varName) {
		w.stopped = true
		return false
	}
	return true
}

// rebindsHotVar reports whether node assigns to or redeclares varName.
func rebindsHotVar(node ast.Node, varName string) bool {
	if node == nil || varName == "" {
		return false
	}
	return rebindsHotStmt(node, varName) || rebindsHotDecl(node, varName)
}

// rebindsHotStmt reports whether a statement node assigns to or defines varName.
func rebindsHotStmt(node ast.Node, varName string) bool {
	switch s := node.(type) {
	case *ast.AssignStmt:
		return exprListNamesVar(s.Lhs, varName)
	case *ast.IncDecStmt:
		return exprNamesVar(s.X, varName)
	case *ast.RangeStmt:
		return isRangeBindingTok(s.Tok) && (exprNamesVar(s.Key, varName) || exprNamesVar(s.Value, varName))
	default:
		return false
	}
}

// isRangeBindingTok reports whether a range statement binds loop variables.
func isRangeBindingTok(tok token.Token) bool {
	return tok == token.DEFINE || tok == token.ASSIGN
}

// rebindsHotDecl reports whether a specification or function literal redeclares varName.
func rebindsHotDecl(node ast.Node, varName string) bool {
	switch s := node.(type) {
	case *ast.ValueSpec:
		return identListNamesVar(s.Names, varName)
	case *ast.TypeSpec:
		return s.Name != nil && s.Name.Name == varName
	case *ast.FuncLit:
		return funcTypeNamesVar(s.Type, varName)
	default:
		return false
	}
}

// exprNamesVar reports whether expr is the identifier varName after unwrapping parentheses.
func exprNamesVar(expr ast.Expr, varName string) bool {
	if expr == nil {
		return false
	}
	id, ok := unwrapCond(expr).(*ast.Ident)
	return ok && id.Name == varName
}

// exprListNamesVar reports whether any expression in exprs names varName.
func exprListNamesVar(exprs []ast.Expr, varName string) bool {
	for i := 0; i < len(exprs) && i < maxHotAllocDecls; i++ {
		if exprNamesVar(exprs[i], varName) {
			return true
		}
	}
	return false
}

// identListNamesVar reports whether any identifier in idents names varName.
func identListNamesVar(idents []*ast.Ident, varName string) bool {
	for i := 0; i < len(idents) && i < maxHotAllocDecls; i++ {
		if idents[i] != nil && idents[i].Name == varName {
			return true
		}
	}
	return false
}

// funcTypeNamesVar reports whether a function signature declares a parameter or result named varName.
func funcTypeNamesVar(ft *ast.FuncType, varName string) bool {
	if ft == nil {
		return false
	}
	return fieldListNamesVar(ft.Params, varName) || fieldListNamesVar(ft.Results, varName)
}

// fieldListNamesVar reports whether any field in fields declares varName.
func fieldListNamesVar(fields *ast.FieldList, varName string) bool {
	if fields == nil {
		return false
	}
	for i := 0; i < len(fields.List) && i < maxHotAllocDecls; i++ {
		if identListNamesVar(fields.List[i].Names, varName) {
			return true
		}
	}
	return false
}

// isZeroCondition recognizes a condition comparing an allocation count with zero.
func isZeroCondition(cond ast.Expr, varName string, call *ast.CallExpr) bool {
	bin, ok := unwrapCond(cond).(*ast.BinaryExpr)
	if !ok {
		return false
	}
	switch bin.Op {
	case token.NEQ:
		return (isMatchingAllocs(bin.X, varName, call) && isZeroLiteral(bin.Y)) ||
			(isZeroLiteral(bin.X) && isMatchingAllocs(bin.Y, varName, call))
	case token.GTR:
		return isMatchingAllocs(bin.X, varName, call) && isZeroLiteral(bin.Y)
	case token.LSS:
		return isZeroLiteral(bin.X) && isMatchingAllocs(bin.Y, varName, call)
	default:
		return false
	}
}

// unwrapCond removes enclosing parentheses before inspecting a condition.
func unwrapCond(expr ast.Expr) ast.Expr {
	for i := 0; i < maxDocLines; i++ {
		if paren, ok := expr.(*ast.ParenExpr); ok {
			expr = paren.X
		} else {
			break
		}
	}
	return expr
}

var predeclaredNumericTypes = map[string]bool{
	"int": true, "int8": true, "int16": true, "int32": true, "int64": true,
	"uint": true, "uint8": true, "uint16": true, "uint32": true, "uint64": true, "uintptr": true,
	"float32": true, "float64": true,
}

// isPredeclaredNumericConversion reports whether call converts a single value to a predeclared numeric type.
func isPredeclaredNumericConversion(call *ast.CallExpr) bool {
	if len(call.Args) != 1 || call.Ellipsis.IsValid() {
		return false
	}
	id, ok := call.Fun.(*ast.Ident)
	return ok && predeclaredNumericTypes[id.Name]
}

// isZeroLiteral recognizes a literal zero, optionally wrapped in predeclared numeric conversions.
func isZeroLiteral(expr ast.Expr) bool {
	for i := 0; i < maxDocLines; i++ {
		expr = unwrapCond(expr)
		if call, ok := expr.(*ast.CallExpr); ok && isPredeclaredNumericConversion(call) {
			expr = call.Args[0]
			continue
		}
		break
	}
	expr = unwrapCond(expr)
	if lit, ok := expr.(*ast.BasicLit); ok && (lit.Kind == token.INT || lit.Kind == token.FLOAT) {
		return lit.Value == "0" || lit.Value == "0.0" || lit.Value == "0."
	}
	return false
}

// isMatchingAllocs matches an allocation measurement to the identifier under test.
func isMatchingAllocs(expr ast.Expr, varName string, call *ast.CallExpr) bool {
	expr = unwrapCond(expr)
	if varName != "" {
		if id, ok := expr.(*ast.Ident); ok && id.Name == varName {
			return true
		}
	}
	if call != nil && expr == call {
		return true
	}
	return false
}

// bodyFailsTest checks whether a branch contains a test failure call.
func bodyFailsTest(body *ast.BlockStmt, testParam string) bool {
	if body == nil {
		return false
	}
	found := false
	ast.Inspect(body, func(n ast.Node) bool {
		if found || n == nil {
			return false
		}
		if call, ok := n.(*ast.CallExpr); ok && callFailsTest(call, testParam) {
			found = true
			return false
		}
		return true
	})
	return found
}

// callFailsTest recognizes calls that fail the current test.
func callFailsTest(c *ast.CallExpr, testParam string) bool {
	sel, ok := c.Fun.(*ast.SelectorExpr)
	if !ok || testParam == "" {
		return false
	}
	receiver, ok := sel.X.(*ast.Ident)
	if !ok || receiver.Name != testParam {
		return false
	}
	switch sel.Sel.Name {
	case "Fatal", "Fatalf", "Error", "Errorf", "FailNow":
		return true
	default:
		return false
	}
}

// checkAllocsCall extracts target calls from a valid testing.AllocsPerRun measurement.
func checkAllocsCall(call *ast.CallExpr, ctx hotTestContext) ([]string, bool) {
	if call == nil || len(call.Args) < 2 {
		return nil, false
	}
	if !isAllocsPerRunIdent(call.Fun) {
		return nil, false
	}
	if !isValidRunsArg(call.Args[0]) {
		return nil, false
	}
	targets := extractCallees(call.Args[1], ctx.imports)
	if len(targets) == 0 {
		return nil, false
	}
	return targets, true
}

// isAllocsPerRunIdent recognizes an AllocsPerRun selector or identifier for allocation checks.
func isAllocsPerRunIdent(fun ast.Expr) bool {
	fun = unwrapCond(fun)
	if sel, ok := fun.(*ast.SelectorExpr); ok {
		return sel.Sel.Name == "AllocsPerRun"
	}
	if id, ok := fun.(*ast.Ident); ok {
		return id.Name == "AllocsPerRun"
	}
	return false
}

// isValidRunsArg checks the measurement count before accepting an allocation test.
func isValidRunsArg(arg ast.Expr) bool {
	arg = unwrapCond(arg)
	if lit, ok := arg.(*ast.BasicLit); ok && lit.Kind == token.INT {
		val, err := strconv.Atoi(lit.Value)
		if err == nil && val <= 0 {
			return false
		}
	}
	return true
}

// extractCallees identifies the function or calls passed to an allocation measurement.
func extractCallees(arg ast.Expr, imports GoImports) []string {
	var callees []string
	if id, ok := unwrapCallee(arg, imports); ok {
		callees = append(callees, id)
		return callees
	}
	ast.Inspect(arg, func(n ast.Node) bool {
		if call, ok := n.(*ast.CallExpr); ok {
			if name, ok := unwrapCallee(call.Fun, imports); ok {
				callees = append(callees, name)
			}
		}
		return true
	})
	return callees
}

// unwrapCallee removes wrappers from a callee expression before identifying its target.
func unwrapCallee(fun ast.Expr, imports GoImports) (string, bool) {
	fun = unwrapCond(fun)
	if idx, ok := fun.(*ast.IndexExpr); ok {
		fun = idx.X
	}
	if idxList, ok := fun.(*ast.IndexListExpr); ok {
		fun = idxList.X
	}
	fun = unwrapCond(fun)
	if id, ok := fun.(*ast.Ident); ok {
		return "func:" + id.Name, true
	}
	if sel, ok := fun.(*ast.SelectorExpr); ok {
		if id, ok := sel.X.(*ast.Ident); ok {
			if _, isPkg := imports.Path(id.Name); isPkg {
				return "func:" + sel.Sel.Name, true
			}
		}
		return "method:" + sel.Sel.Name, true
	}
	return "", false
}
