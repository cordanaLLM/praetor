// SPDX-FileCopyrightText: 2026 Lusoris <lusoris@pm.me>
// SPDX-License-Identifier: EUPL-1.2

package main

import (
	"go/ast"
	"path/filepath"
	"slices"
	"testing"
)

// TestAuditBestPracticesClaims_CLI verifies that the standardsctl audit CLI gate handles missing, valid, and invalid .bestpractices.json files.
func TestAuditBestPracticesClaims_CLI(t *testing.T) {
	f := newAuditFixture(t)
	out, err := f.audit(t)
	if err != nil {
		t.Fatalf("audit failed without .bestpractices.json: %v\nOutput: %s", err, out)
	}
	mustContain(t, out, "[SKIP] bestpractices-claims: no .bestpractices.json found; not applicable.")

	writeFixtureFile(t, f.dir, ".bestpractices.json",
		"{\n  \"test_policy_status\": \"Unmet\",\n  \"test_policy_justification\": \"No automated test suite applies to an organisation profile repository.\"\n}\n")
	out, err = f.audit(t)
	if err != nil {
		t.Fatalf("audit failed on valid .bestpractices.json: %v\nOutput: %s", err, out)
	}
	mustContain(t, out, "[PASS] bestpractices-claims: .bestpractices.json verified (1 criterion claim(s) checked against OpenSSF Best Practices criteria@8b3a2889e3af).")

	writeFixtureFile(t, f.dir, ".bestpractices.json",
		"{\n  \"description_good_status\": \"N/A\"\n}\n")
	if out, err = f.audit(t); err == nil {
		t.Fatalf("audit passed on forbidden N/A claim:\n%s", out)
	}
	mustErrContain(t, err, `[FAIL] bestpractices-claims: .bestpractices.json has 1 forbidden claim(s): criterion "description_good" (status "N/A", rule na_not_allowed)`)
}

// TestAuditRepositoryChecks_CLIAndMCPStayInSync verifies that the CLI audit (runAuditGates) and
// the MCP standards_audit context group (contextGroupGate) run the same shared repository checks
// in the same order.
func TestAuditRepositoryChecks_CLIAndMCPStayInSync(t *testing.T) {
	want := []string{
		"adopt.AuditEditorSelection",
		"adopt.AuditRenovateEngineBumps",
		"adopt.AuditRustMSRV",
		"bestpractices.Audit",
	}
	cliCalls := selectQualifiedCalls(parseFuncs(t, ".")["runAuditGates"], want)
	mcpCalls := selectQualifiedCalls(parseFuncs(t, filepath.Join("..", "standards-mcp"))["method:contextGroupGate"], want)
	if !slices.Equal(cliCalls, want) {
		t.Fatalf("CLI repository checks = %v, want %v", cliCalls, want)
	}
	if !slices.Equal(mcpCalls, want) {
		t.Fatalf("MCP contextGroupGate repository checks = %v, want %v", mcpCalls, want)
	}
}

// selectQualifiedCalls extracts package-qualified function calls from an AST function declaration that match target names.
func selectQualifiedCalls(decl *ast.FuncDecl, targets []string) []string {
	if decl == nil {
		return nil
	}
	var got []string
	ast.Inspect(decl, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		pkg, ok := sel.X.(*ast.Ident)
		if !ok {
			return true
		}
		name := pkg.Name + "." + sel.Sel.Name
		if slices.Contains(targets, name) {
			got = append(got, name)
		}
		return true
	})
	return got
}
