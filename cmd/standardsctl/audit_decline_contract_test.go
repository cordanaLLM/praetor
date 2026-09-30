package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/adopt"
)

// declineReaders are the functions through which an audit gate reads adoption.decline: the one
// policy adoption applies (internal/adopt/declined.go).
var declineReaders = map[string]bool{"AuditDecline": true, "ManifestArtifactDeclined": true}

// cliDeclineGates names, per declinable step whose artefact the CLI audit reads, the function
// that looks its decline up; "adopt:" marks a gate the CLI shares from internal/adopt. A
// retained decline is listed where a gate narrows its failures.
var cliDeclineGates = map[string]string{
	"agent-harness":     "auditAgentContext",
	"dev-container":     "auditDevContainer",
	"makefile":          "resolveDocumentationDeclines",
	"formatter-ignore":  "resolveDocumentationDeclines",
	"readme":            "auditReadmeGovernance",
	"branch-ruleset":    "adopt:AuditBranchProtectionWithPolicy",
	"labels":            "adopt:AuditLabelTaxonomy",
	"paperclip":         "auditPaperclipHarness",
	"agent-definitions": "auditAgentDefinitions",
	"git-hooks":         "adopt:AuditGitHookConfig",
}

// mcpDeclineGates is cliDeclineGates for standards_audit.
var mcpDeclineGates = map[string]string{
	"agent-harness":  "auditContextSync",
	"branch-ruleset": "adopt:AuditBranchProtectionWithPolicy",
	"labels":         "adopt:AuditLabelTaxonomy",
	"git-hooks":      "adopt:AuditGitHookConfig",
}

// TestDeclineContract_Boundary_EveryAuditedStepIsLookedUp (#600): every declinable step whose
// artefact an audit gate reads without the decline covering it (adopt.DeclineAuditSkipped) is
// looked up through the shared reader by the gate that reads it, in the CLI and, where
// standards_audit reads it too, in the MCP audit. A newly declinable step, or a new gate over one,
// fails here until the gate reads the decline adoption accepts.
func TestDeclineContract_Boundary_EveryAuditedStepIsLookedUp(t *testing.T) {
	packages := map[string]map[string]*ast.FuncDecl{
		"cli":   parseFuncs(t, "."),
		"mcp":   parseFuncs(t, filepath.Join("..", "standards-mcp")),
		"adopt": parseFuncs(t, filepath.Join("..", "..", "internal", "adopt")),
	}
	contracts := adopt.DeclineContracts()
	audited := make(map[string]adopt.DeclineContract, len(contracts))
	for _, contract := range contracts {
		audited[contract.Step] = contract
		if contract.Audit == adopt.DeclineAuditSkipped && cliDeclineGates[contract.Step] == "" {
			t.Errorf("step %q is skipped by audit, but no CLI gate is registered as reading its decline", contract.Step)
		}
		if contract.Audit == adopt.DeclineAuditSkipped && contract.MCP && mcpDeclineGates[contract.Step] == "" {
			t.Errorf("step %q is read by standards_audit, but no MCP gate is registered as reading its decline", contract.Step)
		}
	}
	for side, gates := range map[string]map[string]string{"cli": cliDeclineGates, "mcp": mcpDeclineGates} {
		for step, gate := range gates {
			if contract, ok := audited[step]; !ok || contract.Audit == adopt.DeclineAuditNone || (side == "mcp" && !contract.MCP) {
				t.Errorf("%s gate %s registered for step %q, whose contract is %+v", side, gate, step, contract)
				continue
			}
			assertGateReadsDecline(t, packages, side, gate, step)
		}
	}
}

// assertGateReadsDecline fails unless gate, a function of side's package or "adopt:<name>", holds
// the step name as a literal and calls a decline reader, and side's package calls it, so a gate
// that exists but no audit runs cannot satisfy the contract.
func assertGateReadsDecline(t *testing.T, packages map[string]map[string]*ast.FuncDecl, side, gate, step string) {
	t.Helper()
	owner, name := side, gate
	if shared, ok := strings.CutPrefix(gate, "adopt:"); ok {
		owner, name = "adopt", shared
	}
	if !callsFunction(packages[side], name) {
		t.Errorf("%s audit never calls %s, registered for step %q", side, gate, step)
	}
	decl := packages[owner][name]
	if decl == nil || decl.Body == nil {
		t.Errorf("%s gate %s for step %q does not exist", side, gate, step)
		return
	}
	literal, reader := false, false
	ast.Inspect(decl.Body, func(node ast.Node) bool {
		switch n := node.(type) {
		case *ast.BasicLit:
			literal = literal || (n.Kind == token.STRING && n.Value == strconv.Quote(step))
		case *ast.CallExpr:
			reader = reader || declineReaders[calledName(n)]
		}
		return true
	})
	if !literal || !reader {
		t.Errorf("%s gate %s does not read the %q decline (step literal %v, decline reader %v)", side, gate, step, literal, reader)
	}
}

// parseFuncs returns the functions of the non-test Go files in dir by name, methods under
// "method:<name>" so a gate lookup finds only a function and a call search finds both.
func parseFuncs(t *testing.T, dir string) map[string]*ast.FuncDecl {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	funcs := make(map[string]*ast.FuncDecl)
	fset := token.NewFileSet()
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, filepath.Join(dir, name), nil, parser.SkipObjectResolution)
		if err != nil {
			t.Fatal(err)
		}
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			switch {
			case !ok:
			case fn.Recv == nil:
				funcs[fn.Name.Name] = fn
			default:
				funcs["method:"+fn.Name.Name] = fn
			}
		}
	}
	return funcs
}

// callsFunction reports whether any function in funcs calls a function named name.
func callsFunction(funcs map[string]*ast.FuncDecl, name string) bool {
	for _, decl := range funcs {
		found := false
		ast.Inspect(decl, func(node ast.Node) bool {
			if call, ok := node.(*ast.CallExpr); ok && calledName(call) == name {
				found = true
			}
			return !found
		})
		if found {
			return true
		}
	}
	return false
}

// calledName is the name a call expression calls: the selector of pkg.Name, or a bare Name.
func calledName(call *ast.CallExpr) string {
	switch fn := call.Fun.(type) {
	case *ast.SelectorExpr:
		return fn.Sel.Name
	case *ast.Ident:
		return fn.Name
	}
	return ""
}

// declinedFixture is the audit fixture whose manifest declines steps.
func declinedFixture(t *testing.T, manifest string, steps ...string) *auditFixture {
	t.Helper()
	f := newAuditFixture(t)
	if len(steps) > 0 {
		manifest += "adoption:\n  decline:\n    - " + strings.Join(steps, "\n    - ") + "\n"
	}
	writeFixtureFile(t, f.dir, ".standards.yaml", manifest)
	return f
}

func removeFixturePath(t *testing.T, f *auditFixture, rel string) {
	t.Helper()
	if err := os.RemoveAll(filepath.Join(f.dir, filepath.FromSlash(rel))); err != nil {
		t.Fatal(err)
	}
}

// TestAuditDeclinedArtefacts_Positive (#600): with labels, git-hooks or dev-container declined,
// the artefact adoption no longer writes may be absent, or kept by the repository as its own,
// and the full audit passes with a line naming the decline.
func TestAuditDeclinedArtefacts_Positive(t *testing.T) {
	cases := map[string]struct {
		mutate func(t *testing.T, f *auditFixture)
		want   string
	}{
		"labels": {func(t *testing.T, f *auditFixture) { removeFixturePath(t, f, ".config/labels.yaml") },
			"[PASS] Label taxonomy .config/labels.yaml declined by adoption.decline."},
		"git-hooks": {func(t *testing.T, f *auditFixture) { removeFixturePath(t, f, "lefthook.yml") },
			"[PASS] Git hooks (lefthook.yml and its activation) declined by adoption.decline."},
		"dev-container": {func(t *testing.T, f *auditFixture) {
			writeFixtureFile(t, f.dir, ".devcontainer/devcontainer.json", "{\"name\": \"operator-owned\"}\n")
		}, "[PASS] DevContainer configuration declined by adoption.decline."},
	}
	for step, tc := range cases {
		t.Run(step, func(t *testing.T) {
			f := declinedFixture(t, fixtureManifest("acme", "widgets", false), step)
			tc.mutate(t, f)
			out, err := f.audit(t)
			if err != nil {
				t.Fatalf("audit with %s declined: %v\n%s", step, err, out)
			}
			mustContain(t, out, tc.want)
		})
	}
}

// TestAuditDeclinedArtefacts_Negative (#600): without the decline the same missing or foreign
// artefact still fails, and a decline list naming a mandatory artefact fails the audit closed.
func TestAuditDeclinedArtefacts_Negative(t *testing.T) {
	cases := map[string]struct {
		rel, content, want string
	}{
		"labels":        {".config/labels.yaml", "", "Required label taxonomy .config/labels.yaml is missing"},
		"git-hooks":     {"lefthook.yml", "", "lefthook.yml configuration is missing"},
		"dev-container": {".devcontainer/devcontainer.json", "{\"name\": \"operator-owned\"}\n", "[FAIL] DevContainer"},
	}
	for step, tc := range cases {
		t.Run(step, func(t *testing.T) {
			f := newAuditFixture(t)
			if tc.content == "" {
				removeFixturePath(t, f, tc.rel)
			} else {
				writeFixtureFile(t, f.dir, tc.rel, tc.content)
			}
			out, err := f.audit(t)
			mustErrContain(t, err, tc.want)
			if strings.Contains(out, "declined by adoption.decline") {
				t.Fatalf("undeclined %s reported a decline:\n%s", step, out)
			}
		})
	}
	f := declinedFixture(t, fixtureManifest("acme", "widgets", false), "labels", "documentation-gate")
	_, err := f.audit(t)
	mustErrContain(t, err, `adoption cannot decline "documentation-gate"`)
}

// fixtureNoSources is the fixture manifest with a register.sources contract that declares no
// text, for a repository whose only bound text was the Paperclip harness.
func fixtureNoSources() string {
	return strings.TrimSuffix(fixtureManifest("acme", "widgets", false), fixtureRegisterSources()) +
		"register:\n  sources:\n    expected: 0\n    reason: no agent-facing text outside Markdown\n"
}

// TestAuditDeclinedPaperclipNoSources_Positive (#601): with paperclip declined and the harness
// gone, a contract declaring no text passes the audit, naming the decline and the reason, and
// `caveman check --configured-sources` passes with the same line.
func TestAuditDeclinedPaperclipNoSources_Positive(t *testing.T) {
	f := declinedFixture(t, fixtureNoSources(), "paperclip")
	removeFixturePath(t, f, ".paperclip")
	out, err := f.audit(t)
	if err != nil {
		t.Fatalf("audit: %v\n%s", err, out)
	}
	mustContain(t, out,
		"[PASS] Paperclip agent runtime harness .paperclip/harness.json declined by adoption.decline.",
		"[PASS] Caveman non-Markdown source coverage: register.sources declares no agent-facing text (reason: no agent-facing text outside Markdown).")
	check, err := runCavemanCLI(t, "", "check", "--configured-sources", "--root="+f.dir)
	if err != nil || !strings.Contains(check, "declares no agent-facing text (reason: no agent-facing text outside Markdown); nothing to check.") {
		t.Fatalf("configured-sources check: %v\n%s", err, check)
	}
}

// TestAuditDeclinedPaperclipNoSources_Negative (#601): an absent contract still fails closed and
// names the empty declaration; an empty contract over a harness on disk fails in both checks; a
// missing harness without the decline still fails.
func TestAuditDeclinedPaperclipNoSources_Negative(t *testing.T) {
	absent := declinedFixture(t, strings.TrimSuffix(fixtureManifest("acme", "widgets", false), fixtureRegisterSources()), "paperclip")
	removeFixturePath(t, absent, ".paperclip")
	_, err := absent.audit(t)
	mustErrContain(t, err, "audit requires register.sources; declare the repository's agent-facing text, or expected: 0 with a reason")

	kept := declinedFixture(t, fixtureNoSources(), "paperclip")
	_, err = kept.audit(t)
	mustErrContain(t, err, "declares no agent-facing text (reason: no agent-facing text outside Markdown), but .paperclip/harness.json exists")
	if _, err := runCavemanCLI(t, "", "check", "--configured-sources", "--root="+kept.dir); err == nil ||
		!strings.Contains(err.Error(), ".paperclip/harness.json exists") {
		t.Fatalf("configured-sources check over a kept harness: %v", err)
	}

	undeclined := declinedFixture(t, fixtureNoSources())
	removeFixturePath(t, undeclined, ".paperclip")
	_, err = undeclined.audit(t)
	mustErrContain(t, err, "Paperclip agent runtime harness .paperclip/harness.json is missing")
}

// TestAdoptDeclinedPaperclipKeepsNoSources_Boundary (#601): adopt --force keeps an empty
// contract byte for byte while paperclip stays declined and writes no harness; with paperclip no
// longer declined, adoption would write the harness, so it refuses the empty contract before
// writing anything.
func TestAdoptDeclinedPaperclipKeepsNoSources_Boundary(t *testing.T) {
	f := newForceAdoptFixture(t)
	manifest := fixtureNoSources() + declineDevContainer + "    - paperclip\n"
	writeFixtureFile(t, f.dir, ".standards.yaml", manifest)
	removeFixturePath(t, f, ".paperclip")
	if err := adoptFixture(t, f, true); err != nil {
		t.Fatalf("adopt --force: %v", err)
	}
	if got := readFixtureFile(t, f.dir, ".standards.yaml"); got != manifest {
		t.Fatalf("adopt --force changed the empty declaration:\n%s", got)
	}
	if _, err := os.Stat(filepath.Join(f.dir, ".paperclip", "harness.json")); !os.IsNotExist(err) {
		t.Fatalf("adopt wrote a harness for a declined paperclip step: %v", err)
	}
	undeclined := fixtureNoSources() + declineDevContainer
	writeFixtureFile(t, f.dir, ".standards.yaml", undeclined)
	err := adoptFixture(t, f, false)
	mustErrContain(t, err, "but this run writes .paperclip/harness.json")
	if got := readFixtureFile(t, f.dir, ".standards.yaml"); got != undeclined {
		t.Fatalf("the refused adoption rewrote the manifest:\n%s", got)
	}
}

// agentHarnessOperatorText is an operator-owned AGENTS.md without the register block.
const agentHarnessOperatorText = "# Widgets\n\n## Rules\n\n- Keep functions small.\n"

// TestAuditDeclinedAgentHarness_3D (#600): agent-harness is a retained decline. Negative: an
// operator AGENTS.md without the register block still fails, and the failure says the decline
// does not cover it and names what audit still requires. Positive: once compile-context rendered
// the block, the audit passes and names the decline. Boundary: without the decline the same
// failure carries no such note.
func TestAuditDeclinedAgentHarness_3D(t *testing.T) {
	f := declinedFixture(t, fixtureManifest("acme", "widgets", false), "agent-harness")
	writeFixtureFile(t, f.dir, "AGENTS.md", agentHarnessOperatorText)
	_, err := f.audit(t)
	mustErrContain(t, err, "(adoption.decline lists agent-harness, which does not cover this check: audit still requires the text register block in AGENTS.md")

	if out, err := runCompileContextCmd(t, f.dir); err != nil {
		t.Fatalf("compile-context: %v\n%s", err, out)
	}
	out, err := f.audit(t)
	if err != nil {
		t.Fatalf("audit after compile-context: %v\n%s", err, out)
	}
	mustContain(t, out, "[INFO] Agent harness declined by adoption.decline; audit still requires the text register block")

	undeclined := newAuditFixture(t)
	writeFixtureFile(t, undeclined.dir, "AGENTS.md", agentHarnessOperatorText)
	_, err = undeclined.audit(t)
	mustErrContain(t, err, "Agent context text register")
	if strings.Contains(err.Error(), "adoption.decline lists") {
		t.Fatalf("undeclined failure mentions a decline: %v", err)
	}
}
