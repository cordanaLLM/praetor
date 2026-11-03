// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package agenthook

import (
	"context"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/contextopt"
	"github.com/cordanaLLM/praetor/internal/testsupport"
)

// TestCorrelationElapsedAssertions keeps runner load out of package test verdicts.
func TestCorrelationElapsedAssertions(t *testing.T) {
	ctx := testsupport.DeadlineContext(t)
	paths, err := filepath.Glob("*_test.go")
	if err != nil || len(paths) == 0 {
		t.Fatalf("enumerate package tests: %v (%d paths)", err, len(paths))
	}
	// Exceptions are keyed by file:function and must explain why wall time is required.
	allow := map[string]string{}
	for _, path := range paths {
		findings, err := elapsedAssertions(ctx, path, nil, allow)
		if err != nil {
			t.Fatal(err)
		}
		for _, finding := range findings {
			t.Error(finding)
		}
	}
}

// elapsedAssertions finds ordered elapsed-time comparisons controlling test failures.
func elapsedAssertions(ctx context.Context, path string, source any, allow map[string]string) ([]string, error) {
	if source == nil {
		raw, err := contextopt.ReadSnapshot(ctx, path)
		if err != nil {
			return nil, fmt.Errorf("read timing assertions: %w", err)
		}
		source = raw
	}
	positions := token.NewFileSet()
	file, err := parser.ParseFile(positions, path, source, parser.SkipObjectResolution)
	if err != nil {
		return nil, fmt.Errorf("parse timing assertions: %w", err)
	}
	var findings []string
	for _, declaration := range file.Decls {
		function, ok := declaration.(*ast.FuncDecl)
		if !ok || function.Body == nil {
			continue
		}
		key := filepath.Base(path) + ":" + function.Name.Name
		if reason, exists := allow[key]; exists {
			if strings.TrimSpace(reason) == "" {
				return nil, fmt.Errorf("timing exception %s requires a reason", key)
			}
			continue
		}
		for _, position := range elapsedFailurePositions(function.Body) {
			findings = append(findings, fmt.Sprintf("%s: elapsed-time bound controls a test failure; use a fake clock or ordering", positions.Position(position)))
		}
	}
	return findings, nil
}

// elapsedBindings tracks local duration deltas and boolean comparison aliases.
type elapsedBindings struct {
	deltas      map[string]bool
	comparisons map[string]bool
}

// elapsedFailurePositions follows local elapsed aliases, including if initializers.
func elapsedFailurePositions(body *ast.BlockStmt) []token.Pos {
	bindings := elapsedBindings{
		deltas:      map[string]bool{},
		comparisons: map[string]bool{},
	}
	var positions []token.Pos
	for node := range ast.Preorder(body) {
		rememberElapsedBindings(node, bindings)
		statement, ok := node.(*ast.IfStmt)
		if !ok {
			continue
		}
		// Preorder yields the if before its initializer.
		rememberElapsedBindings(statement.Init, bindings)
		if elapsedComparison(statement.Cond, bindings) && (containsTestFailure(statement.Body) || containsTestFailure(statement.Else)) {
			positions = append(positions, statement.Cond.Pos())
		}
	}
	return positions
}

// rememberElapsedBindings records assignment and declaration aliases without text matching.
func rememberElapsedBindings(node ast.Node, bindings elapsedBindings) {
	switch value := node.(type) {
	case *ast.AssignStmt:
		compound := compoundAssignment(value.Tok)
		for index, left := range value.Lhs {
			rememberElapsedName(left, assignedExpression(value.Rhs, len(value.Lhs), index), compound, bindings)
		}
	case *ast.ValueSpec:
		for index, left := range value.Names {
			rememberElapsedName(left, assignedExpression(value.Values, len(value.Names), index), false, bindings)
		}
	}
}

// compoundAssignment reports whether tok is a read-modify-write assignment operator.
func compoundAssignment(tok token.Token) bool {
	switch tok {
	case token.ADD_ASSIGN, token.SUB_ASSIGN, token.MUL_ASSIGN, token.QUO_ASSIGN, token.REM_ASSIGN,
		token.AND_ASSIGN, token.OR_ASSIGN, token.XOR_ASSIGN, token.SHL_ASSIGN, token.SHR_ASSIGN, token.AND_NOT_ASSIGN:
		return true
	default:
		return false
	}
}

// assignedExpression pairs a target slot with its RHS expression when arities match.
func assignedExpression(expressions []ast.Expr, targets, index int) ast.Expr {
	if len(expressions) == targets {
		return expressions[index]
	}
	return nil
}

// rememberElapsedName updates a local name's comparison or delta binding from its assigned expression.
func rememberElapsedName(left, right ast.Expr, compound bool, bindings elapsedBindings) {
	name, ok := left.(*ast.Ident)
	if !ok {
		return
	}
	if compound {
		if containsElapsed(right, bindings.deltas) {
			bindings.deltas[name.Name] = true
		}
		return
	}
	if elapsedComparison(right, bindings) {
		bindings.comparisons[name.Name] = true
		delete(bindings.deltas, name.Name)
		return
	}
	delete(bindings.comparisons, name.Name)
	if containsElapsed(right, bindings.deltas) {
		bindings.deltas[name.Name] = true
		return
	}
	delete(bindings.deltas, name.Name)
}

// containsElapsed recognises clock deltas and local variables assigned from them.
func containsElapsed(root ast.Node, names map[string]bool) bool {
	if root == nil {
		return false
	}
	for node := range ast.Preorder(root) {
		if name, ok := node.(*ast.Ident); ok && names[name.Name] {
			return true
		}
		call, ok := node.(*ast.CallExpr)
		if !ok {
			continue
		}
		if selector, ok := call.Fun.(*ast.SelectorExpr); ok && (selector.Sel.Name == "Since" || selector.Sel.Name == "Sub") {
			return true
		}
	}
	return false
}

// elapsedComparison covers both operand orders, strict/non-strict inequalities and boolean aliases.
func elapsedComparison(root ast.Node, bindings elapsedBindings) bool {
	if root == nil {
		return false
	}
	for node := range ast.Preorder(root) {
		if name, ok := node.(*ast.Ident); ok && bindings.comparisons[name.Name] {
			return true
		}
		comparison, ok := node.(*ast.BinaryExpr)
		if !ok {
			continue
		}
		switch comparison.Op {
		case token.LSS, token.LEQ, token.GTR, token.GEQ:
			if containsElapsed(comparison, bindings.deltas) {
				return true
			}
		}
	}
	return false
}

// containsTestFailure limits the guard to assertions, leaving elapsed logging alone.
func containsTestFailure(root ast.Node) bool {
	if root == nil {
		return false
	}
	for node := range ast.Preorder(root) {
		call, ok := node.(*ast.CallExpr)
		if !ok {
			continue
		}
		selector, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			continue
		}
		switch selector.Sel.Name {
		case "Error", "Errorf", "Fail", "FailNow", "Fatal", "Fatalf":
			return true
		}
	}
	return false
}

// TestCorrelationElapsedAssertionsPlanted refuses a real fixture before allowing a reasoned exception.
func TestCorrelationElapsedAssertionsPlanted(t *testing.T) {
	ctx := testsupport.DeadlineContext(t)
	path := filepath.Join("testdata", "elapsed-assertions", "planted_test.go")
	findings, err := elapsedAssertions(ctx, path, nil, nil)
	if err != nil || len(findings) != 1 {
		t.Fatalf("planted fixture: findings %v, error %v", findings, err)
	}
	key := "planted_test.go:TestPlantedElapsedBound"
	if _, err := elapsedAssertions(ctx, path, nil, map[string]string{key: " "}); err == nil {
		t.Fatal("blank exception reason accepted")
	}
	findings, err = elapsedAssertions(ctx, path, nil, map[string]string{key: "fixture demonstrating the forbidden pattern"})
	if err != nil || len(findings) != 0 {
		t.Fatalf("reasoned exception: findings %v, error %v", findings, err)
	}
	if _, err := elapsedAssertions(ctx, "missing_test.go", nil, nil); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("unreadable input did not fail closed: %v", err)
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := elapsedAssertions(canceled, path, nil, nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled context did not fail closed: %v", err)
	}
}

// TestCorrelationElapsedAssertionShapes pins negative, positive and equality boundary cases.
func TestCorrelationElapsedAssertionShapes(t *testing.T) {
	ctx := testsupport.DeadlineContext(t)
	for _, tc := range []struct {
		name, body string
		want       int
	}{
		{"direct", `if time.Since(start) > time.Second { t.Error("slow") }`, 1},
		{"initializer", `if elapsed := time.Since(start); elapsed >= bound { t.Fatalf("slow") }`, 1},
		{"alias", `elapsed := time.Since(start); took := elapsed; if took < bound { t.Fatal("early") }`, 1},
		{"boolean alias", `slow := time.Since(start) > bound; if slow { t.Fatal("slow") }`, 1},
		{"alias cleared", `slow := time.Since(start) > bound; slow = false; if slow { t.Fatal("x") }`, 0},
		{"alias re-bound", `slow := false; slow = time.Since(start) > bound; if slow { t.Fatal("x") }`, 1},
		{"delta cleared", `elapsed := time.Since(start); elapsed = 0; if elapsed > bound { t.Fatal("x") }`, 0},
		{"compound keeps delta", `elapsed := time.Since(start); elapsed -= offset; if elapsed > bound { t.Fatal("x") }`, 1},
		{"compound adds delta", `var elapsed time.Duration; elapsed += time.Since(start); if elapsed > bound { t.Fatal("x") }`, 1},
		{"multi-assign cleared", `slow := time.Since(start) > bound; slow, ok := next(); if slow && ok { t.Fatal("x") }`, 0},
		{"sub reversed", `var elapsed = end.Sub(start); if bound <= elapsed { t.Errorf("slow") }`, 1},
		{"compound", `if bad || time.Since(start) > bound { t.Fatal("slow") }`, 1},
		{"else", `if time.Since(start) < bound {} else { t.Fatal("slow") }`, 1},
		{"fail", `if time.Since(start) > bound { t.Fail() }`, 1},
		{"failnow", `if time.Since(start) > bound { t.FailNow() }`, 1},
		{"logging", `if time.Since(start) > bound { t.Log("slow") }`, 0},
		{"equality", `if time.Since(start) != bound { t.Fatal("fake clock mismatch") }`, 0},
		{"equality alias", `mismatch := time.Since(start) != bound; if mismatch { t.Fatal("fake clock mismatch") }`, 0},
		{"unrelated", `if count > limit { t.Fatal("count") }`, 0},
		{"literal", "t.Log(`if time.Since(start) > bound { t.Fatal(\"slow\") }`)", 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			findings, err := elapsedAssertions(ctx, "shape_test.go", "package agenthook; func TestShape() {"+tc.body+"}", nil)
			if err != nil || len(findings) != tc.want {
				t.Fatalf("findings %v, error %v; want %d", findings, err, tc.want)
			}
		})
	}
	if _, err := elapsedAssertions(ctx, "invalid_test.go", "not Go", nil); err == nil {
		t.Fatal("invalid input did not fail closed")
	}
}
