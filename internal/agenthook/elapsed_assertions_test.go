// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package agenthook

import (
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestCorrelationElapsedAssertions keeps runner load out of package test verdicts.
func TestCorrelationElapsedAssertions(t *testing.T) {
	paths, err := filepath.Glob("*_test.go")
	if err != nil || len(paths) == 0 {
		t.Fatalf("enumerate package tests: %v (%d paths)", err, len(paths))
	}
	// Exceptions are keyed by file:function and must explain why wall time is required.
	allow := map[string]string{}
	for _, path := range paths {
		findings, err := elapsedAssertions(path, nil, allow)
		if err != nil {
			t.Fatal(err)
		}
		for _, finding := range findings {
			t.Error(finding)
		}
	}
}

// elapsedAssertions finds ordered elapsed-time comparisons controlling test failures.
func elapsedAssertions(path string, source any, allow map[string]string) ([]string, error) {
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

// elapsedFailurePositions follows local elapsed aliases, including if initializers.
func elapsedFailurePositions(body *ast.BlockStmt) []token.Pos {
	names := map[string]bool{}
	var positions []token.Pos
	for node := range ast.Preorder(body) {
		rememberElapsedBindings(node, names)
		statement, ok := node.(*ast.IfStmt)
		if !ok {
			continue
		}
		// Preorder yields the if before its initializer.
		rememberElapsedBindings(statement.Init, names)
		if elapsedComparison(statement.Cond, names) && (containsTestFailure(statement.Body) || containsTestFailure(statement.Else)) {
			positions = append(positions, statement.Cond.Pos())
		}
	}
	return positions
}

// rememberElapsedBindings records assignment and declaration aliases without text matching.
func rememberElapsedBindings(node ast.Node, names map[string]bool) {
	switch value := node.(type) {
	case *ast.AssignStmt:
		for index, expression := range value.Rhs {
			if index < len(value.Lhs) {
				rememberElapsedName(value.Lhs[index], expression, names)
			}
		}
	case *ast.ValueSpec:
		for index, expression := range value.Values {
			if index < len(value.Names) {
				rememberElapsedName(value.Names[index], expression, names)
			}
		}
	}
}

// rememberElapsedName taints a local name when its assigned expression contains a delta.
func rememberElapsedName(left, right ast.Expr, names map[string]bool) {
	if name, ok := left.(*ast.Ident); ok && containsElapsed(right, names) {
		names[name.Name] = true
	}
}

// containsElapsed recognises clock deltas and local variables assigned from them.
func containsElapsed(root ast.Node, names map[string]bool) bool {
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

// elapsedComparison covers both operand orders and strict/non-strict inequalities.
func elapsedComparison(root ast.Node, names map[string]bool) bool {
	for node := range ast.Preorder(root) {
		comparison, ok := node.(*ast.BinaryExpr)
		if !ok {
			continue
		}
		switch comparison.Op {
		case token.LSS, token.LEQ, token.GTR, token.GEQ:
			if containsElapsed(comparison, names) {
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
		case "Error", "Errorf", "Fatal", "Fatalf":
			return true
		}
	}
	return false
}

// TestCorrelationElapsedAssertionsPlanted refuses a real fixture before allowing a reasoned exception.
func TestCorrelationElapsedAssertionsPlanted(t *testing.T) {
	path := filepath.Join("testdata", "elapsed-assertions", "planted_test.go")
	findings, err := elapsedAssertions(path, nil, nil)
	if err != nil || len(findings) != 1 {
		t.Fatalf("planted fixture: findings %v, error %v", findings, err)
	}
	key := "planted_test.go:TestPlantedElapsedBound"
	if _, err := elapsedAssertions(path, nil, map[string]string{key: " "}); err == nil {
		t.Fatal("blank exception reason accepted")
	}
	findings, err = elapsedAssertions(path, nil, map[string]string{key: "fixture demonstrating the forbidden pattern"})
	if err != nil || len(findings) != 0 {
		t.Fatalf("reasoned exception: findings %v, error %v", findings, err)
	}
	if _, err := elapsedAssertions("missing_test.go", nil, nil); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("unreadable input did not fail closed: %v", err)
	}
}

// TestCorrelationElapsedAssertionShapes pins negative, positive and equality boundary cases.
func TestCorrelationElapsedAssertionShapes(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		want       int
	}{
		{"direct", `if time.Since(start) > time.Second { t.Error("slow") }`, 1},
		{"initializer", `if elapsed := time.Since(start); elapsed >= bound { t.Fatalf("slow") }`, 1},
		{"alias", `elapsed := time.Since(start); took := elapsed; if took < bound { t.Fatal("early") }`, 1},
		{"sub reversed", `var elapsed = end.Sub(start); if bound <= elapsed { t.Errorf("slow") }`, 1},
		{"compound", `if bad || time.Since(start) > bound { t.Fatal("slow") }`, 1},
		{"else", `if time.Since(start) < bound {} else { t.Fatal("slow") }`, 1},
		{"logging", `if time.Since(start) > bound { t.Log("slow") }`, 0},
		{"equality", `if time.Since(start) != bound { t.Fatal("fake clock mismatch") }`, 0},
		{"unrelated", `if count > limit { t.Fatal("count") }`, 0},
		{"literal", "t.Log(`if time.Since(start) > bound { t.Fatal(\"slow\") }`)", 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			findings, err := elapsedAssertions("shape_test.go", "package agenthook; func TestShape() {"+tc.body+"}", nil)
			if err != nil || len(findings) != tc.want {
				t.Fatalf("findings %v, error %v; want %d", findings, err, tc.want)
			}
		})
	}
	if _, err := elapsedAssertions("invalid_test.go", "not Go", nil); err == nil {
		t.Fatal("invalid input did not fail closed")
	}
}
