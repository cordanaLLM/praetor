package hiss

import (
	"go/ast"
	"go/parser"
	"go/token"
	"strings"
	"testing"
)

func TestScanASTDepthBoundary(t *testing.T) {
	for _, extra := range []int{0, 1} {
		root := t.TempDir()
		// File -> FuncDecl -> BlockStmt -> ExprStmt -> parentheses -> CallExpr -> leaf.
		parens := maxNodeStack - 6 + extra
		source := "package fixture\nfunc f(){" + strings.Repeat("(", parens) + "panic(1)" + strings.Repeat(")", parens) + "}\n"
		file, err := parser.ParseFile(token.NewFileSet(), "depth.go", source, 0)
		if err != nil {
			t.Fatal(err)
		}
		depth, deepest := 0, 0
		ast.Inspect(file, func(n ast.Node) bool {
			if n == nil {
				depth--
				return true
			}
			depth++
			if depth > deepest {
				deepest = depth
			}
			return true
		})
		if deepest != maxNodeStack+extra {
			t.Fatalf("fixture depth %d, want %d", deepest, maxNodeStack+extra)
		}
		writeFixture(t, root, "depth.go", source)
		report := scanFixture(t, root, ScanOptions{})
		if report.Truncated != (extra > 0) {
			t.Fatalf("depth %d: truncated=%t", deepest, report.Truncated)
		}
		if extra == 0 && report.Breakdown["HISS-07"] != 1 {
			t.Fatalf("exact-bound scan missed panic: %+v", report)
		}
	}
}

func TestScanASTDepthCannotCertifyHiddenViolation(t *testing.T) {
	root := t.TempDir()
	source := "package fixture\nfunc f(){" + strings.Repeat("if true {", 1100) + "panic(1)" + strings.Repeat("}", 1100) + "}\n"
	writeFixture(t, root, "depth.go", source)
	report := scanFixture(t, root, ScanOptions{})
	if !report.Truncated {
		t.Fatalf("depth-limited scan must not certify a hidden panic: %+v", report)
	}
}
