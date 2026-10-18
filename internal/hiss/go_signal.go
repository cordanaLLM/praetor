// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package hiss

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/printer"
	"go/token"
	"strings"
)

var signalFuncs = map[goFunc]struct{}{
	{"syscall", "Kill"}:               {},
	{"golang.org/x/sys/unix", "Kill"}: {},
}

var getpidFuncs = map[goFunc]struct{}{
	{"os", "Getpid"}:                    {},
	{"syscall", "Getpid"}:               {},
	{"golang.org/x/sys/unix", "Getpid"}: {},
}

// checkSignalCall enforces HISS-08: a process signal needs a target bounded above 1.
func (g *goScanner) checkSignalCall(call *ast.CallExpr) {
	fn, _, local, ok := resolvePackageCall(g.imports, call.Fun, signalFuncs)
	if !ok || g.shadowed(local) || len(call.Args) < 2 {
		return
	}
	if isLiteralZero(call.Args[1]) {
		return
	}
	rawTarget := ast.Unparen(call.Args[0])
	targetExpr := stripUnaryMinus(rawTarget)
	if isProcessPid(targetExpr) || g.isPidCall(targetExpr) || g.isDerivedPid(targetExpr) {
		return
	}
	targetText := g.exprString(targetExpr)
	if targetText != "" && g.isGuardedEarlier(targetText, call.Pos()) {
		return
	}
	callName := DefaultImportName(fn.Path) + "." + fn.Name
	g.record("HISS-08", call.Pos(), g.enclosingFunctionName(),
		fmt.Sprintf("Signal call %s with target %s must be guarded; add guard: if %s <= 1 || %s == os.Getpid() || %s == syscall.Getpgrp() { ... }",
			callName, targetText, targetText, targetText, targetText))
}

func isLiteralZero(expr ast.Expr) bool {
	lit, ok := ast.Unparen(expr).(*ast.BasicLit)
	return ok && lit.Kind == token.INT && lit.Value == "0"
}

func stripUnaryMinus(expr ast.Expr) ast.Expr {
	if unary, ok := expr.(*ast.UnaryExpr); ok && unary.Op == token.SUB {
		return ast.Unparen(unary.X)
	}
	return expr
}

func isProcessPid(expr ast.Expr) bool {
	sel, ok := expr.(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != "Pid" {
		return false
	}
	parent, ok := ast.Unparen(sel.X).(*ast.SelectorExpr)
	return ok && parent.Sel.Name == "Process"
}

func (g *goScanner) isPidCall(expr ast.Expr) bool {
	call, ok := expr.(*ast.CallExpr)
	if !ok {
		return false
	}
	_, _, local, ok := resolvePackageCall(g.imports, call.Fun, getpidFuncs)
	return ok && !g.shadowed(local)
}

func (g *goScanner) isDerivedPid(expr ast.Expr) bool {
	ident, ok := expr.(*ast.Ident)
	if !ok {
		return false
	}
	fn := g.enclosingFunc()
	if fn == nil || fn.Body == nil {
		return false
	}
	name := ident.Name
	assignCount := 0
	allValid := true
	ast.Inspect(fn.Body, func(n ast.Node) bool {
		var cnt int
		var valid bool
		switch stmt := n.(type) {
		case *ast.AssignStmt:
			cnt, valid = g.inspectAssignStmt(stmt, name)
		case *ast.ValueSpec:
			cnt, valid = g.inspectValueSpec(stmt, name)
		default:
			return true
		}
		assignCount += cnt
		if !valid {
			allValid = false
		}
		return true
	})
	return assignCount > 0 && allValid
}

func (g *goScanner) inspectAssignStmt(stmt *ast.AssignStmt, name string) (int, bool) {
	count := 0
	allValid := true
	for i, lhs := range stmt.Lhs {
		id, ok := ast.Unparen(lhs).(*ast.Ident)
		if ok && id.Name == name {
			count++
			var rhs ast.Expr
			if i < len(stmt.Rhs) {
				rhs = stmt.Rhs[i]
			}
			if !g.isValidPidSource(name, rhs) {
				allValid = false
			}
		}
	}
	return count, allValid
}

func (g *goScanner) inspectValueSpec(stmt *ast.ValueSpec, name string) (int, bool) {
	count := 0
	allValid := true
	for i, id := range stmt.Names {
		if id.Name == name {
			count++
			var val ast.Expr
			if i < len(stmt.Values) {
				val = stmt.Values[i]
			}
			if !g.isValidPidSource(name, val) {
				allValid = false
			}
		}
	}
	return count, allValid
}

func (g *goScanner) isValidPidSource(targetName string, expr ast.Expr) bool {
	if expr == nil {
		return false
	}
	stripped := stripUnaryMinus(ast.Unparen(expr))
	if isProcessPid(stripped) || g.isPidCall(stripped) {
		return true
	}
	if id, ok := stripped.(*ast.Ident); ok && id.Name == targetName {
		return true
	}
	return false
}

func (g *goScanner) isGuardedEarlier(targetText string, callPos token.Pos) bool {
	fn := g.enclosingFunc()
	if fn == nil || fn.Body == nil {
		return false
	}
	guarded := false
	ast.Inspect(fn.Body, func(n ast.Node) bool {
		if n == nil || guarded {
			return false
		}
		if n.Pos() >= callPos {
			return false
		}
		switch node := n.(type) {
		case *ast.BinaryExpr:
			if g.isComparisonGuard(node, targetText) {
				guarded = true
				return false
			}
		case *ast.CallExpr:
			if g.isCallGuard(node, targetText) {
				guarded = true
				return false
			}
		}
		return true
	})
	return guarded
}

func (g *goScanner) isComparisonGuard(bin *ast.BinaryExpr, targetText string) bool {
	op := goOpString(bin.Op)
	if op == "" {
		return false
	}
	if lit, ok := intLitValue012(bin.Y); ok && g.exprString(bin.X) == targetText {
		return boundRefusesOne(op, lit, true)
	}
	if lit, ok := intLitValue012(bin.X); ok && g.exprString(bin.Y) == targetText {
		return boundRefusesOne(op, lit, false)
	}
	return false
}

func goOpString(tok token.Token) string {
	switch tok {
	case token.GTR:
		return ">"
	case token.GEQ:
		return ">="
	case token.LEQ:
		return "<="
	case token.LSS:
		return "<"
	default:
		return ""
	}
}

func intLitValue012(expr ast.Expr) (int, bool) {
	lit, ok := ast.Unparen(expr).(*ast.BasicLit)
	if !ok || lit.Kind != token.INT {
		return 0, false
	}
	switch lit.Value {
	case "0":
		return 0, true
	case "1":
		return 1, true
	case "2":
		return 2, true
	default:
		return 0, false
	}
}

func (g *goScanner) isCallGuard(call *ast.CallExpr, targetText string) bool {
	name := ""
	switch f := ast.Unparen(call.Fun).(type) {
	case *ast.Ident:
		name = f.Name
	case *ast.SelectorExpr:
		name = f.Sel.Name
	}
	lower := strings.ToLower(name)
	if !strings.Contains(lower, "safe") && !strings.Contains(lower, "guard") {
		return false
	}
	for _, arg := range call.Args {
		if g.exprString(arg) == targetText {
			return true
		}
	}
	return false
}

func (g *goScanner) exprString(expr ast.Expr) string {
	if expr == nil {
		return ""
	}
	var buf bytes.Buffer
	if err := printer.Fprint(&buf, g.fset, ast.Unparen(expr)); err != nil {
		return ""
	}
	return strings.TrimSpace(buf.String())
}
