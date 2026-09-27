// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package main

import (
	"errors"
	"flag"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// declaredFunctions returns the plain functions this package's non-test sources declare.
func declaredFunctions(t *testing.T) map[string]bool {
	t.Helper()
	sources, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatalf("glob sources: %v", err)
	}
	declared := map[string]bool{}
	fset := token.NewFileSet()
	for _, source := range sources {
		if strings.HasSuffix(source, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, source, nil, parser.SkipObjectResolution)
		if err != nil {
			t.Fatalf("parse %s: %v", source, err)
		}
		for _, decl := range file.Decls {
			if fn, ok := decl.(*ast.FuncDecl); ok && fn.Recv == nil {
				declared[fn.Name.Name] = true
			}
		}
	}
	return declared
}

// TestCommandHandlerNames_Positive_EveryCommandNamesADeclaredFunction pins the one assumption
// the docs references gate makes about the dispatch table: each handler is a named top-level
// function, so the gate can read its code. A closure registered as a handler fails here
// before it makes the gate fail.
func TestCommandHandlerNames_Positive_EveryCommandNamesADeclaredFunction(t *testing.T) {
	names, err := commandHandlerNames()
	if err != nil {
		t.Fatalf("commandHandlerNames: %v", err)
	}
	if len(names) != len(commandTable()) {
		t.Fatalf("named %d commands, the table has %d", len(names), len(commandTable()))
	}
	declared := declaredFunctions(t)
	for command, handler := range names {
		if !declared[handler] {
			t.Errorf("command %s is handled by %q, which this package does not declare as a function", command, handler)
		}
	}
	if names["conform"] != "runAdopt" || names["docs"] != "runDocs" {
		t.Errorf("aliases must name their shared handler: conform=%q docs=%q", names["conform"], names["docs"])
	}
}

func TestRunDocsReferences_Negative_RequiresAPraetorCheckout(t *testing.T) {
	err := dispatchCommand("docs", []string{"references", "--path=" + t.TempDir()})
	mustErrContain(t, err, "checks a Praetor source checkout")
	err = dispatchCommand("docs", []string{"references", "extra"})
	mustErrContain(t, err, "accepts no positional arguments")
}

func TestRunDocsReferences_Boundary_HelpAndUsage(t *testing.T) {
	if err := dispatchCommand("docs", []string{"references", "-h"}); !errors.Is(err, flag.ErrHelp) {
		t.Fatalf("docs references -h must be a satisfied help request, got %v", err)
	}
	out, err := captureStdout(t, func() error { return dispatchCommand("docs", []string{"help"}) })
	if err != nil {
		t.Fatalf("docs help: %v", err)
	}
	mustContain(t, out, "references [--path=.]")
	// A checkout directory that is a file, not a directory, is refused like a missing one.
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "cmd"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "cmd", "standardsctl"), []byte("x"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	err = dispatchCommand("docs", []string{"references", "--path=" + root})
	mustErrContain(t, err, "checks a Praetor source checkout")
}
