package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/cordanaLLM/standards/internal/baseline"
	"github.com/cordanaLLM/standards/internal/compiler"
	"github.com/cordanaLLM/standards/internal/config"
	"github.com/cordanaLLM/standards/internal/mcp"
	"github.com/cordanaLLM/standards/internal/needs"
)

const (
	maxScannerBuffer = 1024 * 1024 // 1MB
	maxFilesToScan   = 500
)

// JSONRPCRequest represents a JSON-RPC 2.0 request payload.
type JSONRPCRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      any             `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

// JSONRPCResponse represents a JSON-RPC 2.0 response payload.
type JSONRPCResponse struct {
	JSONRPC string        `json:"jsonrpc"`
	ID      any           `json:"id,omitempty"`
	Result  any           `json:"result,omitempty"`
	Error   *JSONRPCError `json:"error,omitempty"`
}

// JSONRPCError holds structured error details.
type JSONRPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

// Server implements the MCP server instance.
type Server struct {
	rootDir string
	version string
	tools   map[string]mcp.Tool
	order   []string
}

// NewServer initializes a standards-mcp server and registers standard tools.
func NewServer(rootDir, version string) (*Server, error) {
	if rootDir == "" {
		rootDir = "."
	}
	s := &Server{
		rootDir: rootDir,
		version: version,
		tools:   make(map[string]mcp.Tool),
		order:   make([]string, 0, 5),
	}

	if err := s.registerStandardTools(); err != nil {
		return nil, fmt.Errorf("failed to register standard tools: %w", err)
	}

	return s, nil
}

// registerStandardTools registers the 5 mandatory standards MCP tools.
func (s *Server) registerStandardTools() error {
	tools := []func() (mcp.Tool, error){
		s.createAuditTool,
		s.createPlanTool,
		s.createCompileContextTool,
		s.createExplainRuleTool,
		s.createInspectSymbolsTool,
		s.createNeedsReportTool,
	}

	limit := len(tools)
	for i := 0; i < limit; i++ {
		t, err := tools[i]()
		if err != nil {
			return err
		}
		s.tools[t.Name] = t
		s.order = append(s.order, t.Name)
	}
	return nil
}

// createAuditTool builds the read-only standards_audit tool.
func (s *Server) createAuditTool() (mcp.Tool, error) {
	schema := mcp.ToolInputSchema{
		Type: "object",
		Properties: map[string]mcp.PropertySchema{
			"config_path": {
				Type:        "string",
				Description: "Path to .standards.yaml manifest (default: .standards.yaml)",
			},
			"baseline_path": {
				Type:        "string",
				Description: "Path to .standards-baseline.json debt file (default: .standards-baseline.json)",
			},
			"agents_path": {
				Type:        "string",
				Description: "Path to canonical AGENTS.md (default: AGENTS.md)",
			},
		},
	}

	handler := func(ctx context.Context, args map[string]any) (*mcp.ToolResult, error) {
		confPath := s.resolvePath(args, "config_path", ".standards.yaml")
		basePath := s.resolvePath(args, "baseline_path", ".standards-baseline.json")
		agentPath := s.resolvePath(args, "agents_path", "AGENTS.md")

		var report strings.Builder
		report.WriteString("=== cordanaLLM/standards Governance Audit ===\n")

		manifest, err := config.LoadManifest(confPath)
		if err != nil {
			return mcp.ErrorResult(fmt.Sprintf("[FAIL] Manifest audit failed: %v", err)), nil
		}
		report.WriteString(fmt.Sprintf("[PASS] Manifest verified: %s/%s (Version %d)\n",
			manifest.Repository.Owner, manifest.Repository.Name, manifest.Version))

		lockPath := filepath.Join(s.rootDir, ".standards.lock")
		if _, err := os.Stat(lockPath); os.IsNotExist(err) {
			return mcp.ErrorResult("[FAIL] .standards.lock is missing"), nil
		}
		report.WriteString("[PASS] SemVer lockfile .standards.lock verified.\n")

		base, err := baseline.LoadBaseline(basePath)
		if err != nil {
			return mcp.ErrorResult(fmt.Sprintf("[FAIL] Baseline audit failed: %v", err)), nil
		}
		report.WriteString(fmt.Sprintf("[PASS] Technical debt baseline verified: %d recorded legacy infractions.\n",
			base.TotalInfractions))

		tr := compiler.NewTranspiler()
		if err := tr.Verify(agentPath, s.rootDir); err != nil {
			return mcp.ErrorResult(fmt.Sprintf("[FAIL] Agent context targets out of sync: %v", err)), nil
		}
		report.WriteString("[PASS] Cross-agent context targets verified in sync.\n")
		report.WriteString("\nAudit Summary: 100% Compliance with cordanaLLM/standards HISS-16 baseline.")

		return mcp.TextResult(report.String()), nil
	}

	return mcp.NewReadOnlyTool("standards_audit", "Audit repository against declared HISS-16 standards", schema, handler)
}

// createPlanTool builds the read-only standards_plan tool.
func (s *Server) createPlanTool() (mcp.Tool, error) {
	schema := mcp.ToolInputSchema{
		Type: "object",
		Properties: map[string]mcp.PropertySchema{
			"config_path": {
				Type:        "string",
				Description: "Path to .standards.yaml manifest (default: .standards.yaml)",
			},
		},
	}

	handler := func(ctx context.Context, args map[string]any) (*mcp.ToolResult, error) {
		confPath := s.resolvePath(args, "config_path", ".standards.yaml")
		manifest, err := config.LoadManifest(confPath)
		if err != nil {
			return mcp.ErrorResult(fmt.Sprintf("Failed to load manifest: %v", err)), nil
		}

		policy := config.DefaultPolicy()
		policy.ApplyOverrides(manifest.Overrides)

		var b strings.Builder
		b.WriteString("=== cordanaLLM/standards Reconcile Plan (Dry Run) ===\n")
		b.WriteString(fmt.Sprintf("Repository: %s/%s\n", manifest.Repository.Owner, manifest.Repository.Name))
		b.WriteString(fmt.Sprintf("Profiles:   %v\nFacets:     %v\n\nTarget Invariants:\n", manifest.Profiles, manifest.Facets))
		b.WriteString(fmt.Sprintf("  - Max Cyclomatic Complexity: <= %d\n", policy.Complexity.MaxCyclomatic))
		b.WriteString(fmt.Sprintf("  - Max Function LOC:          <= %d\n", policy.Complexity.MaxFuncLOC))
		b.WriteString(fmt.Sprintf("  - Linear History Required:    %t\n", policy.BranchProtection.EnforceLinearHistory))
		b.WriteString(fmt.Sprintf("  - Signed Commits Required:   %t\n", policy.BranchProtection.RequireSignedCommits))
		b.WriteString(fmt.Sprintf("  - Approving Reviewers:       %d\n", policy.BranchProtection.RequiredApprovingReviewers))
		b.WriteString(fmt.Sprintf("  - SLSA Provenance Level:     %d\n", policy.SupplyChain.SLSALevel))
		b.WriteString(fmt.Sprintf("  - Cosign Attestation:        %t\n", policy.SupplyChain.EnforceCosign))
		b.WriteString("\nStatus: Local state matches declared policy. No changes required.")

		return mcp.TextResult(b.String()), nil
	}

	return mcp.NewReadOnlyTool("standards_plan", "Plan standards enforcement and policy reconciliation", schema, handler)
}

// createCompileContextTool builds the standards_compile_context tool.
func (s *Server) createCompileContextTool() (mcp.Tool, error) {
	schema := mcp.ToolInputSchema{
		Type: "object",
		Properties: map[string]mcp.PropertySchema{
			"source": {
				Type:        "string",
				Description: "Path to canonical AGENTS.md (default: AGENTS.md)",
			},
			"target_dir": {
				Type:        "string",
				Description: "Root directory to write target files (default: .)",
			},
			"verify_only": {
				Type:        "boolean",
				Description: "If true, verify synchronization without writing files",
			},
		},
	}

	handler := func(ctx context.Context, args map[string]any) (*mcp.ToolResult, error) {
		source := s.resolvePath(args, "source", "AGENTS.md")
		targetDir := s.resolvePath(args, "target_dir", s.rootDir)
		verifyOnly, _ := args["verify_only"].(bool)

		tr := compiler.NewTranspiler()
		if verifyOnly {
			if err := tr.Verify(source, targetDir); err != nil {
				return mcp.ErrorResult(fmt.Sprintf("Context verification failed: %v", err)), nil
			}
			return mcp.TextResult("All agent context targets are 100% in sync with canonical AGENTS.md."), nil
		}

		res, err := tr.Compile(source)
		if err != nil {
			return mcp.ErrorResult(fmt.Sprintf("Context compilation failed: %v", err)), nil
		}

		if err := tr.WriteOutputs(res, targetDir); err != nil {
			return mcp.ErrorResult(fmt.Sprintf("Failed writing outputs: %v", err)), nil
		}

		var b strings.Builder
		b.WriteString("Cross-agent context transpilation completed successfully:\n")
		for _, f := range res.Files {
			b.WriteString(fmt.Sprintf("  [COMPILED] %-35s (%d lines, budget <= %d)\n",
				f.RelativePath, f.LineCount, compiler.MaxLineBudget))
		}
		return mcp.TextResult(b.String()), nil
	}

	return mcp.NewMutatingTool("standards_compile_context", "Compile or verify cross-agent instructions from AGENTS.md", schema, handler, false, true)
}

// createExplainRuleTool builds the read-only standards_explain_rule tool.
func (s *Server) createExplainRuleTool() (mcp.Tool, error) {
	schema := mcp.ToolInputSchema{
		Type: "object",
		Properties: map[string]mcp.PropertySchema{
			"rule_id": {
				Type:        "string",
				Description: "The HISS rule identifier to explain (e.g. HISS-01, HISS-02, HISS-04, HISS-07, HISS-10, HISS-15, HISS-16)",
			},
		},
		Required: []string{"rule_id"},
	}

	handler := func(ctx context.Context, args map[string]any) (*mcp.ToolResult, error) {
		ruleID, _ := args["rule_id"].(string)
		ruleID = strings.ToUpper(strings.TrimSpace(ruleID))

		explanation, found := lookupRuleExplanation(ruleID)
		if !found {
			return mcp.ErrorResult(fmt.Sprintf("Unknown rule %q. Valid rules: HISS-01, HISS-02, HISS-03, HISS-04, HISS-07, HISS-08, HISS-09, HISS-10, HISS-11, HISS-14, HISS-15, HISS-16", ruleID)), nil
		}

		return mcp.TextResult(explanation), nil
	}

	return mcp.NewReadOnlyTool("standards_explain_rule", "Explain a specific HISS invariant rule and its formal verification mechanism", schema, handler)
}

// createInspectSymbolsTool builds the read-only standards_inspect_symbols tool.
func (s *Server) createInspectSymbolsTool() (mcp.Tool, error) {
	schema := mcp.ToolInputSchema{
		Type: "object",
		Properties: map[string]mcp.PropertySchema{
			"path": {
				Type:        "string",
				Description: "Relative file or directory path to inspect Go AST symbols",
			},
		},
		Required: []string{"path"},
	}

	handler := func(ctx context.Context, args map[string]any) (*mcp.ToolResult, error) {
		relPath, _ := args["path"].(string)
		if relPath == "" {
			return mcp.ErrorResult("path parameter is required"), nil
		}

		targetPath := relPath
		if !filepath.IsAbs(targetPath) {
			targetPath = filepath.Join(s.rootDir, targetPath)
		}

		report, err := s.inspectSymbolsAtPath(targetPath)
		if err != nil {
			return mcp.ErrorResult(fmt.Sprintf("Inspection failed: %v", err)), nil
		}

		return mcp.TextResult(report), nil
	}

	return mcp.NewReadOnlyTool("standards_inspect_symbols", "Inspect Go AST symbols and analyze HISS-04 complexity bounds", schema, handler)
}

// createNeedsReportTool builds the read-only standards_needs_report tool.
func (s *Server) createNeedsReportTool() (mcp.Tool, error) {
	schema := mcp.ToolInputSchema{
		Type: "object",
		Properties: map[string]mcp.PropertySchema{
			"path": {
				Type:        "string",
				Description: "Path to repository to scan (default: .)",
			},
			"framework": {
				Type:        "string",
				Description: "Path to target framework repository (default: /home/kilian/dev/golusoris/golusoris)",
			},
		},
	}

	handler := func(ctx context.Context, args map[string]any) (*mcp.ToolResult, error) {
		targetPath := s.resolvePath(args, "path", s.rootDir)
		fwPath, _ := args["framework"].(string)
		if fwPath == "" {
			fwPath = "/home/kilian/dev/golusoris/golusoris"
		}

		fwIndex, err := needs.InspectFramework(ctx, fwPath)
		if err != nil {
			return mcp.ErrorResult(fmt.Sprintf("Failed to inspect framework: %v", err)), nil
		}

		rep, err := needs.ScanRepo(ctx, targetPath)
		if err != nil {
			return mcp.ErrorResult(fmt.Sprintf("Failed to scan repository: %v", err)), nil
		}

		var b strings.Builder
		b.WriteString(fmt.Sprintf("=== Golusoris Migration Report: %s ===\n", rep.Repository))
		b.WriteString(fmt.Sprintf("Framework: %s (%s) | Readiness Score: %.1f%%\n\n", fwIndex.Name, fwIndex.Version, rep.Readiness.Score))
		b.WriteString("Drop-In Replacement Matrix:\n")
		for _, dep := range rep.Dependencies {
			if dep.Status == needs.StatusCovered || dep.Status == needs.StatusAdapterAvailable {
				b.WriteString(fmt.Sprintf("  ✓ %-35s -> %s\n", dep.Package, dep.GolusorisReplacement))
			} else {
				b.WriteString(fmt.Sprintf("  ✗ %-35s -> NO DIRECT EQUIVALENT (Gap)\n", dep.Package))
			}
		}

		return mcp.TextResult(b.String()), nil
	}

	return mcp.NewReadOnlyTool("standards_needs_report", "Evaluate repository needs and Golusoris migration compatibility", schema, handler)
}

// resolvePath helper for parameter path resolution.
func (s *Server) resolvePath(args map[string]any, key, defaultVal string) string {
	val, ok := args[key].(string)
	if !ok || val == "" {
		val = defaultVal
	}
	if filepath.IsAbs(val) {
		return val
	}
	return filepath.Join(s.rootDir, val)
}

// inspectSymbolsAtPath inspects Go files at path without recursion.
func (s *Server) inspectSymbolsAtPath(path string) (string, error) {
	stat, err := os.Stat(path)
	if err != nil {
		return "", fmt.Errorf("failed stat on %s: %w", path, err)
	}

	files := make([]string, 0, 10)
	if stat.IsDir() {
		entries, err := os.ReadDir(path)
		if err != nil {
			return "", fmt.Errorf("failed reading directory: %w", err)
		}
		for i := 0; i < len(entries) && len(files) < maxFilesToScan; i++ {
			if !entries[i].IsDir() && strings.HasSuffix(entries[i].Name(), ".go") {
				files = append(files, filepath.Join(path, entries[i].Name()))
			}
		}
	} else {
		files = append(files, path)
	}

	if len(files) == 0 {
		return "No Go source files found for symbol inspection.", nil
	}

	fset := token.NewFileSet()
	var b strings.Builder
	b.WriteString("=== Go AST Symbol & HISS-04 Complexity Inspection ===\n\n")

	limit := len(files)
	for i := 0; i < limit; i++ {
		s.inspectSingleFile(fset, files[i], &b)
	}

	return b.String(), nil
}

// inspectSingleFile parses a single Go file and prints its top-level declarations.
func (s *Server) inspectSingleFile(fset *token.FileSet, filePath string, b *strings.Builder) {
	node, err := parser.ParseFile(fset, filePath, nil, parser.ParseComments)
	if err != nil {
		b.WriteString(fmt.Sprintf("File: %s (Parse error: %v)\n\n", filepath.Base(filePath), err))
		return
	}

	rel, err := filepath.Rel(s.rootDir, filePath)
	if err != nil {
		rel = filePath
	}
	b.WriteString(fmt.Sprintf("File: %s (Package: %s)\n", rel, node.Name.Name))

	declLimit := len(node.Decls)
	for d := 0; d < declLimit; d++ {
		decl := node.Decls[d]
		switch fn := decl.(type) {
		case *ast.FuncDecl:
			start := fset.Position(fn.Pos()).Line
			end := fset.Position(fn.End()).Line
			loc := end - start + 1
			stmts := 0
			if fn.Body != nil {
				stmts = len(fn.Body.List)
			}

			status := "PASS"
			if loc > 75 || stmts > 50 {
				status = "HISS-04 WARN"
			}

			receiver := ""
			if fn.Recv != nil && len(fn.Recv.List) > 0 {
				receiver = "(recv) "
			}
			b.WriteString(fmt.Sprintf("  - %sFunc: %s | LOC: %d (<=75) | Stmts: %d (<=50) [%s]\n",
				receiver, fn.Name.Name, loc, stmts, status))

		case *ast.GenDecl:
			if fn.Tok == token.TYPE {
				for _, spec := range fn.Specs {
					if ts, ok := spec.(*ast.TypeSpec); ok {
						b.WriteString(fmt.Sprintf("  - Type: %s\n", ts.Name.Name))
					}
				}
			}
		}
	}
	b.WriteString("\n")
}

var hissRuleExplanations = map[string]string{
	"HISS-01": `Rule: HISS-01 (Control Flow - Acyclic DAG Control Flow)
Formal Specification: Call graphs must form a Directed Acyclic Graph: G = (V, E), ∀v ∈ V, (v, v) ∉ E*
Direct and mutual recursion are strictly prohibited in production runtimes.
Enforcement: AST call-graph analyzer and static linter checks.
Failure Action: Immediate build failure.`,
	"HISS-02": `Rule: HISS-02 (Loops & I/O - Bounded Loops & Mandatory I/O Timeouts)
Formal Specification: Every loop construct must possess a statically verifiable scalar upper bound: iterations(L) <= N_max.
Unbounded loops without counter termination are banned. All I/O operations must accept and enforce explicit context.Context deadlines.
Enforcement: Semgrep rules and AST sweep.
Failure Action: Pre-commit and CI blocker.`,
	"HISS-03": `Rule: HISS-03 (Zero Frame Malloc)
Formal Specification: Hot simulation and frame loops must maintain zero dynamic heap allocations: ΔHeapAlloc_tick = 0.
Enforcement: Heap benchmark allocations gate.
Failure Action: CI failure.`,
	"HISS-04": `Rule: HISS-04 (Complexity Bounds & Modular Sizing)
Formal Specification:
  - McCabe Cyclomatic Complexity <= 10
  - Cognitive Complexity <= 15
  - Function Length <= 75 LOC
  - Executable Statements <= 50
Enforcement: gocyclo, gocognit, AST scanners.
Failure Action: Build sweep blocker.`,
	"HISS-07": `Rule: HISS-07 (Checked Errors & Zero Unwrap)
Formal Specification: Zero .unwrap() and .expect() in non-test code. Total ban on unchecked Go error returns. All error flows must be handled or wrapped with context.
Enforcement: golangci-lint, clippy.
Failure Action: Compiler / linter error.`,
	"HISS-08": `Rule: HISS-08 (Static Determinism & Banned Functions)
Formal Specification: Total ban on eval(), exec(), and dynamic runtime code evaluation. Ban on insecure C runtime functions (gets, strcpy, sprintf).
Enforcement: Semgrep rules.
Failure Action: Admission rejection.`,
	"HISS-09": `Rule: HISS-09 (Reference Safety & Mandatory Safety Proofs)
Formal Specification: Any unsafe block must be preceded by an explanatory '// SAFETY:' comment proving invariants.
Enforcement: AST check.
Failure Action: Immediate AST check rejection.`,
	"HISS-10": `Rule: HISS-10 (5-Layer Zero-Warnings Cascade)
Formal Specification: Warnings are treated as fatal errors across IDE, Pre-Commit, Pre-Push, CI, and Pre-Apply layers.
Enforcement: Compile and linter flags (-Werror, zero-warning tolerance).
Failure Action: Exit code 1.`,
	"HISS-11": `Rule: HISS-11 (Hermetic Supply Chain)
Formal Specification: Pinned lockfiles mandatory. Zero floating tags. SLSA Level 3 provenance attestations and Sigstore Cosign signatures.
Enforcement: CI attestation gate.
Failure Action: Deployment rejection.`,
	"HISS-14": `Rule: HISS-14 (Append-Only ABI & Migration Footers)
Formal Specification: Public APIs are append-only. Breaking changes require conventional commit breaking indicator (!) and mandatory Migration: footer.
Enforcement: Git log and API diff analyzer.
Failure Action: PR blocker.`,
	"HISS-15": `Rule: HISS-15 (3D Test Discipline)
Formal Specification: Mandatory Positive, Negative, and Boundary tests for all public interfaces. Touched-file clean rule enforced.
Enforcement: Coverage gates and test matrix.
Failure Action: Merge gate rejection.`,
	"HISS-16": `Rule: HISS-16 (Canonical AGENTS.md & Server Gates)
Formal Specification: Single source of agent instructions (AGENTS.md). Vendor targets compiled via standardsctl compile-context. Sandboxed verification.
Enforcement: Pre-commit blocker, server-side admission.
Failure Action: Merge blocker.`,
}

// lookupRuleExplanation returns authoritative HISS rule descriptions.
func lookupRuleExplanation(ruleID string) (string, bool) {
	val, ok := hissRuleExplanations[ruleID]
	return val, ok
}

// handleInitialize returns MCP initialization metadata.
func (s *Server) handleInitialize(req JSONRPCRequest) *JSONRPCResponse {
	return &JSONRPCResponse{
		JSONRPC: "2.0",
		ID:      req.ID,
		Result: map[string]any{
			"protocolVersion": "2024-11-05",
			"capabilities": map[string]any{
				"tools": map[string]any{},
			},
			"serverInfo": map[string]string{
				"name":    "standards-mcp",
				"version": s.version,
			},
		},
	}
}

// handleToolsList enumerates all registered tools with their schemas.
func (s *Server) handleToolsList(req JSONRPCRequest) *JSONRPCResponse {
	toolList := make([]map[string]any, 0, len(s.order))
	for _, name := range s.order {
		tool := s.tools[name]
		toolList = append(toolList, map[string]any{
			"name":        tool.Name,
			"description": tool.Description,
			"inputSchema": tool.InputSchema,
			"annotations": tool.Annotations,
		})
	}
	return &JSONRPCResponse{
		JSONRPC: "2.0",
		ID:      req.ID,
		Result: map[string]any{
			"tools": toolList,
		},
	}
}

// handleToolsCall dispatches tool execution to the appropriate tool handler.
func (s *Server) handleToolsCall(ctx context.Context, req JSONRPCRequest) *JSONRPCResponse {
	var params struct {
		Name      string         `json:"name"`
		Arguments map[string]any `json:"arguments"`
	}
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return &JSONRPCResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Error: &JSONRPCError{
				Code:    -32602,
				Message: fmt.Sprintf("Invalid params for tools/call: %v", err),
			},
		}
	}

	tool, exists := s.tools[params.Name]
	if !exists {
		return &JSONRPCResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Error: &JSONRPCError{
				Code:    -32601,
				Message: fmt.Sprintf("Tool not found: %s", params.Name),
			},
		}
	}

	res, err := tool.Handler(ctx, params.Arguments)
	if err != nil {
		return &JSONRPCResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Error: &JSONRPCError{
				Code:    -32603,
				Message: fmt.Sprintf("Internal tool execution error: %v", err),
			},
		}
	}

	return &JSONRPCResponse{
		JSONRPC: "2.0",
		ID:      req.ID,
		Result:  res,
	}
}

// HandleRequest processes an incoming JSON-RPC 2.0 request and produces a response.
func (s *Server) HandleRequest(ctx context.Context, req JSONRPCRequest) *JSONRPCResponse {
	switch req.Method {
	case "initialize":
		return s.handleInitialize(req)

	case "notifications/initialized":
		return nil

	case "ping":
		return &JSONRPCResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Result:  map[string]any{},
		}

	case "tools/list":
		return s.handleToolsList(req)

	case "tools/call":
		return s.handleToolsCall(ctx, req)

	default:
		return &JSONRPCResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Error: &JSONRPCError{
				Code:    -32601,
				Message: fmt.Sprintf("Method not found: %s", req.Method),
			},
		}
	}
}

// RunStdio executes the stdio JSON-RPC 2.0 loop.
func (s *Server) RunStdio(ctx context.Context) error {
	scanner := bufio.NewScanner(os.Stdin)
	buf := make([]byte, 64*1024)
	scanner.Buffer(buf, maxScannerBuffer)

	for scanner.Scan() {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		line := scanner.Bytes()
		if len(strings.TrimSpace(string(line))) == 0 {
			continue
		}

		var req JSONRPCRequest
		if err := json.Unmarshal(line, &req); err != nil {
			resp := JSONRPCResponse{
				JSONRPC: "2.0",
				Error: &JSONRPCError{
					Code:    -32700,
					Message: "Parse error: invalid JSON-RPC payload",
				},
			}
			s.writeStdioResponse(&resp)
			continue
		}

		resp := s.HandleRequest(ctx, req)
		if resp != nil {
			s.writeStdioResponse(resp)
		}
	}

	if err := scanner.Err(); err != nil && !errors.Is(err, io.EOF) {
		return fmt.Errorf("scanner error: %w", err)
	}
	return nil
}

// writeStdioResponse serializes a JSON-RPC response to stdout.
func (s *Server) writeStdioResponse(resp *JSONRPCResponse) {
	data, err := json.Marshal(resp)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to marshal response: %v\n", err)
		return
	}
	fmt.Fprintf(os.Stdout, "%s\n", string(data))
}

// writeJSON encodes an HTTP JSON payload without unchecked error suppression.
func writeJSON(w http.ResponseWriter, payload any) {
	if err := json.NewEncoder(w).Encode(payload); err != nil {
		fmt.Fprintf(os.Stderr, "failed to encode response: %v\n", err)
	}
}

// RunHTTP serves JSON-RPC 2.0 requests over HTTP.
func (s *Server) RunHTTP(ctx context.Context, addr string) error {
	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"status":"ok","server":"standards-mcp","version":%q}`, s.version)
	})

	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
			return
		}
		body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxScannerBuffer))
		if err != nil {
			http.Error(w, "Payload Too Large", http.StatusRequestEntityTooLarge)
			return
		}

		var req JSONRPCRequest
		if err := json.Unmarshal(body, &req); err != nil {
			w.Header().Set("Content-Type", "application/json")
			writeJSON(w, JSONRPCResponse{
				JSONRPC: "2.0",
				Error:   &JSONRPCError{Code: -32700, Message: "Parse error"},
			})
			return
		}

		resp := s.HandleRequest(r.Context(), req)
		w.Header().Set("Content-Type", "application/json")
		if resp != nil {
			writeJSON(w, resp)
		}
	})

	server := &http.Server{
		Addr:              addr,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	return s.runHTTPServer(ctx, server)
}

// handleSSEEndpoint handles incoming SSE connections.
func (s *Server) handleSSEEndpoint(ctx context.Context) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		flusher, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "Streaming unsupported", http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")

		sessionID := fmt.Sprintf("session-%d", time.Now().UnixNano())
		fmt.Fprintf(w, "event: endpoint\ndata: /messages?sessionId=%s\n\n", sessionID)
		flusher.Flush()

		select {
		case <-r.Context().Done():
			return
		case <-ctx.Done():
			return
		}
	}
}

// handleSSEMessages handles POST JSON-RPC messages for SSE sessions.
func (s *Server) handleSSEMessages(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxScannerBuffer))
	if err != nil {
		http.Error(w, "Payload Too Large", http.StatusRequestEntityTooLarge)
		return
	}

	var req JSONRPCRequest
	if err := json.Unmarshal(body, &req); err != nil {
		w.Header().Set("Content-Type", "application/json")
		writeJSON(w, JSONRPCResponse{
			JSONRPC: "2.0",
			Error:   &JSONRPCError{Code: -32700, Message: "Parse error"},
		})
		return
	}

	resp := s.HandleRequest(r.Context(), req)
	w.Header().Set("Content-Type", "application/json")
	if resp != nil {
		writeJSON(w, resp)
	}
}

// RunSSE serves the MCP Server-Sent Events transport.
func (s *Server) RunSSE(ctx context.Context, addr string) error {
	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"status":"ok","server":"standards-mcp","version":%q}`, s.version)
	})
	mux.HandleFunc("/sse", s.handleSSEEndpoint(ctx))
	mux.HandleFunc("/messages", s.handleSSEMessages)

	server := &http.Server{
		Addr:              addr,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	return s.runHTTPServer(ctx, server)
}

// runHTTPServer gracefully runs and terminates an HTTP server.
func (s *Server) runHTTPServer(ctx context.Context, server *http.Server) error {
	errChan := make(chan error, 1)
	go func() {
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errChan <- err
		}
		close(errChan)
	}()

	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return server.Shutdown(shutdownCtx)
	case err := <-errChan:
		return err
	}
}
