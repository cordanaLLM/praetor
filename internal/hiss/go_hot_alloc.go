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
	"path/filepath"
	"sort"
	"strconv"
	"strings"
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

func checkHotAllocations(ctx context.Context, rep *ScanReport, root string, paths []string) error {
	byDir, dirs := groupFilesByDir(paths)
	for i := 0; i < len(dirs) && i < maxHotAllocFiles; i++ {
		if ctx.Err() != nil {
			rep.Truncated = true
			return ctx.Err()
		}
		if err := checkDirHotAlloc(rep, root, byDir[dirs[i]]); err != nil {
			return err
		}
	}
	return nil
}

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

func checkDirHotAlloc(rep *ScanReport, root string, files []string) error {
	var prodFiles, testFiles []string
	for i := 0; i < len(files) && i < maxHotAllocFiles; i++ {
		if strings.HasSuffix(files[i], "_test.go") {
			testFiles = append(testFiles, files[i])
		} else {
			prodFiles = append(prodFiles, files[i])
		}
	}
	fset := token.NewFileSet()
	targets, err := collectHotTargets(fset, root, prodFiles, rep)
	if err != nil {
		return err
	}
	if len(targets) == 0 {
		return nil
	}
	tests, err := findZeroAllocTests(fset, testFiles)
	if err != nil {
		return err
	}
	reportMissingHotTests(rep, targets, tests)
	return nil
}

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

func collectHotTargets(fset *token.FileSet, root string, prodFiles []string, rep *ScanReport) ([]hotPathFunction, error) {
	var targets []hotPathFunction
	for i := 0; i < len(prodFiles) && i < maxHotAllocFiles; i++ {
		file, err := parser.ParseFile(fset, prodFiles[i], nil, parser.ParseComments|parser.SkipObjectResolution)
		if err != nil {
			return nil, fmt.Errorf("parse %s: %w", prodFiles[i], err)
		}
		rel, relErr := filepath.Rel(root, prodFiles[i])
		if relErr != nil {
			rel = prodFiles[i]
		}
		targets = append(targets, collectFileHotFunctions(fset, file, filepath.ToSlash(rel), rep)...)
	}
	return targets, nil
}

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

func findZeroAllocTests(fset *token.FileSet, testFiles []string) ([]string, error) {
	var results []string
	for i := 0; i < len(testFiles) && i < maxHotAllocFiles; i++ {
		file, err := parser.ParseFile(fset, testFiles[i], nil, parser.ParseComments|parser.SkipObjectResolution)
		if err != nil {
			return nil, fmt.Errorf("parse test %s: %w", testFiles[i], err)
		}
		results = append(results, findZeroAllocTestsInFile(file)...)
	}
	return results, nil
}

func findZeroAllocTestsInFile(file *ast.File) []string {
	var tested []string
	ast.Inspect(file, func(n ast.Node) bool {
		block, ok := n.(*ast.BlockStmt)
		if !ok {
			return true
		}
		tested = append(tested, inspectBlockForAllocTests(block)...)
		return true
	})
	return tested
}

func inspectBlockForAllocTests(block *ast.BlockStmt) []string {
	var tested []string
	stmts := block.List
	for i := 0; i < len(stmts) && i < maxHotAllocStmts; i++ {
		switch s := stmts[i].(type) {
		case *ast.AssignStmt:
			tested = append(tested, checkAssignAllocTest(stmts, i, s)...)
		case *ast.DeclStmt:
			tested = append(tested, checkDeclAllocTest(stmts, i, s)...)
		case *ast.IfStmt:
			tested = append(tested, checkIfAllocTest(s)...)
		}
	}
	return tested
}

func checkAssignAllocTest(stmts []ast.Stmt, idx int, assign *ast.AssignStmt) []string {
	if len(assign.Lhs) == 0 || len(assign.Rhs) == 0 {
		return nil
	}
	call, ok := assign.Rhs[0].(*ast.CallExpr)
	if !ok {
		return nil
	}
	targets, valid := checkAllocsCall(call)
	if !valid {
		return nil
	}
	id, ok := assign.Lhs[0].(*ast.Ident)
	if !ok {
		return nil
	}
	if hasZeroAssertion(stmts[idx+1:], id.Name, nil) {
		return targets
	}
	return nil
}

func checkDeclAllocTest(stmts []ast.Stmt, idx int, declStmt *ast.DeclStmt) []string {
	gen, ok := declStmt.Decl.(*ast.GenDecl)
	if !ok || gen.Tok != token.VAR {
		return nil
	}
	for i := 0; i < len(gen.Specs) && i < maxHotAllocDecls; i++ {
		spec, ok := gen.Specs[i].(*ast.ValueSpec)
		if !ok || len(spec.Names) == 0 || len(spec.Values) == 0 {
			continue
		}
		call, ok := spec.Values[0].(*ast.CallExpr)
		if !ok {
			continue
		}
		targets, valid := checkAllocsCall(call)
		if valid && hasZeroAssertion(stmts[idx+1:], spec.Names[0].Name, nil) {
			return targets
		}
	}
	return nil
}

func checkIfInitAllocTest(ifStmt *ast.IfStmt) []string {
	assign, ok := ifStmt.Init.(*ast.AssignStmt)
	if !ok || len(assign.Lhs) == 0 || len(assign.Rhs) == 0 {
		return nil
	}
	call, ok := assign.Rhs[0].(*ast.CallExpr)
	if !ok {
		return nil
	}
	id, ok := assign.Lhs[0].(*ast.Ident)
	if !ok {
		return nil
	}
	targets, valid := checkAllocsCall(call)
	if valid && isZeroCondition(ifStmt.Cond, id.Name, nil) {
		return targets
	}
	return nil
}

func checkIfCondAllocTest(ifStmt *ast.IfStmt) []string {
	bin, ok := unwrapCond(ifStmt.Cond).(*ast.BinaryExpr)
	if !ok {
		return nil
	}
	call, ok := bin.X.(*ast.CallExpr)
	if !ok {
		return nil
	}
	targets, valid := checkAllocsCall(call)
	if valid && isZeroCondition(bin, "", call) {
		return targets
	}
	return nil
}

func checkIfAllocTest(ifStmt *ast.IfStmt) []string {
	if !bodyFailsTest(ifStmt.Body) {
		return nil
	}
	if targets := checkIfInitAllocTest(ifStmt); len(targets) > 0 {
		return targets
	}
	return checkIfCondAllocTest(ifStmt)
}

func hasZeroAssertion(stmts []ast.Stmt, varName string, call *ast.CallExpr) bool {
	found := false
	for i := 0; i < len(stmts) && i < maxHotAllocStmts && !found; i++ {
		ast.Inspect(stmts[i], func(n ast.Node) bool {
			if found || n == nil {
				return false
			}
			if ifStmt, ok := n.(*ast.IfStmt); ok {
				if isZeroCondition(ifStmt.Cond, varName, call) && bodyFailsTest(ifStmt.Body) {
					found = true
					return false
				}
			}
			return true
		})
	}
	return found
}

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

func isZeroLiteral(expr ast.Expr) bool {
	for i := 0; i < maxDocLines; i++ {
		expr = unwrapCond(expr)
		if call, ok := expr.(*ast.CallExpr); ok && len(call.Args) == 1 {
			expr = call.Args[0]
			continue
		}
		break
	}
	expr = unwrapCond(expr)
	if lit, ok := expr.(*ast.BasicLit); ok {
		return lit.Value == "0" || lit.Value == "0.0" || lit.Value == "0."
	}
	return false
}

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

func bodyFailsTest(body *ast.BlockStmt) bool {
	if body == nil {
		return false
	}
	found := false
	ast.Inspect(body, func(n ast.Node) bool {
		if found || n == nil {
			return false
		}
		if call, ok := n.(*ast.CallExpr); ok && callFailsTest(call) {
			found = true
			return false
		}
		return true
	})
	return found
}

func callFailsTest(c *ast.CallExpr) bool {
	sel, ok := c.Fun.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	switch sel.Sel.Name {
	case "Fatal", "Fatalf", "Error", "Errorf", "FailNow":
		return true
	default:
		return false
	}
}

func checkAllocsCall(call *ast.CallExpr) ([]string, bool) {
	if call == nil || len(call.Args) < 2 {
		return nil, false
	}
	if !isAllocsPerRunIdent(call.Fun) {
		return nil, false
	}
	if !isValidRunsArg(call.Args[0]) {
		return nil, false
	}
	targets := extractCallees(call.Args[1])
	if len(targets) == 0 {
		return nil, false
	}
	return targets, true
}

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

func extractCallees(arg ast.Expr) []string {
	var callees []string
	if id, ok := unwrapCallee(arg); ok {
		callees = append(callees, id)
		return callees
	}
	ast.Inspect(arg, func(n ast.Node) bool {
		if call, ok := n.(*ast.CallExpr); ok {
			if name, ok := unwrapCallee(call.Fun); ok {
				callees = append(callees, name)
			}
		}
		return true
	})
	return callees
}

func unwrapCallee(fun ast.Expr) (string, bool) {
	fun = unwrapCond(fun)
	if idx, ok := fun.(*ast.IndexExpr); ok {
		fun = idx.X
	}
	if idxList, ok := fun.(*ast.IndexListExpr); ok {
		fun = idxList.X
	}
	fun = unwrapCond(fun)
	if id, ok := fun.(*ast.Ident); ok {
		return id.Name, true
	}
	if sel, ok := fun.(*ast.SelectorExpr); ok {
		return sel.Sel.Name, true
	}
	return "", false
}
