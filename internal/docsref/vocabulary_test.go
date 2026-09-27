// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package docsref

import (
	"go/ast"
	"go/parser"
	"go/token"
	"maps"
	"testing"
)

// TestSourceTreeImportsBindsModulePackages covers the vocabulary tree's import table: a
// module package binds its package clause or its alias, while the module root, a sibling
// module sharing the prefix, the standard library and a partial AST bind nothing.
func TestSourceTreeImportsBindsModulePackages(t *testing.T) {
	root, inventory := writeModule(t, map[string]string{
		"internal/alpha/alpha.go": "package alpha\n",
		"internal/beta/beta.go":   "package betaimpl\n",
		"root.go":                 "package m\n",
	})
	tree, err := newSourceTree(root, inventory)
	if err != nil {
		t.Fatalf("newSourceTree: %v", err)
	}
	src := "package main\n\nimport (\n\t\"example.com/m/internal/alpha\"\n\t\"example.com/m/internal/beta\"\n" +
		"\tb \"example.com/m/internal/beta\"\n\t\"example.com/m\"\n\t\"example.com/m-plugins/x\"\n\t\"fmt\"\n)\n"
	file, err := parser.ParseFile(token.NewFileSet(), "main.go", src, parser.ImportsOnly)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	// Positive: the package clause names an unrenamed import, an alias names a renamed one.
	// Negative: the module root, the sibling module and fmt are absent.
	want := map[string]string{"alpha": "internal/alpha", "betaimpl": "internal/beta", "b": "internal/beta"}
	if got := tree.imports(file); !maps.Equal(got, want) {
		t.Fatalf("imports = %v, want %v", got, want)
	}
	// Boundary: no file, and a spec whose path does not unquote.
	if got := tree.imports(nil); len(got) != 0 {
		t.Fatalf("imports(nil) = %v", got)
	}
	partial := &ast.File{Imports: []*ast.ImportSpec{{Path: &ast.BasicLit{Kind: token.STRING, Value: `"example.com/m/internal/alpha`}}}}
	if got := tree.imports(partial); len(got) != 0 {
		t.Fatalf("imports(partial AST) = %v", got)
	}
}
