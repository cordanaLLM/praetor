package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cordanaLLM/praetor/internal/compiler"
	"github.com/cordanaLLM/praetor/internal/config"
	"github.com/cordanaLLM/praetor/internal/hiss"
	"github.com/cordanaLLM/praetor/internal/mcp"
	"github.com/cordanaLLM/praetor/internal/util"
)

// ---- fixture ---------------------------------------------------------------------------

const fixtureAgentsMD = `# Fixture Repository

## Core Directives & Invariants

| Invariant | Scope | Enforcement Mechanism | Failure Action |
| :--- | :--- | :--- | :--- |
| **HISS-01** | Control Flow | Recursion strictly prohibited. | Build failure |
| **HISS-15** | 3D Testing | Positive, negative, and boundary tests mandatory. | CI coverage gate |

## Operational Rules

1. Act on verified state.
`

const fixtureMainGo = `package main

import "fmt"

// Greet returns a greeting for name.
func Greet(name string) string {
	if name == "" {
		return "hello"
	}
	return fmt.Sprintf("hello %s", name)
}

func main() {
	fmt.Println(Greet("praetor"))
}
`

// fixtureComplexGo has cyclomatic complexity 13 (> 10) and cognitive complexity 12.
const fixtureComplexGo = `package main

// Classify is a deliberately branchy fixture for the HISS-04 inspector.
func Classify(a, b, c, d int) string {
	if a > 0 && b > 0 && c > 0 {
		return "all"
	}
	if a > 0 || b > 0 {
		if c > 0 {
			return "ac"
		}
		if d > 0 {
			return "ad"
		}
	}
	switch {
	case a == 1:
		return "one"
	case a == 2:
		return "two"
	case a == 3:
		return "three"
	}
	for i := 0; i < d; i++ {
		if i%2 == 0 {
			continue
		}
	}
	return "none"
}
`

const fixturePickGo = `package main

// Pick exercises the else-if chain accounting.
func Pick(x int) int {
	if x < 0 {
		return -1
	} else if x == 0 {
		return 0
	} else {
		return 1
	}
}
`

func writeFixtureFile(t *testing.T, root, rel, content string) {
	t.Helper()
	full := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(full), 0o750); err != nil {
		t.Fatalf("mkdir %s: %v", full, err)
	}
	if err := os.WriteFile(full, []byte(content), 0o600); err != nil {
		t.Fatalf("write %s: %v", full, err)
	}
}

// initGitRepo turns dir into a git repository or skips the test when git is unavailable.
func initGitRepo(t *testing.T, dir string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if _, err := util.RunGit(ctx, dir, "init", "-q"); err != nil {
		t.Skipf("git unavailable: %v", err)
	}
}

// newFixtureRepo builds a governed repository in a temporary directory: manifest,
// lockfile, baseline, labels, ruleset, hook config, a clean Go module and AGENTS.md
// with its compiled vendor targets in sync.
func newFixtureRepo(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	initGitRepo(t, root)

	files := map[string]string{
		"AGENTS.md":                         fixtureAgentsMD,
		".standards.yaml":                   "version: 1\nrepository:\n  owner: \"fixture\"\n  name: \"repo\"\nprofiles:\n  - \"framework\"\nfacets: []\n",
		".standards.lock":                   validAuditLock(t),
		".config/archetypes/framework.yaml": auditLockSource,
		".standards-baseline.json":          `{"version":1,"generated_at":"2026-01-01T00:00:00Z","repository":"fixture/repo","commit_sha":"","total_infractions":0,"infractions":[]}` + "\n",
		".config/labels.yaml":               "labels: []\n",
		".github/rulesets/main.json":        "{}\n",
		"lefthook.yml":                      "pre-commit:\n  commands: {}\n",
		"go.mod":                            "module fixture\n\ngo 1.27\n",
		"main.go":                           fixtureMainGo,
		"complex.go":                        fixtureComplexGo,
	}
	for rel, content := range files {
		writeFixtureFile(t, root, rel, content)
	}

	if _, err := compiler.SyncRegisterBlock(context.Background(), root, filepath.Join(root, "AGENTS.md"), true); err != nil {
		t.Fatalf("splice fixture text register: %v", err)
	}
	tr := compiler.NewTranspiler()
	res, err := tr.Compile(filepath.Join(root, "AGENTS.md"))
	if err != nil {
		t.Fatalf("compile fixture AGENTS.md: %v", err)
	}
	if err := tr.WriteOutputs(res, root); err != nil {
		t.Fatalf("write fixture vendor targets: %v", err)
	}
	return root
}

// newFixtureServer returns a server confined to a fresh fixture repository.
func newFixtureServer(t *testing.T) (*Server, string) {
	t.Helper()
	root := newFixtureRepo(t)
	srv, err := NewServer(root, "v1.0.0-test")
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	return srv, root
}

// callTool invokes a registered tool through the JSON-RPC layer and returns its result.
func callTool(t *testing.T, srv *Server, name string, args map[string]any) *mcp.ToolResult {
	t.Helper()
	params, err := json.Marshal(map[string]any{"name": name, "arguments": args})
	if err != nil {
		t.Fatalf("marshal params: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	resp := srv.HandleRequest(ctx, JSONRPCRequest{JSONRPC: "2.0", ID: 1, Method: "tools/call", Params: params})
	if resp == nil {
		t.Fatalf("%s: nil response", name)
	}
	if resp.Error != nil {
		t.Fatalf("%s: JSON-RPC error %+v", name, resp.Error)
	}
	res, ok := resp.Result.(*mcp.ToolResult)
	if !ok || res == nil || len(res.Content) == 0 {
		t.Fatalf("%s: unexpected result %+v", name, resp.Result)
	}
	return res
}

// expectText asserts a successful tool result containing want.
func expectText(t *testing.T, name string, res *mcp.ToolResult, want string) {
	t.Helper()
	if res.IsError {
		t.Fatalf("%s: unexpected error result: %s", name, res.Content[0].Text)
	}
	if !strings.Contains(res.Content[0].Text, want) {
		t.Errorf("%s: output lacks %q:\n%s", name, want, res.Content[0].Text)
	}
}

// expectError asserts an error tool result containing want.
func expectError(t *testing.T, name string, res *mcp.ToolResult, want string) {
	t.Helper()
	if !res.IsError {
		t.Fatalf("%s: expected error result, got success:\n%s", name, res.Content[0].Text)
	}
	if !strings.Contains(res.Content[0].Text, want) {
		t.Errorf("%s: error lacks %q: %s", name, want, res.Content[0].Text)
	}
}

// ---- registration and protocol -------------------------------------------------------------

func TestServer_Positive_RegistrationAndAnnotations(t *testing.T) {
	srv, _ := newFixtureServer(t)

	ro := mcp.ToolAnnotations{ReadOnlyHint: true, DestructiveHint: false, IdempotentHint: true, OpenWorldHint: false}
	expected := map[string]mcp.ToolAnnotations{
		"standards_audit":                   ro,
		"standards_plan":                    ro,
		"standards_compile_context":         {ReadOnlyHint: false, DestructiveHint: true, IdempotentHint: true, OpenWorldHint: false},
		"standards_explain_rule":            ro,
		"standards_inspect_symbols":         ro,
		"standards_needs_report":            ro,
		"standards_adopt":                   {ReadOnlyHint: false, DestructiveHint: true, IdempotentHint: true, OpenWorldHint: false},
		"standards_dogfood":                 {ReadOnlyHint: false, DestructiveHint: false, IdempotentHint: false, OpenWorldHint: true},
		"standards_dogfood_suite":           {ReadOnlyHint: false, DestructiveHint: false, IdempotentHint: false, OpenWorldHint: true},
		"standards_dogfood_discover":        {ReadOnlyHint: false, DestructiveHint: false, IdempotentHint: false, OpenWorldHint: true},
		"standards_dogfood_schedule_status": ro,
		"standards_dogfood_repair_status":   ro,
		"standards_harvest_workstation":     {ReadOnlyHint: true, DestructiveHint: false, IdempotentHint: true, OpenWorldHint: true},
		"standards_package_docs":            ro,
		"standards_version_audit":           {ReadOnlyHint: true, DestructiveHint: false, IdempotentHint: true, OpenWorldHint: true},
		"standards_memory_recall":           ro,
		"standards_context_analyze":         ro,
		"standards_wishes_status":           ro,
		"standards_client_capabilities":     ro,
		"standards_wishes_update":           {ReadOnlyHint: false, DestructiveHint: true, IdempotentHint: false, OpenWorldHint: false},
		"standards_transcript_ingest":       {ReadOnlyHint: false, DestructiveHint: false, IdempotentHint: true, OpenWorldHint: false},
		"standards_notebook_prepare":        {ReadOnlyHint: true, DestructiveHint: false, IdempotentHint: true, OpenWorldHint: false},
		"standards_planning_validate":       ro,
		"standards_planning_prepare":        {ReadOnlyHint: false, DestructiveHint: false, IdempotentHint: false, OpenWorldHint: false},
		"standards_hindsight_optimize":      {ReadOnlyHint: false, DestructiveHint: true, IdempotentHint: true, OpenWorldHint: false},
	}
	if len(srv.tools) != len(expected) || len(srv.order) != len(expected) {
		t.Fatalf("registered %d tools (order %d), want %d", len(srv.tools), len(srv.order), len(expected))
	}
	for name, want := range expected {
		tool, exists := srv.tools[name]
		if !exists {
			t.Errorf("tool %q not registered", name)
			continue
		}
		if tool.Annotations != want {
			t.Errorf("tool %q annotations = %+v, want %+v", name, tool.Annotations, want)
		}
		if tool.Handler == nil {
			t.Errorf("tool %q registered without handler", name)
		}
	}

	listResp := srv.HandleRequest(context.Background(), JSONRPCRequest{JSONRPC: "2.0", ID: 3, Method: "tools/list"})
	if listResp == nil || listResp.Error != nil {
		t.Fatalf("tools/list failed: %+v", listResp)
	}
	listed, ok := listResp.Result.(map[string]any)["tools"].([]map[string]any)
	if !ok || len(listed) != len(expected) {
		t.Fatalf("tools/list returned %d tools, want %d", len(listed), len(expected))
	}
}

func TestServer_Positive_ProtocolMethods(t *testing.T) {
	srv, _ := newFixtureServer(t)
	ctx := context.Background()

	resp := srv.HandleRequest(ctx, JSONRPCRequest{JSONRPC: "2.0", ID: 1, Method: "initialize"})
	if resp == nil || resp.Error != nil {
		t.Fatalf("initialize failed: %+v", resp)
	}
	resMap, ok := resp.Result.(map[string]any)
	if !ok || resMap["protocolVersion"] != "2024-11-05" {
		t.Errorf("unexpected initialize result: %+v", resp.Result)
	}

	if notif := srv.HandleRequest(ctx, JSONRPCRequest{JSONRPC: "2.0", Method: "notifications/initialized"}); notif != nil {
		t.Errorf("notification should not produce response, got: %+v", notif)
	}

	ping := srv.HandleRequest(ctx, JSONRPCRequest{JSONRPC: "2.0", ID: 2, Method: "ping"})
	if ping == nil || ping.Error != nil || ping.ID != 2 {
		t.Errorf("ping failed: %+v", ping)
	}
}

func TestServer_Negative_JSONRPC(t *testing.T) {
	srv, _ := newFixtureServer(t)
	ctx := context.Background()

	unk := srv.HandleRequest(ctx, JSONRPCRequest{JSONRPC: "2.0", ID: 40, Method: "unknown_method"})
	if unk == nil || unk.Error == nil || unk.Error.Code != -32601 {
		t.Fatalf("expected method not found error, got: %+v", unk)
	}

	mal := srv.HandleRequest(ctx, JSONRPCRequest{JSONRPC: "2.0", ID: 41, Method: "tools/call", Params: []byte(`not-json`)})
	if mal == nil || mal.Error == nil || mal.Error.Code != -32602 {
		t.Fatalf("expected invalid params error, got: %+v", mal)
	}

	badToolP, err := json.Marshal(map[string]any{"name": "nonexistent_tool"})
	if err != nil {
		t.Fatal(err)
	}
	bad := srv.HandleRequest(ctx, JSONRPCRequest{JSONRPC: "2.0", ID: 42, Method: "tools/call", Params: badToolP})
	if bad == nil || bad.Error == nil || bad.Error.Code != -32601 {
		t.Fatalf("expected tool not found error (-32601), got: %+v", bad)
	}

	// A tool registered without a handler must fail with -32603, not panic.
	srv.tools["broken_tool"] = mcp.Tool{Name: "broken_tool", InputSchema: mcp.ToolInputSchema{Type: "object"}}
	brokenP, err := json.Marshal(map[string]any{"name": "broken_tool"})
	if err != nil {
		t.Fatal(err)
	}
	broken := srv.HandleRequest(ctx, JSONRPCRequest{JSONRPC: "2.0", ID: 43, Method: "tools/call", Params: brokenP})
	if broken == nil || broken.Error == nil || broken.Error.Code != -32603 {
		t.Fatalf("expected -32603 for nil handler, got: %+v", broken)
	}

	// Absent arguments reach the handler as an empty map.
	res := callTool(t, srv, "standards_explain_rule", nil)
	expectError(t, "explain_rule without args", res, "Unknown rule")
}

func TestServer_Boundary_ToolTimeoutAndArgTypes(t *testing.T) {
	root := newFixtureRepo(t)
	srv, err := NewServerWithOptions(ServerOptions{RootDir: root, Version: "v", ToolTimeout: 50 * time.Millisecond})
	if err != nil {
		t.Fatalf("NewServerWithOptions: %v", err)
	}
	slow, err := mcp.NewReadOnlyTool("slow_tool", "waits for its deadline", mcp.ToolInputSchema{}, func(ctx context.Context, _ map[string]any) (*mcp.ToolResult, error) {
		<-ctx.Done()
		return mcp.ErrorResult(ctx.Err().Error()), nil
	})
	if err != nil {
		t.Fatalf("slow tool: %v", err)
	}
	srv.tools[slow.Name] = slow

	start := time.Now()
	res := callTool(t, srv, "slow_tool", nil)
	if time.Since(start) > 5*time.Second {
		t.Fatalf("tool call was not bounded by the tool timeout")
	}
	expectError(t, "slow_tool", res, context.DeadlineExceeded.Error())

	// Boundary: booleans given as strings are rejected instead of being coerced to false.
	res = callTool(t, srv, "standards_compile_context", map[string]any{"verify_only": "true"})
	expectError(t, "compile_context verify_only string", res, "verify_only must be a boolean")
	if _, err := argBool(map[string]any{"x": nil}, "x", true); err != nil {
		t.Errorf("nil argument should fall back to the default: %v", err)
	}
	if _, err := argString(map[string]any{"p": 42}, "p"); !errors.Is(err, ErrArgType) {
		t.Errorf("argString on int: got %v, want ErrArgType", err)
	}
}

// ---- path confinement -------------------------------------------------------------------------

func TestServer_Boundary_ResolvePathConfinement(t *testing.T) {
	root := newFixtureRepo(t)
	parent := filepath.Dir(root)
	t.Chdir(parent)

	// A relative -root is resolved once; defaults are never joined onto it twice.
	srv, err := NewServer(filepath.Base(root), "v")
	if err != nil {
		t.Fatalf("NewServer relative root: %v", err)
	}
	resolvedRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatalf("eval root: %v", err)
	}
	got, err := srv.resolvePath(map[string]any{}, "path", srv.rootDir)
	if err != nil {
		t.Fatalf("default path: %v", err)
	}
	if gotResolved, evalErr := filepath.EvalSymlinks(got); evalErr != nil || gotResolved != resolvedRoot {
		t.Errorf("default path = %q (resolved %q), want root %q", got, gotResolved, resolvedRoot)
	}
	if got, err := srv.resolvePath(map[string]any{}, "config_path", ".standards.yaml"); err != nil || got != filepath.Join(srv.rootDir, ".standards.yaml") {
		t.Errorf("relative default = %q, %v", got, err)
	}

	// Inside the root: relative, absolute and interior "..".
	for _, in := range []string{"main.go", filepath.Join(root, "main.go"), filepath.Join("sub", "..", "main.go")} {
		if _, err := srv.resolvePath(map[string]any{"path": in}, "path", srv.rootDir); err != nil {
			t.Errorf("inside path %q rejected: %v", in, err)
		}
	}

	// Outside the root: lexical escape, absolute foreign path, symlink escape.
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(root, "escape")); err != nil {
		t.Skipf("symlinks unsupported: %v", err)
	}
	for _, in := range []string{"..", filepath.Join("..", "x"), outside, filepath.Join("escape", "f")} {
		if _, err := srv.resolvePath(map[string]any{"path": in}, "path", srv.rootDir); !errors.Is(err, ErrOutsideRoot) {
			t.Errorf("outside path %q: got %v, want ErrOutsideRoot", in, err)
		}
	}

	// The opt-in flag lifts the confinement.
	open, err := NewServerWithOptions(ServerOptions{RootDir: root, Version: "v", AllowOutsideRoot: true})
	if err != nil {
		t.Fatalf("open server: %v", err)
	}
	if got, err := open.resolvePath(map[string]any{"path": outside}, "path", open.rootDir); err != nil || got != filepath.Clean(outside) {
		t.Errorf("allow-outside-root absolute = %q, %v", got, err)
	}
	if got, err := open.resolvePath(map[string]any{"path": filepath.Join("escape", "f")}, "path", open.rootDir); err != nil || got == "" {
		t.Errorf("allow-outside-root symlink = %q, %v", got, err)
	}
}

func TestServer_Negative_NewServerRoot(t *testing.T) {
	if _, err := NewServer(filepath.Join(t.TempDir(), "missing"), "v"); err == nil {
		t.Error("expected error for a non-existent root")
	}
	file := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := NewServer(file, "v"); err == nil {
		t.Error("expected error for a file root")
	}
}

// ---- explain_rule ------------------------------------------------------------------------------

func TestServer_Positive_ExplainRuleCoversDocumentedInvariants(t *testing.T) {
	srv, _ := newFixtureServer(t)

	// Every invariant documented in AGENTS.md must resolve, including HISS-17 through HISS-21.
	for _, id := range []string{
		"HISS-01", "HISS-02", "HISS-03", "HISS-04", "HISS-05", "HISS-06", "HISS-07",
		"HISS-08", "HISS-09", "HISS-10", "HISS-11", "HISS-12", "HISS-13", "HISS-14",
		"HISS-15", "HISS-16", "HISS-17", "HISS-18", "HISS-19", "HISS-20", "HISS-21",
	} {
		res := callTool(t, srv, "standards_explain_rule", map[string]any{"rule_id": id})
		expectText(t, id, res, "Rule: "+id)
	}
	for _, id := range knownRuleIDs() {
		res := callTool(t, srv, "standards_explain_rule", map[string]any{"rule_id": " " + strings.ToLower(id) + "\n"})
		expectText(t, "normalised "+id, res, "Rule: "+id)
	}

	schema := srv.tools["standards_explain_rule"].InputSchema.Properties["rule_id"]
	if len(schema.Enum) != len(hissRuleExplanations) {
		t.Errorf("schema enum lists %d rules, map has %d", len(schema.Enum), len(hissRuleExplanations))
	}

	res := callTool(t, srv, "standards_explain_rule", map[string]any{"rule_id": "UNKNOWN-99"})
	expectError(t, "unknown rule", res, "Unknown rule")
	for _, id := range knownRuleIDs() {
		if !strings.Contains(res.Content[0].Text, id) {
			t.Errorf("error message does not list %s", id)
		}
	}
	res = callTool(t, srv, "standards_explain_rule", map[string]any{"rule_id": 7})
	expectError(t, "typed rule id", res, "must be a string")
}

// ---- plan and audit ------------------------------------------------------------------------------

func TestServer_Positive_PlanAndAuditOnSyncedRepo(t *testing.T) {
	srv, root := newFixtureServer(t)

	plan := callTool(t, srv, "standards_plan", nil)
	expectText(t, "plan", plan, "No changes required")
	expectText(t, "plan default effective reviews", plan, "Approving Reviewers:       1")
	expectText(t, "plan default configured reviews", plan, "Configured Reviewer Minimum: 1")
	expectText(t, "plan default review mode", plan, "Review Mode:               independent")

	writeFixtureFile(t, root, ".standards.yaml", "version: 1\nrepository:\n  owner: fixture\n  name: repo\nprofiles: [framework]\nfacets: []\noverrides:\n  branch_protection:\n    review_mode: single_maintainer\n")
	plan = callTool(t, srv, "standards_plan", nil)
	expectText(t, "plan single-maintainer effective reviews", plan, "Approving Reviewers:       0")
	expectText(t, "plan single-maintainer configured reviews", plan, "Configured Reviewer Minimum: 1")
	expectText(t, "plan single-maintainer review mode", plan, "Review Mode:               single_maintainer")

	audit := callTool(t, srv, "standards_audit", nil)
	expectText(t, "audit", audit, "[PASS] Technical debt baseline verified")
	expectText(t, "audit", audit, "[PASS] Cross-agent context targets verified in sync")
	expectText(t, "audit", audit, "7/7 MCP audit gates passed")
}

// The heading names the tool, never a repository; the manifest identity follows it (#361).
func TestWritePlanHeader_NamesTheManifestRepository(t *testing.T) {
	for _, repo := range []config.RepositoryMetadata{{Owner: "golusoris", Name: "golusoris"}, {}} {
		var b strings.Builder
		if err := writePlanHeader(&b, &config.Manifest{Repository: repo}, config.DefaultPolicy()); err != nil {
			t.Fatal(err)
		}
		lines := strings.SplitN(b.String(), "\n", 3)
		if lines[0] != "=== Praetor Reconcile Plan (Dry Run) ===" || lines[1] != "Repository: "+repo.Owner+"/"+repo.Name {
			t.Errorf("header for %+v = %q", repo, lines[:2])
		}
		if strings.Contains(b.String(), "cordanaLLM/praetor") {
			t.Errorf("header names this product's repository:\n%s", b.String())
		}
	}
}

func TestWritePlanHeaderRejectsInvalidReviewMode(t *testing.T) {
	policy := config.DefaultPolicy()
	policy.BranchProtection.ReviewMode = "unreviewed"
	var b strings.Builder
	if err := writePlanHeader(&b, &config.Manifest{}, policy); err == nil || !strings.Contains(err.Error(), "unsupported branch protection review mode") {
		t.Fatalf("invalid review mode was not propagated: %v", err)
	}
}

func TestServer_Negative_PlanDriftAndAuditFailures(t *testing.T) {
	srv, root := newFixtureServer(t)

	if err := os.Remove(filepath.Join(root, ".github", "rulesets", "main.json")); err != nil {
		t.Fatal(err)
	}
	plan := callTool(t, srv, "standards_plan", nil)
	expectText(t, "plan drift", plan, "[DRIFT] Policy drift detected")
	if strings.Contains(plan.Content[0].Text, "No changes required") {
		t.Error("drifted plan still claims no changes required")
	}
	audit := callTool(t, srv, "standards_audit", nil)
	expectError(t, "audit ruleset", audit, "[FAIL] Branch protection ruleset")
	writeFixtureFile(t, root, ".github/rulesets/main.json", "{}\n")

	// A new HISS-02 violation must trip the ratchet even though the baseline is empty.
	writeFixtureFile(t, root, "bad.go", "package main\n\nfunc spin() {\n\tfor {\n\t}\n}\n")
	audit = callTool(t, srv, "standards_audit", nil)
	expectError(t, "audit ratchet", audit, "[FAIL] HISS invariant violations introduced")
	expectError(t, "audit ratchet", audit, "bad.go")
	if err := os.Remove(filepath.Join(root, "bad.go")); err != nil {
		t.Fatal(err)
	}

	if err := os.Remove(filepath.Join(root, ".standards.lock")); err != nil {
		t.Fatal(err)
	}
	audit = callTool(t, srv, "standards_audit", nil)
	expectError(t, "audit lock", audit, "[FAIL] Effective policy audit failed")
	plan = callTool(t, srv, "standards_plan", nil)
	expectText(t, "plan missing", plan, "[DRIFT] Missing baseline files: .standards.lock")
	writeFixtureFile(t, root, ".standards.lock", validAuditLock(t))

	bad := callTool(t, srv, "standards_audit", map[string]any{"config_path": "nonexistent.yaml"})
	expectError(t, "audit manifest", bad, "[FAIL] Effective policy audit failed")
	escaped := callTool(t, srv, "standards_audit", map[string]any{"config_path": "../outside.yaml"})
	expectError(t, "audit escape", escaped, "outside the server root")
}

func TestServer_Boundary_AuditLockfileIsDirectory(t *testing.T) {
	srv, root := newFixtureServer(t)
	if err := os.Remove(filepath.Join(root, ".standards.lock")); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, ".standards.lock"), 0o750); err != nil {
		t.Fatal(err)
	}
	audit := callTool(t, srv, "standards_audit", nil)
	expectError(t, "audit lock dir", audit, "source must be regular")
}

// ---- inspect_symbols ---------------------------------------------------------------------------

// fixtureManifestWithComplexity is the fixture manifest with a repository complexity override.
func fixtureManifestWithComplexity(complexity string) string {
	return "version: 1\nrepository:\n  owner: \"fixture\"\n  name: \"repo\"\nprofiles:\n  - \"framework\"\nfacets: []\n" +
		"overrides:\n  complexity:\n" + complexity
}

func TestServer_Positive_InspectSymbolsMeasuresComplexity(t *testing.T) {
	srv, root := newFixtureServer(t)
	writeFixtureFile(t, root, ".standards.yaml", fixtureManifestWithComplexity(
		"    max_cyclomatic: 10\n    max_cognitive: 15\n    max_func_loc: 75\n    max_statements: 50\n"))

	res := callTool(t, srv, "standards_inspect_symbols", map[string]any{"path": "."})
	expectText(t, "inspect dir", res, "Func: Greet")
	expectText(t, "inspect dir", res, "Func: Classify")
	expectText(t, "inspect dir", res, "Cyclo: 13 (<=10)")
	expectText(t, "inspect dir", res, "HISS-04 WARN: Cyclo")

	// The manifest asks for 75 lines; the audit caps it at its own length, and so does this.
	single := callTool(t, srv, "standards_inspect_symbols", map[string]any{"path": "main.go"})
	expectText(t, "inspect file", single, fmt.Sprintf(
		"Func: Greet | LOC: 6 (<=%d) | Stmts: 3 (<=50) | Cyclo: 2 (<=10) | Cognitive: 1 (<=15) [PASS]", config.AuditMaxFuncLOC))
	if strings.Contains(single.Content[0].Text, "WARN") {
		t.Errorf("clean file reported a warning:\n%s", single.Content[0].Text)
	}
}

// The verdict follows the inspected repository's resolved policy, not a literal ceiling (#360):
// the same function passes under a looser policy and fails under a tighter one.
func TestServer_Boundary_InspectSymbolsFollowsRepositoryPolicy(t *testing.T) {
	srv, root := newFixtureServer(t)
	loose := callTool(t, srv, "standards_inspect_symbols", map[string]any{"path": "complex.go"})
	expectText(t, "resolved defaults", loose, "Cyclo: 13 (<=15)")
	if strings.Contains(loose.Content[0].Text, "WARN") {
		t.Errorf("the resolved default policy admits Classify:\n%s", loose.Content[0].Text)
	}

	writeFixtureFile(t, root, ".standards.yaml", fixtureManifestWithComplexity("    max_cyclomatic: 13\n    max_func_loc: 26\n"))
	tight := callTool(t, srv, "standards_inspect_symbols", map[string]any{"path": "complex.go"})
	expectText(t, "tight policy", tight, "LOC: 27 (<=26)")
	expectText(t, "tight policy", tight, "Cyclo: 13 (<=13)")
	expectText(t, "tight policy", tight, "HISS-04 WARN: LOC]")
}

// A policy that exists but does not resolve reports against the HISS-04 ceiling, tightened by
// any readable override, and says so; only a resolution the caller interrupted fails the tool.
func TestServer_Negative_InspectSymbolsUnresolvablePolicy(t *testing.T) {
	srv, root := newFixtureServer(t)
	// The lock `praetorctl init` writes pins nothing and carries no digest.
	writeFixtureFile(t, root, ".standards.lock", "# SemVer lockfile\nversion: 1\npinned_version: \"v0.0.0\"\n")
	writeFixtureFile(t, root, ".standards.yaml", fixtureManifestWithComplexity("    max_func_loc: 26\n"))
	initLocked := callTool(t, srv, "standards_inspect_symbols", map[string]any{"path": "complex.go"})
	expectText(t, "init lock", initLocked, "[WARN] repository policy unresolved")
	expectText(t, "init lock", initLocked, "LOC: 27 (<=26)")
	expectText(t, "init lock", initLocked, "Cyclo: 13 (<=10)")

	writeFixtureFile(t, root, ".standards.yaml", "version: [\n")
	corrupt := callTool(t, srv, "standards_inspect_symbols", map[string]any{"path": "main.go"})
	expectText(t, "corrupt manifest", corrupt, "[WARN] repository policy unresolved")
	expectText(t, "corrupt manifest", corrupt, fmt.Sprintf("(<=%d)", config.AuditMaxFuncLOC))

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := srv.inspectSymbolsAtPath(ctx, filepath.Join(root, "main.go")); err == nil ||
		!strings.Contains(err.Error(), "resolve complexity policy") {
		t.Errorf("interrupted resolution = %v, want an error", err)
	}
}

// Both a locked repository without a length of its own and an unadopted workspace are judged
// against the scanner's own default, hiss.DefaultMaxFuncLOC (BUG-309).
func TestServer_Boundary_InspectSymbolsLengthIsScannerDefault(t *testing.T) {
	srv, root := newFixtureServer(t)
	want := fmt.Sprintf("Func: Greet | LOC: 6 (<=%d)", hiss.DefaultMaxFuncLOC)
	locked := callTool(t, srv, "standards_inspect_symbols", map[string]any{"path": "main.go"})
	expectText(t, "locked defaults", locked, want)

	if err := os.Remove(filepath.Join(root, ".standards.yaml")); err != nil {
		t.Fatal(err)
	}
	unadopted := callTool(t, srv, "standards_inspect_symbols", map[string]any{"path": "main.go"})
	expectText(t, "no manifest", unadopted, want)
	if strings.Contains(unadopted.Content[0].Text, "WARN") {
		t.Errorf("an unadopted workspace is not an unresolved policy:\n%s", unadopted.Content[0].Text)
	}
}

func TestServer_Negative_InspectSymbols(t *testing.T) {
	srv, root := newFixtureServer(t)

	res := callTool(t, srv, "standards_inspect_symbols", map[string]any{"path": "does-not-exist.go"})
	expectError(t, "missing path", res, "Inspection failed")
	res = callTool(t, srv, "standards_inspect_symbols", map[string]any{"path": ""})
	expectError(t, "empty path", res, "path parameter is required")
	res = callTool(t, srv, "standards_inspect_symbols", map[string]any{"path": "../"})
	expectError(t, "escaping path", res, "outside the server root")

	writeFixtureFile(t, root, "broken/broken.go", "package main\n\nfunc (")
	res = callTool(t, srv, "standards_inspect_symbols", map[string]any{"path": "broken"})
	expectText(t, "parse error", res, "Parse error")

	if err := os.Mkdir(filepath.Join(root, "empty"), 0o750); err != nil {
		t.Fatal(err)
	}
	res = callTool(t, srv, "standards_inspect_symbols", map[string]any{"path": "empty"})
	expectText(t, "no go files", res, "No Go source files")
}

func TestServer_Boundary_MeasureFuncMetrics(t *testing.T) {
	cases := []struct {
		name string
		src  string
		fn   string
		want funcMetrics
	}{
		{"simple", fixtureMainGo, "Greet", funcMetrics{LOC: 6, Statements: 3, Cyclomatic: 2, Cognitive: 1}},
		{"branchy", fixtureComplexGo, "Classify", funcMetrics{LOC: 27, Statements: 20, Cyclomatic: 13, Cognitive: 12}},
		{"else-if chain", fixturePickGo, "Pick", funcMetrics{LOC: 9, Statements: 5, Cyclomatic: 3, Cognitive: 3}},
		{"empty body", "package p\n\nfunc Nop() {}\n", "Nop", funcMetrics{LOC: 1, Statements: 0, Cyclomatic: 1, Cognitive: 0}},
	}
	for _, tc := range cases {
		fset := token.NewFileSet()
		file, err := parser.ParseFile(fset, tc.name+".go", tc.src, 0)
		if err != nil {
			t.Fatalf("%s: parse: %v", tc.name, err)
		}
		fn := findFunc(file, tc.fn)
		if fn == nil {
			t.Fatalf("%s: function %s not found", tc.name, tc.fn)
		}
		if got := measureFunc(fset, fn); got != tc.want {
			t.Errorf("%s: metrics = %+v, want %+v", tc.name, got, tc.want)
		}
	}

	// Boundary: exactly at the caps is a pass; one over each cap names the bound.
	bounds := config.HISSComplexityCeiling()
	at := funcMetrics{LOC: bounds.MaxFuncLOC, Statements: bounds.MaxStatements, Cyclomatic: bounds.MaxCyclomatic, Cognitive: bounds.MaxCognitive}
	if v := at.violations(bounds); len(v) != 0 {
		t.Errorf("metrics at the caps reported %v", v)
	}
	over := funcMetrics{LOC: bounds.MaxFuncLOC + 1, Statements: bounds.MaxStatements + 1, Cyclomatic: bounds.MaxCyclomatic + 1, Cognitive: bounds.MaxCognitive + 1}
	if v := strings.Join(over.violations(bounds), ","); v != "LOC,Stmts,Cyclo,Cognitive" {
		t.Errorf("violations over every cap = %q", v)
	}
}

// findFunc returns the named top-level function declaration of a parsed file.
func findFunc(file *ast.File, name string) *ast.FuncDecl {
	for _, decl := range file.Decls {
		if fn, ok := decl.(*ast.FuncDecl); ok && fn.Name.Name == name {
			return fn
		}
	}
	return nil
}
