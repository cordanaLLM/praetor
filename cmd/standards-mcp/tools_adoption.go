package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/cordanaLLM/praetor/internal/adopt"
	"github.com/cordanaLLM/praetor/internal/dogfood"
	"github.com/cordanaLLM/praetor/internal/mcp"
)

const (
	adoptBudget   = 2 * time.Minute
	dogfoodBudget = 3 * time.Minute
	harvestBudget = 2 * time.Minute
	// maxDogfoodScanTargets bounds the local repositories one dogfood call evaluates.
	maxDogfoodScanTargets = 10
)

// adoptArgs holds the parsed standards_adopt arguments.
type adoptArgs struct {
	path       string
	dryRun     bool
	force      bool
	recordBase bool
	sourceRoot string
}

// parseAdoptArgs validates the adoption arguments; the target path is confined to the
// server root unless -allow-outside-root is set.
func (s *Server) parseAdoptArgs(args map[string]any) (adoptArgs, error) {
	var a adoptArgs
	var err error
	if a.path, err = s.resolvePath(args, "path", s.rootDir); err != nil {
		return a, err
	}
	if a.sourceRoot, err = s.resolveOptionalPath(args, "source_root"); err != nil {
		return a, err
	}
	if a.dryRun, err = argBool(args, "dry_run", false); err != nil {
		return a, err
	}
	if a.force, err = argBool(args, "force", false); err != nil {
		return a, err
	}
	if a.recordBase, err = argBool(args, "record_baseline", true); err != nil {
		return a, err
	}
	return a, nil
}

// createAdoptTool builds the standards_adopt tool for agents.
func (s *Server) createAdoptTool() (mcp.Tool, error) {
	schema := mcp.ToolInputSchema{
		Type: "object",
		Properties: map[string]mcp.PropertySchema{
			"path": {
				Type:        "string",
				Description: "Path to repository to adopt (default: server root; other repositories require -allow-outside-root)",
			},
			"dry_run": {
				Type:        "boolean",
				Description: "Simulate adoption without writing files (default: false)",
			},
			"force": {
				Type:        "boolean",
				Description: "Overwrite existing standards configurations (default: false)",
			},
			"source_root": {Type: "string", Description: "Optional Praetor source bundle for creating a real pinned lock; confined to the server root"},
			"record_baseline": {
				Type:        "boolean",
				Description: "Record existing infractions into .standards-baseline.json (default: true)",
			},
		},
	}

	handler := func(ctx context.Context, args map[string]any) (*mcp.ToolResult, error) {
		a, err := s.parseAdoptArgs(args)
		if err != nil {
			return mcp.ErrorResult(err.Error()), nil
		}

		adoptCtx, cancel := context.WithTimeout(ctx, adoptBudget)
		defer cancel()

		report, err := adopt.Adopt(adoptCtx, adopt.AdoptOptions{
			Path:           a.path,
			LockSourceRoot: a.sourceRoot,
			DryRun:         a.dryRun,
			Force:          a.force,
			RecordBaseline: a.recordBase,
		})
		if err != nil {
			details := ""
			if report != nil {
				details = formatAdoptMCPResult(report, a.dryRun)
			}
			return mcp.ErrorResult(details + fmt.Sprintf("Adoption failed: %v", err)), nil
		}

		if len(report.Errors) != 0 {
			return mcp.ErrorResult(formatAdoptMCPResult(report, a.dryRun)), nil
		}
		return mcp.TextResult(formatAdoptMCPResult(report, a.dryRun)), nil
	}

	// Adoption installs git hooks and, with force, replaces existing configuration
	// files: destructive, and re-runnable (idempotent) once applied.
	return mcp.NewMutatingTool("standards_adopt", "Adopt any codebase into Praetor governance in 1 step", schema, handler, true, true)
}

func formatAdoptMCPResult(r *adopt.AdoptReport, dryRun bool) string {
	var sb strings.Builder
	mode := "APPLIED"
	if len(r.Errors) > 0 {
		mode = "INCOMPLETE"
	}
	if dryRun {
		mode = "SIMULATED (DRY RUN)"
	}
	fmt.Fprintf(&sb, "=== Praetor Repository Adoption [%s] ===\n", mode)
	fmt.Fprintf(&sb, "State: %s | Archetype: %s\n", r.State, r.Archetype)
	formatAdoptDebt(&sb, r, dryRun)
	fileLabel := "Created Files"
	if dryRun {
		fileLabel = "Planned Files"
	}
	fmt.Fprintf(&sb, "%s: %d\n", fileLabel, len(r.CreatedFiles))
	for _, f := range r.CreatedFiles {
		fmt.Fprintf(&sb, "  + %s\n", f)
	}
	reconciledLabel := "Reconciled Files"
	if dryRun {
		reconciledLabel = "Planned Reconciliations"
	}
	fmt.Fprintf(&sb, "%s: %d\n", reconciledLabel, len(r.ReconciledFiles))
	for _, f := range r.ReconciledFiles {
		fmt.Fprintf(&sb, "  ~ %s\n", f)
	}
	for _, failure := range r.Errors {
		fmt.Fprintf(&sb, "[ERROR] %s\n", failure)
	}
	for _, warning := range r.Warnings {
		fmt.Fprintf(&sb, "[WARN] %s\n", warning)
	}
	return sb.String()
}

func formatAdoptDebt(sb *strings.Builder, r *adopt.AdoptReport, dryRun bool) {
	switch r.BaselineStatus {
	case "scanned":
		if dryRun {
			fmt.Fprintf(sb, "Legacy Debt Scanned (dry-run; not written): %d infractions\n", r.LegacyDebtCount)
			return
		}
		fmt.Fprintf(sb, "Legacy Debt Baselined: %d infractions\n", r.LegacyDebtCount)
	case "existing":
		fmt.Fprintf(sb, "Existing Legacy Debt Baseline: %d infractions\n", r.LegacyDebtCount)
	case "skipped":
		sb.WriteString("Legacy Debt Scan: skipped (baseline recording disabled)\n")
	default:
		status := r.BaselineStatus
		if status == "" {
			status = "not_run"
		}
		fmt.Fprintf(sb, "Legacy Debt Baseline: %s; no usable result\n", status)
	}
}

// parseDogfoodOptions validates the dogfood arguments. Remote benchmark clones are an
// explicit server-level opt-in; a client argument alone cannot trigger network egress.
func (s *Server) parseDogfoodOptions(args map[string]any) (dogfood.DogfoodOptions, error) {
	var opts dogfood.DogfoodOptions
	var err error
	if opts.HostRepoPath, err = s.resolvePath(args, "host_path", s.rootDir); err != nil {
		return opts, err
	}
	if opts.TargetReposDir, err = s.resolveOptionalPath(args, "targets_dir"); err != nil {
		return opts, err
	}
	if opts.BenchmarkPopular, err = argBool(args, "benchmark_popular", false); err != nil {
		return opts, err
	}
	if opts.BenchmarkPopular && !s.opts.AllowRemoteBenchmarks {
		return opts, ErrRemoteBenchmarksDisabled
	}
	opts.SkipWorkstationSkills = true
	opts.DryRun = true
	opts.MaxScanTargets = maxDogfoodScanTargets
	return opts, nil
}

// createDogfoodTool builds the standards_dogfood tool for agents.
func (s *Server) createDogfoodTool() (mcp.Tool, error) {
	schema := mcp.ToolInputSchema{
		Type: "object",
		Properties: map[string]mcp.PropertySchema{
			"host_path": {
				Type:        "string",
				Description: "Path to host repository (default: server root)",
			},
			"targets_dir": {
				Type:        "string",
				Description: "Optional local directory containing target repos to test (confined to the server root unless -allow-outside-root)",
			},
			"public_loop":  {Type: "boolean", Description: "Run retained public clone plan/apply/recheck loop (requires server remote opt-in)"},
			"public_repos": {Type: "string", Description: "Comma-separated curated HTTPS URLs, optionally #<commit SHA>"},
			"artifact_dir": {Type: "string", Description: "Required retained evidence directory for public_loop, confined to server root"},
			"source_root":  {Type: "string", Description: "Praetor bundle with validated lock and local archetypes (default: host_path)"},
			"max_attempts": {Type: "integer", Description: "Public loop apply/recheck bound: 2 or 3 (default: 2)"},
			"dry_run":      {Type: "boolean", Description: "Public loop plans only by default; false applies inside fresh disposable clones"},
			"benchmark_popular": {
				Type:        "boolean",
				Description: "Benchmark against curated popular public OSS repositories (clones them; requires the server flag -allow-remote-benchmarks)",
			},
		},
	}

	handler := func(ctx context.Context, args map[string]any) (*mcp.ToolResult, error) {
		public, err := argBool(args, "public_loop", false)
		if err != nil {
			return mcp.ErrorResult(err.Error()), nil
		}
		if public {
			return s.runPublicDogfood(ctx, args), nil
		}
		if err := rejectPublicOnlyArgs(args); err != nil {
			return mcp.ErrorResult(err.Error()), nil
		}
		opts, err := s.parseDogfoodOptions(args)
		if err != nil {
			return mcp.ErrorResult(err.Error()), nil
		}

		dfCtx, cancel := context.WithTimeout(ctx, dogfoodBudget)
		defer cancel()

		rep, err := dogfood.RunDogfood(dfCtx, opts)
		if err != nil {
			return mcp.ErrorResult(fmt.Sprintf("Dogfood run failed: %v", err)), nil
		}

		if !rep.OverallPassed {
			return mcp.ErrorResult(formatDogfoodMCPResult(rep)), nil
		}
		return mcp.TextResult(formatDogfoodMCPResult(rep)), nil
	}

	// Dogfooding spawns Git for explicitly enabled clones and retains public-loop
	// evidence. MCP dogfooding never audits unrelated workstation skill roots.
	return mcp.NewOpenWorldTool("standards_dogfood", "Execute self-governance verification and retained public adoption loops", schema, handler, false, false)
}

func formatDogfoodMCPResult(rep *dogfood.DogfoodReport) string {
	var sb strings.Builder
	sb.WriteString("=== Praetor Universal Dogfooding Report ===\n")
	fmt.Fprintf(&sb, "Host: %s\n", rep.HostRepoPath)
	fmt.Fprintf(&sb, "Context Sync: %t | Invariants Audit: %t\n", rep.ContextSyncPassed, rep.SelfAuditPassed)
	if len(rep.TargetResults) > 0 {
		fmt.Fprintf(&sb, "Local Targets Evaluated: %d\n", len(rep.TargetResults))
		for _, tr := range rep.TargetResults {
			fmt.Fprintf(&sb, "  - %s: Archetype: %s | Debt: %d | Actions: %d\n", tr.RepoName, tr.Archetype, tr.DebtCount, tr.Actions)
		}
	}
	if len(rep.RemoteResults) > 0 {
		fmt.Fprintf(&sb, "Remote Benchmarks Evaluated: %d\n", len(rep.RemoteResults))
		for _, rr := range rep.RemoteResults {
			fmt.Fprintf(&sb, "  - %s: Grade: %s | Archetype: %s | Debt: %d | HISS: %d\n", rr.RepoURL, rr.ReadinessGrade, rr.Archetype, rr.DebtCount, rr.HISSInfractions)
		}
	}
	fmt.Fprintf(&sb, "Overall Status: %t\n", rep.OverallPassed)
	return sb.String()
}

// resolveDevDir returns the workstation directory to harvest: the dev_dir argument,
// or ~/dev when absent. Both are subject to root confinement, so the default only works
// with -allow-outside-root (or when the server root is the dev directory itself).
func (s *Server) resolveDevDir(args map[string]any) (string, error) {
	devDir, err := s.resolveOptionalPath(args, "dev_dir")
	if err != nil {
		return "", err
	}
	if devDir != "" {
		return devDir, nil
	}
	home, hErr := os.UserHomeDir()
	if hErr != nil {
		return "", fmt.Errorf("dev_dir is required: cannot resolve the home directory: %w", hErr)
	}
	return s.confinePath(filepath.Join(home, "dev"))
}

// createHarvestWorkstationTool builds the standards_harvest_workstation tool.
func (s *Server) createHarvestWorkstationTool() (mcp.Tool, error) {
	schema := mcp.ToolInputSchema{
		Type: "object",
		Properties: map[string]mcp.PropertySchema{
			"json": {Type: "boolean", Description: "Return private workstation repository observations as JSON (default false)"},
			"dev_dir": {
				Type:        "string",
				Description: "Path to developer repositories root directory (default: ~/dev; requires -allow-outside-root unless under the server root)",
			},
		},
	}

	// The harvest walks repositories outside the governed tree and runs git in each:
	// open-world, but it never writes.
	return mcp.NewOpenWorldTool("standards_harvest_workstation", "Audit workstation repositories and fleet adoption state", schema, s.runHarvestWorkstation, true, true)
}
