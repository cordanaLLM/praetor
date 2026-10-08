package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/cordanaLLM/praetor/internal/adopt"
	"github.com/cordanaLLM/praetor/internal/buildid"
	"github.com/cordanaLLM/praetor/internal/compiler"
	"github.com/cordanaLLM/praetor/internal/config"
	"github.com/cordanaLLM/praetor/internal/hisscatalog"
	"github.com/cordanaLLM/praetor/internal/mcp"
	"github.com/cordanaLLM/praetor/internal/needs"
	"github.com/cordanaLLM/praetor/internal/util"
	"github.com/cordanaLLM/praetor/internal/workstation"
)

const (
	// maxScannerBuffer is the largest accepted JSON-RPC line (stdio) or body (http).
	maxScannerBuffer = 1024 * 1024 // 1MB
	// defaultToolTimeout bounds every tools/call (HISS-02). The per-tool budgets inside
	// the handlers (2-3 minutes) fit within it, and the HTTP write deadline is derived
	// from it so a long tool never outlives the response that reports its outcome.
	defaultToolTimeout = 5 * time.Minute
)

// JSON-RPC 2.0 error codes (https://www.jsonrpc.org/specification#error_object).
const (
	codeParseError     = -32700
	codeInvalidRequest = -32600
	codeMethodNotFound = -32601
	codeInvalidParams  = -32602
	codeInternalError  = -32603
)

// engineBuild describes the running binary for the engine-build check the context write runs;
// tests substitute a fake stale build.
var engineBuild = workstation.RunningBuild

var (
	// ErrOutsideRoot is returned for path arguments that resolve outside -root.
	ErrOutsideRoot = errors.New("path resolves outside the server root; start standards-mcp with -allow-outside-root to permit it")
	// ErrArgType is returned when a tool argument has the wrong JSON type.
	ErrArgType = errors.New("argument has the wrong type")
	// ErrRemoteBenchmarksDisabled is returned when benchmark_popular is requested
	// without -allow-remote-benchmarks.
	ErrRemoteBenchmarksDisabled = errors.New("remote benchmark clones are disabled; start standards-mcp with -allow-remote-benchmarks to permit them")
)

// JSONRPCRequest represents a JSON-RPC 2.0 request payload. A nil ID marks a
// notification; decodeRequest rejects an explicit null id, which MCP forbids.
type JSONRPCRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      any             `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

// JSONRPCResponse represents a JSON-RPC 2.0 response payload. ID is always encoded: a
// response to a message whose id could not be read (parse error, invalid request)
// carries "id": null, as JSON-RPC 2.0 section 5 requires, never an absent member.
type JSONRPCResponse struct {
	JSONRPC string        `json:"jsonrpc"`
	ID      any           `json:"id"`
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
	// Build is the binary identity Version renders (buildid.Running), which dogfood suite and
	// discovery reports record in engine_build (#689). The zero value records what the running
	// build information proves without an injected release.
	Build buildid.Identity
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
	// ToolsMode selects what tools/list serves: "full" (default, every tool with its
	// schema) or "index" (discovery tools and the hot tools only). tools/call accepts every
	// registered tool in both modes.
	ToolsMode string
	// OffloadThreshold is the size in bytes of all text items of one tool result above which
	// the largest items are written to .standards/cache/mcp-out and replaced by pointer
	// lines. Zero reads the mcp.offload_threshold_bytes key of the root's manifest and, when
	// that is absent, selects mcp.OffloadThresholdBytes; mcp.OffloadDisabled turns offloading
	// off.
	OffloadThreshold int
}

// Server implements the MCP server instance.
type Server struct {
	rootDir string
	version string
	opts    ServerOptions
	tools   map[string]mcp.Tool
	// order lists the tool names sorted ascending, so tools/list bytes do not depend on
	// the order the constructors run in.
	order     []string
	toolsMode string

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
	toolsMode, err := normalizeToolsMode(opts.ToolsMode)
	if err != nil {
		return nil, err
	}
	var notice string
	if opts.OffloadThreshold, notice, err = resolveOffloadThreshold(root, opts.OffloadThreshold); err != nil {
		return nil, err
	}
	if notice != "" {
		fmt.Fprintln(os.Stderr, notice)
	}

	s := &Server{
		rootDir:  root,
		version:  opts.Version,
		opts:     opts,
		tools:    make(map[string]mcp.Tool),
		order:    make([]string, 0, 32),
		sessions: newSSERegistry(),

		toolsMode: toolsMode,
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
		s.createNotebookPrepareTool,
		s.createPlanningValidateTool,
		s.createPlanningPrepareTool,
		s.createContextAnalyzeTool,
		s.createDogfoodSuiteTool,
		s.createDogfoodDiscoveryTool,
		s.createDogfoodScheduleStatusTool,
		s.createDogfoodRepairStatusTool,
		s.createWishesStatusTool,
		s.createWishesUpdateTool,
		s.createClientCapabilitiesTool,
		s.createToolsIndexTool,
		s.createToolDescribeTool,
		s.createOutputReadTool,
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
	sort.Strings(s.order)
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

// requireStringArguments rejects every supplied argument that is not a JSON string,
// explicit null included, for tools whose arguments are all strings; argString alone reads
// null as absent. Undeclared keys never reach a handler: handleToolsCall refuses them
// first through the tool's input schema.
func requireStringArguments(args map[string]any) error {
	keys := make([]string, 0, len(args))
	for key := range args {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		if _, isString := args[key].(string); !isString {
			return fmt.Errorf("%w: %s must be a string, got %T", ErrArgType, key, args[key])
		}
	}
	return nil
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
			"catalog_root": {
				Type: "string", Description: "Root containing pinned .config/archetypes (default: server root)",
			},
			"fleet_config_path": {
				Type: "string", Description: "Explicit fleet complexity policy file for this audit",
			},
			"organization_config_path": {
				Type: "string", Description: "Explicit organization complexity policy file for this audit",
			},
			"deployment_config_path": {
				Type: "string", Description: "Explicit deployment complexity policy file for this audit",
			},
			"workstation_config_path": {
				Type: "string", Description: "Explicit workstation complexity policy file for this audit",
			},
		},
	}

	handler := func(ctx context.Context, args map[string]any) (*mcp.ToolResult, error) {
		p, err := s.resolveAuditPaths(args)
		if err != nil {
			return mcpErrorResult(err.Error(), mcpTextUntrusted), nil
		}
		return s.runAuditGates(ctx, p), nil
	}

	return mcp.NewReadOnlyTool("standards_audit", "Audit repository against declared HISS standards", schema, handler)
}

// resolveAuditPaths preserves confinement for both existing and optional audit inputs.
// An omitted external policy path contributes no layer and performs no discovery.
func (s *Server) resolveAuditPaths(args map[string]any) (auditPaths, error) {
	var p auditPaths
	inputs := []struct {
		key, fallback string
		target        *string
	}{
		{"config_path", ".standards.yaml", &p.manifest},
		{"baseline_path", ".standards-baseline.json", &p.baseline},
		{"agents_path", "AGENTS.md", &p.agents},
		{"catalog_root", "", &p.policy.CatalogRoot},
		{"fleet_config_path", "", &p.policy.FleetPath},
		{"organization_config_path", "", &p.policy.OrganizationPath},
		{"deployment_config_path", "", &p.policy.DeploymentPath},
		{"workstation_config_path", "", &p.policy.WorkstationPath},
	}
	for _, input := range inputs {
		var path string
		var err error
		if input.fallback == "" {
			path, err = s.resolveOptionalPath(args, input.key)
		} else {
			path, err = s.resolvePath(args, input.key, input.fallback)
		}
		if err != nil {
			return p, err
		}
		*input.target = path
	}
	return p, nil
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
			"catalog_root": {
				Type: "string", Description: "Root containing pinned .config/archetypes (default: server root)",
			},
		},
	}

	handler := func(ctx context.Context, args map[string]any) (*mcp.ToolResult, error) {
		manifest, policy, notice, failure := s.resolvePlanInputs(ctx, args)
		if failure != nil {
			return failure, nil
		}

		var b mcpTextBuilder
		if notice != "" {
			b.Template("[INFO] %s\n", notice)
		}
		if err := writePlanHeader(&b, manifest, policy); err != nil {
			return mcp.ErrorResult(fmt.Sprintf("Failed to resolve plan policy: %v", err)), nil
		}
		missing, drift, err := adopt.PlanDrift(ctx, s.rootDir, policy)
		if err != nil {
			return mcp.ErrorResult(fmt.Sprintf("Failed to inspect plan drift: %v", err)), nil
		}
		// The plan verdict internal/adopt authors once for this tool and the CLI plan. This tool
		// never reads the forge, so it says the live branch protection was not compared (#159).
		b.External(adopt.FormatPlanStatus(missing, drift, false), mcpTextShared)

		return mcpComposedTextResult(b.Text()), nil
	}

	return mcp.NewReadOnlyTool("standards_plan", "Plan standards enforcement and policy reconciliation", schema, handler)
}

// resolvePlanInputs loads the manifest and resolves the policy a standards_plan call previews.
// A non-nil result is the error to return to the caller; the other values are then unset.
func (s *Server) resolvePlanInputs(ctx context.Context, args map[string]any) (*config.Manifest, *config.ResolvedPolicy, string, *mcp.ToolResult) {
	confPath, err := s.resolvePath(args, "config_path", ".standards.yaml")
	if err != nil {
		return nil, nil, "", mcpErrorResult(err.Error(), mcpTextUntrusted)
	}
	// The same confined catalog selection standards_audit accepts, so a repository whose
	// pinned catalog is not materialized can be previewed against the bundle it pins.
	catalogRoot, err := s.resolveOptionalPath(args, "catalog_root")
	if err != nil {
		return nil, nil, "", mcpErrorResult(err.Error(), mcpTextUntrusted)
	}
	manifest, err := config.LoadManifest(confPath)
	if err != nil {
		return nil, nil, "", mcp.ErrorResult(fmt.Sprintf("Failed to load manifest: %v", err))
	}
	if err := ctx.Err(); err != nil {
		return nil, nil, "", mcp.ErrorResult(fmt.Sprintf("plan cancelled: %v", err))
	}
	// The same resolution as the CLI plan, adopt and sync: the pinned profiles and facets
	// joined with the repository overrides. Defaults plus overrides alone previewed a
	// weaker ruleset than adopt writes for any repository pinned to a stricter profile.
	policy, notice, err := config.ResolveRepositoryPolicyFromCatalog(ctx, confPath, catalogRoot, manifest)
	if err != nil {
		return nil, nil, "", mcp.ErrorResult(fmt.Sprintf("Failed to resolve plan policy: %v", err))
	}
	if policy == nil {
		return nil, nil, "", mcp.ErrorResult(fmt.Sprintf("no manifest to plan at %s", confPath))
	}
	return manifest, policy, notice, nil
}

// writePlanHeader prints the resolved policy values of a reconcile plan.
func writePlanHeader(b *mcpTextBuilder, manifest *config.Manifest, policy *config.ResolvedPolicy) error {
	reviewCount, _, err := policy.BranchProtection.EffectiveReviewRequirements()
	if err != nil {
		return fmt.Errorf("resolve branch protection reviews: %w", err)
	}
	// Repository-neutral heading; the manifest-backed identity follows on the next line.
	// This used to name this product's own repository in every adopted repository (#361).
	b.Template("=== Praetor Reconcile Plan (Dry Run) ===\nRepository: %s/%s\nprofiles: %v.\nfacets: %v.\ninvariants:\n- max_cyclomatic: %d.\n- max_function_loc: %d.\n- linear_history: %t.\n- signed_commits: %t.\n- approving_reviewers: %d.\n- configured_reviewer_minimum: %d.\n- review_mode: %s.\n- slsa_level: %d.\n- cosign_attestation: %t.\n- sbom_generation: %t.\n",
		manifest.Repository.Owner, manifest.Repository.Name, manifest.Profiles, manifest.Facets,
		policy.Complexity.MaxCyclomatic, policy.Complexity.MaxFuncLOC,
		policy.BranchProtection.EnforceLinearHistory, policy.BranchProtection.RequireSignedCommits,
		reviewCount, policy.BranchProtection.RequiredApprovingReviewers,
		policy.BranchProtection.ReviewMode, policy.SupplyChain.SLSALevel,
		policy.SupplyChain.EnforceCosign, policy.SupplyChain.RequireSBOM)
	return nil
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
			return mcpErrorResult(err.Error(), mcpTextUntrusted), nil
		}
		targetDir, err := s.resolvePath(args, "target_dir", s.rootDir)
		if err != nil {
			return mcpErrorResult(err.Error(), mcpTextUntrusted), nil
		}
		verifyOnly, err := argBool(args, "verify_only", false)
		if err != nil {
			return mcpErrorResult(err.Error(), mcpTextUntrusted), nil
		}
		return s.compileContext(ctx, source, targetDir, verifyOnly), nil
	}

	// Writing overwrites the vendor files, persona projections and plugin copies in target_dir,
	// so the tool is destructive.
	return mcp.NewMutatingTool("standards_compile_context", "Compile or verify cross-agent instructions from AGENTS.md", schema, handler, true, true)
}

// compileContext verifies or (re)writes the vendor targets, persona projections and plugin
// copies compiled from source, through the implementation the CLI's compile-context runs
// (compiler.VerifyCompiledContext, adopt.CompileAgentContext). Both reconcile the text
// register block themselves: a verify-only call never writes the source and reports a stale or
// missing block as a verification failure instead.
func (s *Server) compileContext(ctx context.Context, source, targetDir string, verifyOnly bool) *mcp.ToolResult {
	source, err := s.resolveContextPath(ctx, source)
	if err != nil {
		return mcp.ErrorResult(fmt.Sprintf("Context source confinement failed: %v", err))
	}
	tr := compiler.NewTranspiler()
	var b strings.Builder
	if verifyOnly {
		if err := compiler.VerifyCompiledContext(ctx, &b, tr, source, targetDir); err != nil {
			return mcp.ErrorResult(fmt.Sprintf("Context verification failed: %v", err))
		}
		// The compiler's shared report, the same text the CLI prints; bound by count and digest.
		return mcpTextResult(b.String(), mcpTextShared)
	}
	if err := ctx.Err(); err != nil {
		return mcp.ErrorResult(fmt.Sprintf("compile-context cancelled before writing: %v", err))
	}
	// An installed server older than the engine checkout it serves would write its own stale
	// rendering, as the CLI's compile-context did from client wrappers (BUG-1004).
	err = workstation.CheckBuildCurrent(ctx, targetDir, engineBuild())
	if err == nil {
		// The CLI's write: the evidence ignore reconciliation, then every projection and the lint.
		err = adopt.CompileAgentContext(ctx, &b, tr, source, targetDir)
	}
	if err != nil {
		return mcp.ErrorResult(fmt.Sprintf("Context compilation failed: %v", err))
	}
	// The compiler's shared report, the same text the CLI prints; bound by count and digest.
	return mcpTextResult(b.String(), mcpTextShared)
}

func (s *Server) resolveContextPath(ctx context.Context, path string) (string, error) {
	if _, err := s.confinePath(path); err != nil {
		return "", err
	}
	resolved, err := util.ResolveExistingPath(ctx, path)
	if err != nil {
		return "", err
	}
	if s.opts.AllowOutsideRoot {
		return resolved, nil
	}
	root, err := util.ResolveExistingPath(ctx, s.rootDir)
	if err != nil {
		return "", err
	}
	rel, err := filepath.Rel(root, resolved)
	if err != nil || !filepath.IsLocal(rel) {
		return "", fmt.Errorf("%w: resolved context path", ErrOutsideRoot)
	}
	return resolved, nil
}

// createExplainRuleTool builds the read-only standards_explain_rule tool.
func (s *Server) createExplainRuleTool() (mcp.Tool, error) {
	ruleList := strings.Join(knownRuleIDs(), ", ")
	schema := mcp.ToolInputSchema{
		Type: "object",
		Properties: map[string]mcp.PropertySchema{
			"rule_id": {
				Type:        "string",
				Description: "input: HISS rule identifier. allowed: schema enum.",
				Enum:        knownRuleIDs(),
			},
		},
		Required: []string{"rule_id"},
	}

	handler := func(ctx context.Context, args map[string]any) (*mcp.ToolResult, error) {
		ruleID, err := argString(args, "rule_id")
		if err != nil {
			return mcpErrorResult(err.Error(), mcpTextUntrusted), nil
		}
		ruleID = strings.ToUpper(strings.TrimSpace(ruleID))

		explanation, found := lookupRuleExplanation(ruleID)
		if !found {
			return mcp.ErrorResult(fmt.Sprintf("Unknown rule %q. Valid rules: %s", ruleID, ruleList)), nil
		}

		// The catalog text internal/hisscatalog authors once, closed by its adopted-repository line.
		return mcpTextResult(explanation, mcpTextShared), nil
	}

	return mcp.NewReadOnlyTool("standards_explain_rule", "Explain specific HISS invariant rule plus formal verification mechanism", schema, handler)
}

// createInspectSymbolsTool builds the read-only standards_inspect_symbols tool.
func (s *Server) createInspectSymbolsTool() (mcp.Tool, error) {
	schema := mcp.ToolInputSchema{
		Type: "object",
		Properties: map[string]mcp.PropertySchema{
			"path": {
				Type:        "string",
				Description: "Go AST symbol inspection path; file or directory; server-root relative",
			},
		},
		Required: []string{"path"},
	}

	handler := func(ctx context.Context, args map[string]any) (*mcp.ToolResult, error) {
		targetPath, err := s.resolveOptionalPath(args, "path")
		if err != nil {
			return mcpErrorResult(err.Error(), mcpTextUntrusted), nil
		}
		if targetPath == "" {
			return mcp.ErrorResult("block: path parameter required."), nil
		}
		if err := ctx.Err(); err != nil {
			return mcp.ErrorResult(fmt.Sprintf("inspection cancelled: %v", err)), nil
		}

		var report mcpGovernedText
		report, err = s.inspectSymbolsAtPath(ctx, targetPath)
		if err != nil {
			return mcp.ErrorResult(fmt.Sprintf("Inspection failed: %v", err)), nil
		}

		return mcpComposedTextResult(report), nil
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
				Type: "string",
				Description: "Go target framework local checkout path; default: $PRAETOR_FRAMEWORK_DIR, else " +
					"framework.targets.go.checkout, else framework.targets.go.contract, else " +
					"framework.targets.go.module; \"\" selects declaration",
			},
		},
	}

	handler := func(ctx context.Context, args map[string]any) (*mcp.ToolResult, error) {
		targetPath, err := s.resolvePath(args, "path", s.rootDir)
		if err != nil {
			return mcpErrorResult(err.Error(), mcpTextUntrusted), nil
		}
		fwPath, err := s.resolveOptionalPath(args, "framework")
		if err != nil {
			return mcpErrorResult(err.Error(), mcpTextUntrusted), nil
		}
		registry, err := loadNeedsRegistry(ctx)
		if err != nil {
			return mcp.ErrorResult(fmt.Sprintf("Failed to load operator settings: %v", err)), nil
		}
		_, explicit := args["framework"]
		source := needs.SelectFrameworkSource(needs.FrameworkSelection{
			Explicit: fwPath, ExplicitSet: explicit, Getenv: os.Getenv, Targets: registry.Targets(),
		})

		fwIndex, err := needs.InspectFramework(ctx, source)
		if err != nil {
			return mcp.ErrorResult(fmt.Sprintf("Failed to inspect framework: %v", err)), nil
		}

		rep, err := needs.ReportRepoWithFramework(ctx, targetPath, fwIndex, registry)
		if err != nil {
			return mcp.ErrorResult(fmt.Sprintf("Failed to scan repository: %v", err)), nil
		}

		var b mcpTextBuilder
		// internal/needs renders the header once for this tool and the CLI needs report, for the
		// framework the row is scored against; the relationship table and the umbrella-import
		// recommendations carry repository-derived names.
		b.External(needs.FormatReportHeader(rep, needs.RowFramework(registry, rep, fwIndex)), mcpTextShared)
		b.External(needs.FormatLibraryRelationships(rep), mcpTextUntrusted)
		b.External(needs.FormatUmbrellaImports(rep), mcpTextUntrusted)

		return mcpComposedTextResult(b.Text()), nil
	}

	return mcp.NewReadOnlyTool("standards_needs_report", "Evaluate repository needs and target framework migration compatibility", schema, handler)
}

// installManifestPath locates the install manifest that records the host's settings
// documents. Tests replace it so no test reads the operator's own manifest.
var installManifestPath = config.DefaultInstallManifestPath

// loadNeedsRegistry selects the operator settings at call time the way hook does
// (PRAETOR_FLEET_CONFIG / PRAETOR_WORKSTATION_CONFIG, then the install manifest) and prepares
// the needs engine for their framework targets through needs.SelectRegistry, the loader the
// CLI needs subcommands share. A host with no per-user configuration directory selects no
// manifest.
func loadNeedsRegistry(ctx context.Context) (*needs.AnalyzerRegistry, error) {
	manifest, err := installManifestPath()
	if err != nil {
		manifest = ""
	}
	_, registry, err := needs.SelectRegistry(ctx, config.SettingsRequest{Getenv: os.Getenv, ManifestPath: manifest})
	return registry, err
}

// ---- HISS rule explanations ----------------------------------------------------------------

// knownRuleIDs returns the explainable rule identifiers in ascending order: every invariant
// of the HISS catalog in internal/hisscatalog, the same registry the generated wiki matrix and
// the adopted AGENTS.md harness render.
func knownRuleIDs() []string {
	return hisscatalog.RuleIDs()
}

// lookupRuleExplanation returns the authoritative HISS rule description, closed by the line
// that states what an adopted repository enforces (BUG-804).
func lookupRuleExplanation(ruleID string) (string, bool) {
	rule, ok := hisscatalog.LookupRule(ruleID)
	if !ok {
		return "", false
	}
	return rule.AdoptedExplanation(), true
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
				"tools": map[string]any{"listChanged": false},
			},
			"serverInfo": map[string]string{
				"name":    "standards-mcp",
				"version": s.version,
			},
		},
	}
}

// handleToolsList enumerates the tools of the server's mode, sorted by name, with their
// schemas. The result carries no version or other per-build text, so its bytes are stable
// across builds that register the same tools.
func (s *Server) handleToolsList(req JSONRPCRequest) *JSONRPCResponse {
	names := s.listedToolNames()
	toolList := make([]map[string]any, 0, len(names))
	for _, name := range names {
		toolList = append(toolList, listedDescriptor(s.tools[name]))
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
		return errorResponse(req.ID, codeInvalidParams, fmt.Sprintf("Invalid params for tools/call: %v", err))
	}

	tool, exists := s.tools[params.Name]
	if !exists {
		return errorResponse(req.ID, codeMethodNotFound, fmt.Sprintf("Tool not found: %s", params.Name))
	}
	if tool.Handler == nil {
		return errorResponse(req.ID, codeInternalError, fmt.Sprintf("Tool %s has no handler", params.Name))
	}
	if params.Arguments == nil {
		params.Arguments = map[string]any{}
	}

	res, err := s.runTool(ctx, tool, params.Arguments)
	if err != nil {
		return errorResponse(req.ID, codeInternalError, servedErrorText(err))
	}
	served, err := s.offloader().Serve(ctx, params.Name, res)
	if err != nil {
		return errorResponse(req.ID, codeInternalError, fmt.Sprintf("Tool %s result withheld: %v", params.Name, err))
	}

	return &JSONRPCResponse{
		JSONRPC: "2.0",
		ID:      req.ID,
		Result:  served,
	}
}

// offloader returns the output offloader bound to the server root.
func (s *Server) offloader() mcp.Offloader {
	return mcp.Offloader{Root: s.rootDir, Threshold: s.opts.OffloadThreshold}
}

// runTool runs one tool under the per-call deadline (HISS-02). One strict check sits in
// front of every handler: an undeclared key is refused before any side effect. MCP reports
// input validation as a tool execution error (isError) so the model can correct the call,
// not as a JSON-RPC protocol error. The refusal quotes client-supplied keys, so it is
// returned as a result and passes the caller's single SanitizeResult path like any other.
func (s *Server) runTool(ctx context.Context, tool mcp.Tool, args map[string]any) (*mcp.ToolResult, error) {
	if err := tool.InputSchema.CheckArguments(args); err != nil {
		return mcpErrorResult(err.Error(), mcpTextUntrusted), nil
	}
	callCtx, cancel := context.WithTimeout(ctx, s.opts.ToolTimeout)
	defer cancel()
	return tool.Handler(callCtx, args)
}

// servedErrorText renders a handler error for the client. Errors quote paths, file
// content and upstream messages, so they pass the same neutralizer as results; an error
// too large to inspect is replaced by the bound violation instead of being served raw.
func servedErrorText(err error) string {
	text, sanitizeErr := mcp.SanitizeText(err.Error())
	if sanitizeErr != nil {
		return fmt.Sprintf("Internal tool execution error withheld: %v", sanitizeErr)
	}
	return "Internal tool execution error: " + text
}

// validRequestID reports whether id is a JSON-RPC string or number. decodeRequest keeps
// numbers as json.Number; in-process callers may pass Go integers or floats.
func validRequestID(id any) bool {
	switch id.(type) {
	case string, json.Number, float64, float32, int, int32, int64, uint, uint32, uint64:
		return true
	default:
		return false
	}
}

// validateEnvelope answers a message that is not a valid JSON-RPC 2.0 request object
// with -32600 Invalid Request, or returns nil. Such a message is answered even without an
// id, because the server cannot tell whether it was meant as a notification.
func validateEnvelope(req JSONRPCRequest) *JSONRPCResponse {
	if req.ID != nil && !validRequestID(req.ID) {
		return errorResponse(nil, codeInvalidRequest, "Invalid Request: id must be a string or a number")
	}
	if req.JSONRPC != "2.0" {
		return errorResponse(req.ID, codeInvalidRequest, `Invalid Request: jsonrpc must be exactly "2.0"`)
	}
	if req.Method == "" {
		return errorResponse(req.ID, codeInvalidRequest, "Invalid Request: method is required")
	}
	return nil
}

// HandleRequest processes one JSON-RPC 2.0 message. A request (a message with an id)
// gets exactly one response. A notification (no id) never gets one, whatever its method,
// and is never dispatched: every method this server implements is a request, so a
// notification such as notifications/initialized has nothing to execute, and a request
// method sent without an id must not run a tool whose outcome nobody can receive.
func (s *Server) HandleRequest(ctx context.Context, req JSONRPCRequest) *JSONRPCResponse {
	if resp := validateEnvelope(req); resp != nil {
		return resp
	}
	if req.ID == nil {
		return nil
	}
	return s.dispatch(ctx, req)
}

// dispatch routes a validated request to its method handler.
func (s *Server) dispatch(ctx context.Context, req JSONRPCRequest) *JSONRPCResponse {
	switch req.Method {
	case "initialize":
		return s.handleInitialize(req)

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
		return errorResponse(req.ID, codeMethodNotFound, fmt.Sprintf("Method not found: %s", req.Method))
	}
}
