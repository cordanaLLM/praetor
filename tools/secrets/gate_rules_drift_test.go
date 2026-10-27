// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package secrets_test

import (
	"bytes"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/cordanaLLM/praetor/internal/secretscan"
	"github.com/cordanaLLM/praetor/tools/secrets"
)

func TestRulesGen_NoDrift(t *testing.T) {
	srcPath := filepath.Join("..", "..", secrets.RulesSourcePath)
	src, err := os.ReadFile(srcPath)
	if err != nil {
		t.Fatalf("read %s: %v", srcPath, err)
	}
	want, err := secrets.GenerateRulesGen(src)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	destPath := filepath.Join("..", "..", secrets.RulesGenDestPath)
	got, err := os.ReadFile(destPath)
	if err != nil {
		t.Fatalf("read %s: %v", destPath, err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("%s differs from generator output; run 'go run ./tools/secrets/cmd/generate' to update", destPath)
	}
}

func findConstStringInAST(f *ast.File, name string) (string, bool) {
	for _, decl := range f.Decls {
		genDecl, ok := decl.(*ast.GenDecl)
		if ok && genDecl.Tok == token.CONST {
			if val, ok := findValueInSpecs(genDecl.Specs, name); ok {
				return val, true
			}
		}
	}
	return "", false
}

func findValueInSpecs(specs []ast.Spec, target string) (string, bool) {
	for _, spec := range specs {
		valSpec, ok := spec.(*ast.ValueSpec)
		if !ok {
			continue
		}
		for i, id := range valSpec.Names {
			if id.Name == target && i < len(valSpec.Values) {
				if lit, ok := valSpec.Values[i].(*ast.BasicLit); ok && lit.Kind == token.STRING {
					val, err := strconv.Unquote(lit.Value)
					return val, err == nil
				}
			}
		}
	}
	return "", false
}

func parseGateLogOpts(t *testing.T) string {
	t.Helper()
	src, err := secrets.Read(secrets.GateFile)
	if err != nil {
		t.Fatalf("read gate file: %v", err)
	}
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, secrets.GateFile, src, 0)
	if err != nil {
		t.Fatalf("parse gate file: %v", err)
	}
	val, ok := findConstStringInAST(f, "logOpts")
	if !ok {
		t.Fatalf("logOpts string const not found (or not unquotable) in %s", secrets.GateFile)
	}
	return val
}

func TestGateLogOpts_MatchesEngine(t *testing.T) {
	got := parseGateLogOpts(t)
	if got != secretscan.LogOpts {
		t.Errorf("gate logOpts %q != secretscan.LogOpts %q", got, secretscan.LogOpts)
	}
}
