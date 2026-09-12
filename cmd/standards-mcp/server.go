package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/cordanaLLM/praetor/internal/compiler"
	"github.com/cordanaLLM/praetor/internal/config"
	"github.com/cordanaLLM/praetor/internal/mcp"
	"github.com/cordanaLLM/praetor/internal/needs"
	"github.com/cordanaLLM/praetor/internal/util"
)

const (
	// maxScannerBuffer is the largest accepted JSON-RPC line (stdio) or body (http).
	maxScannerBuffer = 1024 * 1024 // 1MB
	// defaultToolTimeout bounds every tools/call (HISS-02). The per-tool budgets inside
	// the handlers (2-3 minutes) fit within it, and the HTTP write deadline is derived
	// from it so a long tool never outlives the response that reports its outcome.
	defaultToolTimeout = 5 * time.Minute
)

var (
	// ErrOutsideRoot is returned for path arguments that resolve outside -root.
	ErrOutsideRoot = errors.New("path resolves outside the server root; start standards-mcp with -allow-outside-root to permit it")
	// ErrArgType is returned when a tool argument has the wrong JSON type.
	ErrArgType = errors.New("argument has the wrong type")
	// ErrRemoteBenchmarksDisabled is returned when benchmark_popular is requested
	// without -allow-remote-benchmarks.
	ErrRemoteBenchmarksDisabled = errors.New("remote benchmark clones are disabled; start standards-mcp with -allow-remote-benchmarks to permit them")
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

// ServerOptions configures a standards-mcp server instance.
type ServerOptions struct {
	// RootDir is the repository the server governs; "" means the working directory.
	RootDir string
	// Version is reported in initialize and /health responses.
	Version string
	// AllowOutsideRoot permits path arguments outside RootDir (cross-repository
	// adoption, workstation harvests). Off by default: every path is confined.
	AllowOutsideRoot bool
	// AllowRemoteBenchmarks permits standards_dogfood to clone public repositories.
	AllowRemoteBenchmarks bool
	// AuthToken, when set, is required as a bearer token on every http/sse request and
	// is mandatory to bind a non-loopback address.
	AuthToken string
	// AllowedOrigins lists browser origins accepted on http/sse besides loopback.
	AllowedOrigins []string
	// ToolTimeout bounds one tools/call; zero selects defaultToolTimeout.
	ToolTimeout time.Duration
}

// Server implements the MCP server instance.
type Server struct {
	rootDir string
	version string
	opts    ServerOptions
	tools   map[string]mcp.Tool
	order   []string

	sessions *sseRegistry

	boundMu   sync.Mutex
	boundHost string
	boundAddr string
}

// NewServer initializes a standards-mcp server confined to rootDir with default options.
func NewServer(rootDir, version string) (*Server, error) {
	return NewServerWithOptions(ServerOptions{RootDir: rootDir, Version: version})
}

// NewServerWithOptions initializes a standards-mcp server and registers the standard
// tools. The root is resolved to an absolute path once so relative defaults are never
// joined onto a relative root again.
func NewServerWithOptions(opts ServerOptions) (*Server, error) {
	if opts.RootDir == "" {
		opts.RootDir = "."
	}
	root, err := filepath.Abs(filepath.Clean(opts.RootDir))
	if err != nil {
		return nil, fmt.Errorf("resolve root %q: %w", opts.RootDir, err)
	}
	if !util.DirExists(root) {
		return nil, fmt.Errorf("root %q is not a directory", root)
	}
	if opts.ToolTimeout <= 0 {
		opts.ToolTimeout = defaultToolTimeout
	}

	s := &Server{
		rootDir:  root,
		version:  opts.Version,
		opts:     opts,
		tools:    make(map[string]mcp.Tool),
		order:    make([]string, 0, 13),
		sessions: newSSERegistry(),
	}

	if err := s.registerStandardTools(); err != nil {
		return nil, fmt.Errorf("failed to register standard tools: %w", err)
	}

	return s, nil
}

// registerStandardTools registers the standards MCP tools.
func (s *Server) registerStandardTools() error {
	tools := []func() (mcp.Tool, error){
		s.createAuditTool,
		s.createPlanTool,
		s.createCompileContextTool,
		s.createExplainRuleTool,
		s.createInspectSymbolsTool,
		s.createNeedsReportTool,
		s.createAdoptTool,
		s.createDogfoodTool,
		s.createHarvestWorkstationTool,
		s.createPackageDocsTool,
		s.createVersionAuditTool,
		s.createMemoryRecallTool,
		s.createHindsightOptimizeTool,
		s.createTranscriptIngestTool,
		s.createContextAnalyzeTool,
		s.createDogfoodSuiteTool,
		s.createDogfoodScheduleStatusTool,
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

// ---- argument helpers ------------------------------------------------------------------

// argBool reads an optional boolean argument, rejecting a value of any other type so a
// string "true" is never silently treated as false.
func argBool(args map[string]any, key string, def bool) (bool, error) {
	raw, ok := args[key]
	if !ok || raw == nil {
		return def, nil
	}
	b, isBool := raw.(bool)
	if !isBool {
		return false, fmt.Errorf("%w: %s must be a boolean, got %T", ErrArgType, key, raw)
	}
	return b, nil
}

// argString reads an optional string argument ("" when absent), rejecting other types.
func argString(args map[string]any, key string) (string, error) {
	raw, ok := args[key]
	if !ok || raw == nil {
		return "", nil
	}
	str, isString := raw.(string)
	if !isString {
		return "", fmt.Errorf("%w: %s must be a string, got %T", ErrArgType, key, raw)
	}
	return str, nil
}

// resolvePath resolves a path argument against the server root. An absent or empty
// argument yields defaultVal (an absolute server-owned path, or a name relative to the
// root); both supplied and default values are confined unless AllowOutsideRoot is set.
func (s *Server) resolvePath(args map[string]any, key, defaultVal string) (string, error) {
	val, err := argString(args, key)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(val) == "" {
		val = defaultVal
	}
	return s.confinePath(val)
}

// resolveOptionalPath is resolvePath for arguments whose absence means "none".
func (s *Server) resolveOptionalPath(args map[string]any, key string) (string, error) {
	val, err := argString(args, key)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(val) == "" {
		return "", nil
	}
	return s.confinePath(val)
}

// confinePath resolves val (relative to the root, or absolute) and verifies that it
// stays under the root lexically and through symlinks (util.ConfinePath). Paths outside
// the root are permitted only with AllowOutsideRoot.
func (s *Server) confinePath(val string) (string, error) {
	abs := filepath.Clean(val)
	if !filepath.IsAbs(abs) {
		abs = filepath.Join(s.rootDir, abs)
	}

	rel, err := filepath.Rel(s.rootDir, abs)
	if err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		confined, cErr := util.ConfinePath(s.rootDir, rel)
		if cErr == nil {
			return confined, nil
		}
		if !s.opts.AllowOutsideRoot {
			return "", fmt.Errorf("%w: %q: %w", ErrOutsideRoot, val, cErr)
		}
		return abs, nil
	}

	if !s.opts.AllowOutsideRoot {
		return "", fmt.Errorf("%w: %q is not under %s", ErrOutsideRoot, val, s.rootDir)
	}
	return abs, nil
}

// ---- tools ------------------------------------------------------------------------------

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
		var p auditPaths
		var err error
		if p.manifest, err = s.resolvePath(args, "config_path", ".standards.yaml"); err != nil {
			return mcp.ErrorResult(err.Error()), nil
		}
		if p.baseline, err = s.resolvePath(args, "baseline_path", ".standards-baseline.json"); err != nil {
			return mcp.ErrorResult(err.Error()), nil
		}
		if p.agents, err = s.resolvePath(args, "agents_path", "AGENTS.md"); err != nil {
			return mcp.ErrorResult(err.Error()), nil
		}
		return s.runAuditGates(ctx, p), nil
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
		confPath, err := s.resolvePath(args, "config_path", ".standards.yaml")
		if err != nil {
			return mcp.ErrorResult(err.Error()), nil
		}
		manifest, err := config.LoadManifest(confPath)
		if err != nil {
			return mcp.ErrorResult(fmt.Sprintf("Failed to load manifest: %v", err)), nil
		}
		if err := ctx.Err(); err != nil {
			return mcp.ErrorResult(fmt.Sprintf("plan cancelled: %v", err)), nil
		}

		policy := config.DefaultPolicy()
		policy.ApplyOverrides(manifest.Overrides)

		var b strings.Builder
		writePlanHeader(&b, manifest, policy)
		missing, drift := planDrift(s.rootDir, policy)
		writePlanStatus(&b, missing, drift)

		return mcp.TextResult(b.String()), nil
	}

	return mcp.NewReadOnlyTool("standards_plan", "Plan standards enforcement and policy reconciliation", schema, handler)
}

// writePlanHeader prints the resolved policy values of a reconcile plan.
func writePlanHeader(b *strings.Builder, manifest *config.Manifest, policy *config.ResolvedPolicy) {
	b.WriteString("=== cordanaLLM/praetor Reconcile Plan (Dry Run) ===\n")
	fmt.Fprintf(b, "Repository: %s/%s\n", manifest.Repository.Owner, manifest.Repository.Name)
	fmt.Fprintf(b, "Profiles:   %v\nFacets:     %v\n\nTarget Invariants:\n", manifest.Profiles, manifest.Facets)
	fmt.Fprintf(b, "  - Max Cyclomatic Complexity: <= %d\n", policy.Complexity.MaxCyclomatic)
	fmt.Fprintf(b, "  - Max Function LOC:          <= %d\n", policy.Complexity.MaxFuncLOC)
	fmt.Fprintf(b, "  - Linear History Required:    %t\n", policy.BranchProtection.EnforceLinearHistory)
	fmt.Fprintf(b, "  - Signed Commits Required:   %t\n", policy.BranchProtection.RequireSignedCommits)
	fmt.Fprintf(b, "  - Approving Reviewers:       %d\n", policy.BranchProtection.RequiredApprovingReviewers)
	fmt.Fprintf(b, "  - SLSA Provenance Level:     %d\n", policy.SupplyChain.SLSALevel)
	fmt.Fprintf(b, "  - Cosign Attestation:        %t\n", policy.SupplyChain.EnforceCosign)
	fmt.Fprintf(b, "  - SBOM Generation Required:  %t\n", policy.SupplyChain.RequireSBOM)
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
				Description: "Root directory to write target files (default: server root)",
			},
			"verify_only": {
				Type:        "boolean",
				Description: "If true, verify synchronization without writing files",
			},
		},
	}

	handler := func(ctx context.Context, args map[string]any) (*mcp.ToolResult, error) {
		source, err := s.resolvePath(args, "source", "AGENTS.md")
		if err != nil {
			return mcp.ErrorResult(err.Error()), nil
		}
		targetDir, err := s.resolvePath(args, "target_dir", s.rootDir)
		if err != nil {
			return mcp.ErrorResult(err.Error()), nil
		}
		verifyOnly, err := argBool(args, "verify_only", false)
		if err != nil {
			return mcp.ErrorResult(err.Error()), nil
		}
		return s.compileContext(ctx, source, targetDir, verifyOnly), nil
	}

	// Writing overwrites the six vendor files in target_dir, so the tool is destructive.
	return mcp.NewMutatingTool("standards_compile_context", "Compile or verify cross-agent instructions from AGENTS.md", schema, handler, true, true)
}

// compileContext verifies or (re)writes the vendor targets compiled from source.
func (s *Server) compileContext(ctx context.Context, source, targetDir string, verifyOnly bool) *mcp.ToolResult {
	tr := compiler.NewTranspiler()
	res, err := tr.Compile(source)
	if err != nil {
		if verifyOnly {
			return mcp.ErrorResult(fmt.Sprintf("Context verification failed: %v", err))
		}
		return mcp.ErrorResult(fmt.Sprintf("Context compilation failed: %v", err))
	}
	if err := s.confineContextOutputs(res, targetDir); err != nil {
		return mcp.ErrorResult(fmt.Sprintf("Context output confinement failed: %v", err))
	}
	if verifyOnly {
		if err := tr.Verify(source, targetDir); err != nil {
			return mcp.ErrorResult(fmt.Sprintf("Context verification failed: %v", err))
		}
		return mcp.TextResult("All agent context targets are 100% in sync with canonical AGENTS.md.")
	}

	if err := ctx.Err(); err != nil {
		return mcp.ErrorResult(fmt.Sprintf("compile-context cancelled before writing: %v", err))
	}
	if err := tr.WriteOutputs(res, targetDir); err != nil {
		return mcp.ErrorResult(fmt.Sprintf("Failed writing outputs: %v", err))
	}

	var b strings.Builder
	b.WriteString("Cross-agent context transpilation completed successfully:\n")
	for _, f := range res.Files {
		fmt.Fprintf(&b, "  [COMPILED] %-35s (%d lines, budget <= %d)\n", f.RelativePath, f.LineCount, compiler.MaxLineBudget)
	}
	return mcp.TextResult(b.String())
}

// confineContextOutputs checks every descendant before any output is read or written.
// A confined target directory alone does not prevent its children escaping via symlinks.
func (s *Server) confineContextOutputs(result *compiler.CompileResult, targetDir string) error {
	for i := 0; i < len(result.Files); i++ {
		if _, err := s.confinePath(filepath.Join(targetDir, result.Files[i].RelativePath)); err != nil {
			return fmt.Errorf("target %s: %w", result.Files[i].RelativePath, err)
		}
	}
	return nil
}

// createExplainRuleTool builds the read-only standards_explain_rule tool.
func (s *Server) createExplainRuleTool() (mcp.Tool, error) {
	ruleList := strings.Join(knownRuleIDs(), ", ")
	schema := mcp.ToolInputSchema{
		Type: "object",
		Properties: map[string]mcp.PropertySchema{
			"rule_id": {
				Type:        "string",
				Description: "The HISS rule identifier to explain (one of: " + ruleList + ")",
				Enum:        knownRuleIDs(),
			},
		},
		Required: []string{"rule_id"},
	}

	handler := func(ctx context.Context, args map[string]any) (*mcp.ToolResult, error) {
		ruleID, err := argString(args, "rule_id")
		if err != nil {
			return mcp.ErrorResult(err.Error()), nil
		}
		ruleID = strings.ToUpper(strings.TrimSpace(ruleID))

		explanation, found := lookupRuleExplanation(ruleID)
		if !found {
			return mcp.ErrorResult(fmt.Sprintf("Unknown rule %q. Valid rules: %s", ruleID, ruleList)), nil
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
				Description: "File or directory path (relative to the server root) to inspect Go AST symbols",
			},
		},
		Required: []string{"path"},
	}

	handler := func(ctx context.Context, args map[string]any) (*mcp.ToolResult, error) {
		targetPath, err := s.resolveOptionalPath(args, "path")
		if err != nil {
			return mcp.ErrorResult(err.Error()), nil
		}
		if targetPath == "" {
			return mcp.ErrorResult("path parameter is required"), nil
		}
		if err := ctx.Err(); err != nil {
			return mcp.ErrorResult(fmt.Sprintf("inspection cancelled: %v", err)), nil
		}

		report, err := s.inspectSymbolsAtPath(targetPath)
		if err != nil {
			return mcp.ErrorResult(fmt.Sprintf("Inspection failed: %v", err)), nil
		}

		return mcp.TextResult(report), nil
	}

	return mcp.NewReadOnlyTool("standards_inspect_symbols", "Inspect Go AST symbols and analyze HISS-04 complexity bounds (LOC, statements, cyclomatic, cognitive)", schema, handler)
}

// createNeedsReportTool builds the read-only standards_needs_report tool.
func (s *Server) createNeedsReportTool() (mcp.Tool, error) {
	schema := mcp.ToolInputSchema{
		Type: "object",
		Properties: map[string]mcp.PropertySchema{
			"path": {
				Type:        "string",
				Description: "Path to repository to scan (default: server root)",
			},
			"framework": {
				Type:        "string",
				Description: "Path to a local checkout of the target framework (default: built-in capability index)",
			},
		},
	}

	handler := func(ctx context.Context, args map[string]any) (*mcp.ToolResult, error) {
		targetPath, err := s.resolvePath(args, "path", s.rootDir)
		if err != nil {
			return mcp.ErrorResult(err.Error()), nil
		}
		fwPath, err := s.resolveOptionalPath(args, "framework")
		if err != nil {
			return mcp.ErrorResult(err.Error()), nil
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
		fmt.Fprintf(&b, "=== Golusoris Migration Report: %s ===\n", rep.Repository)
		fmt.Fprintf(&b, "Framework: %s (%s) | Readiness Score: %.1f%%\n\n", fwIndex.Name, fwIndex.Version, rep.Readiness.Score)
		b.WriteString("Drop-In Replacement Matrix:\n")
		for _, dep := range rep.Dependencies {
			if dep.Status == needs.StatusCovered || dep.Status == needs.StatusAdapterAvailable {
				fmt.Fprintf(&b, "  ✓ %-35s -> %s\n", dep.Package, dep.GolusorisReplacement)
			} else {
				fmt.Fprintf(&b, "  ✗ %-35s -> NO DIRECT EQUIVALENT (Gap)\n", dep.Package)
			}
		}

		return mcp.TextResult(b.String()), nil
	}

	return mcp.NewReadOnlyTool("standards_needs_report", "Evaluate repository needs and Golusoris migration compatibility", schema, handler)
}

// ---- HISS rule explanations ----------------------------------------------------------------

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
Enforcement: gocyclo, gocognit and funlen via golangci-lint (.golangci.yml), plus the standards_inspect_symbols AST scanner.
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
Enforcement: CI coverage gate (go test -race -coverprofile with a minimum statement-coverage floor enforced by 'go tool cover') and PR checklist validation of the 3D test attestation.
Failure Action: Merge gate rejection.`,
	"HISS-16": `Rule: HISS-16 (Canonical AGENTS.md & Server Gates)
Formal Specification: Single source of agent instructions (AGENTS.md). Vendor targets compiled via praetorctl compile-context. Sandboxed verification.
Enforcement: Pre-commit blocker, server-side admission.
Failure Action: Merge blocker.`,
	"HISS-17": `Rule: HISS-17 (State Ledger Discipline)
Formal Specification: Every agent turn starts by inspecting .workingdir/STATE.md and .workingdir/OPEN.md; tasks are tracked via 'praetorctl state task'; every turn ends with 'praetorctl state sync .'.
Enforcement: Pre-commit state-sync hook and the CI / pre-push state audit.
Failure Action: Pre-commit / CI gate rejection.`,
	"HISS-18": `Rule: HISS-18 (CI Efficiency)
Formal Specification: Diff-aware change gating: heavy race and security gates are skipped on docs-only or state-only changes as classified by 'praetorctl ci filter'.
Enforcement: CI filter step exporting run_* outputs that every heavy gate's condition consumes.
Failure Action: CI optimization gate.`,
}

// knownRuleIDs returns the explainable rule identifiers in ascending order.
func knownRuleIDs() []string {
	ids := make([]string, 0, len(hissRuleExplanations))
	for id := range hissRuleExplanations {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

// lookupRuleExplanation returns authoritative HISS rule descriptions.
func lookupRuleExplanation(ruleID string) (string, bool) {
	val, ok := hissRuleExplanations[ruleID]
	return val, ok
}

// ---- JSON-RPC dispatch ----------------------------------------------------------------------

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

// errorResponse builds a JSON-RPC error response.
func errorResponse(id any, code int, message string) *JSONRPCResponse {
	return &JSONRPCResponse{
		JSONRPC: "2.0",
		ID:      id,
		Error:   &JSONRPCError{Code: code, Message: message},
	}
}

// handleToolsCall dispatches tool execution to the appropriate tool handler under the
// per-call deadline (HISS-02).
func (s *Server) handleToolsCall(ctx context.Context, req JSONRPCRequest) *JSONRPCResponse {
	var params struct {
		Name      string         `json:"name"`
		Arguments map[string]any `json:"arguments"`
	}
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return errorResponse(req.ID, -32602, fmt.Sprintf("Invalid params for tools/call: %v", err))
	}

	tool, exists := s.tools[params.Name]
	if !exists {
		return errorResponse(req.ID, -32601, fmt.Sprintf("Tool not found: %s", params.Name))
	}
	if tool.Handler == nil {
		return errorResponse(req.ID, -32603, fmt.Sprintf("Tool %s has no handler", params.Name))
	}
	if params.Arguments == nil {
		params.Arguments = map[string]any{}
	}

	callCtx, cancel := context.WithTimeout(ctx, s.opts.ToolTimeout)
	defer cancel()

	res, err := tool.Handler(callCtx, params.Arguments)
	if err != nil {
		return errorResponse(req.ID, -32603, fmt.Sprintf("Internal tool execution error: %v", err))
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
		return errorResponse(req.ID, -32601, fmt.Sprintf("Method not found: %s", req.Method))
	}
}
