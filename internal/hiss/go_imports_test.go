package hiss

import (
	"go/ast"
	"go/parser"
	"go/token"
	"testing"
)

// TestDefaultImportNameDerivesTheBoundName covers the names an unrenamed import binds:
// the last path element, a module major-version element skipped and a gopkg.in version
// suffix dropped.
func TestDefaultImportNameDerivesTheBoundName(t *testing.T) {
	cases := map[string]string{
		"unsafe":                      "unsafe",
		"net/http":                    "http",
		"example.com/mod/v2":          "mod",
		"example.com/mod/v10":         "mod",
		"gopkg.in/yaml.v3":            "yaml",
		"gopkg.in/check.v1":           "check",
		"github.com/org/repo/sub/pkg": "pkg",
	}
	for importPath, want := range cases {
		if got := DefaultImportName(importPath); got != want {
			t.Errorf("DefaultImportName(%q) = %q, want %q", importPath, got, want)
		}
	}
}

// TestDefaultImportNameNegativeLookalikes keeps elements that only resemble a version: a
// bare v2 path is the package v2, and neither "v" nor "vx" is a major version.
func TestDefaultImportNameNegativeLookalikes(t *testing.T) {
	cases := map[string]string{
		"v2":                "v2",
		"example.com/v":     "v",
		"example.com/vx":    "vx",
		"example.com/foo.v": "foo.v",
		"example.com/a.vb2": "a.vb2",
	}
	for importPath, want := range cases {
		if got := DefaultImportName(importPath); got != want {
			t.Errorf("DefaultImportName(%q) = %q, want %q", importPath, got, want)
		}
	}
}

// TestDefaultImportNameBoundaryShortPaths covers the shortest inputs: an empty path, a
// ".v1" element with nothing before the suffix, and a one-letter name before it.
func TestDefaultImportNameBoundaryShortPaths(t *testing.T) {
	cases := map[string]string{
		"":     ".",
		".v1":  ".v1",
		"a.v1": "a",
	}
	for importPath, want := range cases {
		if got := DefaultImportName(importPath); got != want {
			t.Errorf("DefaultImportName(%q) = %q, want %q", importPath, got, want)
		}
	}
}

// TestGoImportsPathResolvesBoundNames covers Path in all three directions: a renamed and a
// version-suffixed import resolve, an unbound, blank or dot-imported name does not.
func TestGoImportsPathResolvesBoundNames(t *testing.T) {
	src := "package p\n\nimport (\n\tstdctx \"context\"\n\t\"gopkg.in/yaml.v3\"\n\t. \"strings\"\n\t_ \"embed\"\n)\n"
	file, err := parser.ParseFile(token.NewFileSet(), "p.go", src, parser.ImportsOnly)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	im := FileImports(file)
	for name, want := range map[string]string{"stdctx": "context", "yaml": "gopkg.in/yaml.v3"} {
		if got, ok := im.Path(name); !ok || got != want {
			t.Errorf("Path(%q) = %q, %v; want %q, true", name, got, ok, want)
		}
	}
	for _, name := range []string{"context", "strings", ".", "_", "embed", "yaml.v3", ""} {
		if got, ok := im.Path(name); ok {
			t.Errorf("Path(%q) = %q, true; want unbound", name, got)
		}
	}
	if got, ok := (GoImports{}).Path("context"); ok || got != "" {
		t.Errorf("zero GoImports resolved context to %q", got)
	}
}

// receiverExpr parses the receiver type of the one method in src.
func receiverExpr(t *testing.T, recv string) ast.Expr {
	t.Helper()
	src := "package p\n\nfunc (" + recv + ") M() {}\n"
	file, err := parser.ParseFile(token.NewFileSet(), "p.go", src, 0)
	if err != nil {
		t.Fatalf("parse %q: %v", recv, err)
	}
	fn, ok := file.Decls[0].(*ast.FuncDecl)
	if !ok || fn.Recv == nil || len(fn.Recv.List) != 1 {
		t.Fatalf("parse %q: no single receiver", recv)
	}
	return fn.Recv.List[0].Type
}

// TestReceiverTypeNameUnwrapsToTheBaseType covers the receiver shapes Go allows: plain,
// pointer, parenthesized and generic with one or several type parameters.
func TestReceiverTypeNameUnwrapsToTheBaseType(t *testing.T) {
	cases := map[string]string{
		"s Set":          "Set",
		"s *Set":         "Set",
		"s *Set[T]":      "Set",
		"p Pair[K, V]":   "Pair",
		"p *Pair[K, V]":  "Pair",
		"s (*Set)":       "Set",
		"p (Pair[K, V])": "Pair",
		"*Anonymous[T]":  "Anonymous",
	}
	for recv, want := range cases {
		got, ok := ReceiverTypeName(receiverExpr(t, recv))
		if !ok || got != want {
			t.Errorf("ReceiverTypeName(%q) = %q, %v; want %q, true", recv, got, ok, want)
		}
	}
}

// TestReceiverTypeNameNegativeShapes rejects expressions that name no declared type.
func TestReceiverTypeNameNegativeShapes(t *testing.T) {
	for _, expr := range []ast.Expr{
		nil,
		&ast.SelectorExpr{X: ast.NewIdent("pkg"), Sel: ast.NewIdent("T")},
		&ast.ArrayType{Elt: ast.NewIdent("T")},
		&ast.StarExpr{X: &ast.MapType{Key: ast.NewIdent("K"), Value: ast.NewIdent("V")}},
	} {
		if got, ok := ReceiverTypeName(expr); ok || got != "" {
			t.Errorf("ReceiverTypeName(%T) = %q, %v; want \"\", false", expr, got, ok)
		}
	}
}

// TestReceiverTypeNameBoundaryDepth resolves a type wrapped in exactly the layers the bound
// allows and refuses one more, so a pathological expression cannot spin (HISS-02).
func TestReceiverTypeNameBoundaryDepth(t *testing.T) {
	wrap := func(layers int) ast.Expr {
		var expr ast.Expr = ast.NewIdent("Deep")
		for i := 0; i < layers; i++ {
			expr = &ast.StarExpr{X: expr}
		}
		return expr
	}
	if got, ok := ReceiverTypeName(wrap(maxReceiverTypeDepth - 1)); !ok || got != "Deep" {
		t.Errorf("%d layers: got %q, %v; want Deep, true", maxReceiverTypeDepth-1, got, ok)
	}
	if got, ok := ReceiverTypeName(wrap(maxReceiverTypeDepth)); ok {
		t.Errorf("%d layers must exceed the bound, got %q", maxReceiverTypeDepth, got)
	}
}
