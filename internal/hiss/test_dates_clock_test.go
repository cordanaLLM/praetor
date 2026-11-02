// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package hiss

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A second wall-clock sample can change calendar days even inside one test.
func TestFixtureDatesSampleClockOnce(t *testing.T) {
	for _, path := range []string{
		"cmd/standardsctl/audit_backlog_test.go", "cmd/standardsctl/audit_build_warnings_test.go",
		"cmd/standardsctl/audit_supply_chain_test.go", "cmd/standardsctl/audit_mutants_test.go",
		"cmd/standardsctl/dedupe_test.go", "internal/promptopt/optimizer_test.go",
		"tools/apicompat/gate_exceptions_test.go", "tools/apicompat/gate_clock_test.go",
		"cmd/standardsctl/audit_exceptions_test.go",
		"internal/adopt/api_system_packages_test.go",
		"internal/config/exceptions_test.go",
		"internal/dedupe/dedupe_test.go",
		"internal/efficiency/collector_test.go",
		"internal/forge/job_timeout_audit_test.go",
		"internal/forge/workflow_run_health_test.go",
		"tools/apicompat/gate/download_retry_test.go",
		"tools/secrets/gate_test.go",
		"tribunus/internal/sources/codexlocal/source_test.go",
	} {
		data, err := os.ReadFile(filepath.Join("..", "..", filepath.FromSlash(path)))
		if err != nil {
			t.Fatal(err)
		}
		for name, reads := range fixtureClockReads(t, string(data)) {
			if reads > 1 {
				t.Errorf("%s:%s samples time.Now %d times; capture one test clock", path, name, reads)
			}
		}
	}
}

func TestFixtureClockReadCounter(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		want       int
	}{
		{"none", "return", 0},
		{"one", "today := time.Now(); _ = today.AddDate(0, 0, 1)", 1},
		{"planted midnight race", "_ = time.Now(); _ = time.Now()", 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := fixtureClockReads(t, "package p\nfunc TestClock() {"+tc.body+"}")["TestClock"]; got != tc.want {
				t.Fatalf("clock reads = %d, want %d", got, tc.want)
			}
		})
	}
}

func fixtureClockReads(t *testing.T, source string) map[string]int {
	t.Helper()
	file, err := parser.ParseFile(token.NewFileSet(), "clock_test.go", source, 0)
	if err != nil {
		t.Fatal(err)
	}
	counts := make(map[string]int)
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || !strings.HasPrefix(fn.Name.Name, "Test") {
			continue
		}
		for node := range ast.Preorder(fn) {
			call, ok := node.(*ast.CallExpr)
			if ok && isTimeNow(call.Fun) {
				counts[fn.Name.Name]++
			}
		}
	}
	return counts
}

func isTimeNow(expr ast.Expr) bool {
	sel, ok := expr.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	ident, ok := sel.X.(*ast.Ident)
	return ok && ident.Name == "time" && sel.Sel.Name == "Now"
}
