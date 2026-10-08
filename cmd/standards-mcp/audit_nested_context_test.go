package main

import (
	"path/filepath"
	"testing"

	"github.com/cordanaLLM/praetor/internal/compiler"
	"github.com/cordanaLLM/praetor/internal/testsupport"
)

// nestedProse is a nested AGENTS.md written in prose: it fails the caveman context check.
const nestedProse = "# Service\n\nSearch for an existing implementation before adding one. Grep the repository for the " +
	"capability and extend the code that is already there. Two implementations of one behavior are a " +
	"defect: they drift, and the second one stops matching the first.\n"

// trackNested writes text to rel below root and stages it, so the fixture repository tracks it.
func trackNested(t *testing.T, root, rel, text string) {
	t.Helper()
	writePathFixture(t, filepath.Join(root, filepath.FromSlash(rel)), text)
	testsupport.RunFixtureGit(t, root, []string{"add", "--", rel})
}

// Positive: a clean tracked nested AGENTS.md passes standards_audit, whose caveman gate counts
// it, and standards_compile_context verify_only.
func TestMCPAuditNestedContext_Positive(t *testing.T) {
	srv, root := newFixtureServer(t)
	trackNested(t, root, "service/AGENTS.md", fixtureSkill)
	audit := callTool(t, srv, "standards_audit", nil)
	expectText(t, "clean nested audit", audit, "[PASS] 1 nested AGENTS.md, 0 personas and 0 skills passed the caveman lint")
	expectText(t, "clean nested audit count", audit, "passed: 11/11")
	expectText(t, "clean nested verify", verifyInPlace(t, srv), "1 nested AGENTS.md, 0 personas and 0 skills passed")
}

// Negative, the #311 sentinel for the MCP mirrors: a tracked nested AGENTS.md in prose fails
// standards_audit and standards_compile_context verify_only, each naming the file. A persona in
// prose fails standards_audit as it fails the CLI audit, which the MCP audit used to skip.
func TestMCPAuditNestedContext_Negative(t *testing.T) {
	srv, root := newFixtureServer(t)
	trackNested(t, root, "service/AGENTS.md", nestedProse)
	label := filepath.Join("service", "AGENTS.md")
	audit := callTool(t, srv, "standards_audit", nil)
	expectError(t, "prose nested audit", audit, "[FAIL] Agent source caveman lint: nested AGENTS.md fails the caveman lint")
	expectError(t, "prose nested audit label", audit, label)
	verify := verifyInPlace(t, srv)
	expectError(t, "prose nested verify", verify, "nested AGENTS.md fails the caveman lint")
	expectError(t, "prose nested verify label", verify, label)

	srv, root = newFixtureServer(t)
	writePersona(t, root, "reviewer.md", fixturePersona+nestedProse)
	expectError(t, "prose persona audit", callTool(t, srv, "standards_audit", nil), "agent text fails the caveman lint")
}

// Boundary: the audit reads only tracked files, so the same prose in an untracked nested
// AGENTS.md is never read and every gate passes; and the prose fixture really fails the check
// the gate runs, so the pass is no empty one.
func TestMCPAuditNestedContext_Boundary(t *testing.T) {
	if _, err := compiler.LintContextText("fixture", nestedProse); err == nil {
		t.Fatal("the prose fixture passes the context lint, so this case proves nothing")
	}
	srv, root := newFixtureServer(t)
	writePathFixture(t, filepath.Join(root, "untracked", "AGENTS.md"), nestedProse)
	audit := callTool(t, srv, "standards_audit", nil)
	expectText(t, "untracked nested audit", audit, "[PASS] 0 nested AGENTS.md, 0 personas and 0 skills passed the caveman lint")
	expectText(t, "untracked nested audit count", audit, "passed: 11/11")
}
