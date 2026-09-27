// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package cavemansource

import (
	"go/ast"
	"go/parser"
	"go/token"
	"maps"
	"strings"
	"testing"
)

// parseMCPImports parses the import block src declares.
func parseMCPImports(t *testing.T, imports string) *ast.File {
	t.Helper()
	file, err := parser.ParseFile(token.NewFileSet(), "tool.go", "package tool\n\nimport (\n"+imports+")\n", parser.ImportsOnly)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	return file
}

// TestResolveMCPImportsBindsGovernedNames covers the MCP source reader's import table: plain
// and renamed governed imports bind, ungoverned and blank non-MCP imports bind nothing, and
// a dot import or a blank import of internal/mcp is refused.
func TestResolveMCPImportsBindsGovernedNames(t *testing.T) {
	// Positive: unrenamed imports bind their canonical name, an alias binds the alias.
	file := parseMCPImports(t, "\t\"fmt\"\n\tweb \"net/http\"\n\t\""+mcpImportPath+"\"\n\t\"os\"\n\t_ \"strings\"\n")
	got, err := resolveMCPImports(file)
	if err != nil {
		t.Fatalf("resolveMCPImports: %v", err)
	}
	want := map[string]string{"fmt": "fmt", "web": "http", "mcp": "mcp"}
	if !maps.Equal(got, want) {
		t.Fatalf("imports = %v, want %v", got, want)
	}
	// Negative: a dot import of a governed package and a blank import of internal/mcp.
	for name, imports := range map[string]string{
		"dot import of":            "\t. \"fmt\"\n",
		"blank import of internal": "\t_ \"" + mcpImportPath + "\"\n",
	} {
		if _, err := resolveMCPImports(parseMCPImports(t, imports)); err == nil || !strings.Contains(err.Error(), name) {
			t.Errorf("%s: err = %v, want refusal", name, err)
		}
	}
	// Boundary: no file, and a spec whose path does not unquote, bind nothing.
	if got, err := resolveMCPImports(nil); err != nil || len(got) != 0 {
		t.Fatalf("resolveMCPImports(nil) = %v, %v", got, err)
	}
	partial := &ast.File{Imports: []*ast.ImportSpec{{Path: &ast.BasicLit{Kind: token.STRING, Value: `"fmt`}}}}
	if got, err := resolveMCPImports(partial); err != nil || len(got) != 0 {
		t.Fatalf("resolveMCPImports(partial AST) = %v, %v", got, err)
	}
}
