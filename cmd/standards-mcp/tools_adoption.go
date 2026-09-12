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
	"github.com/cordanaLLM/praetor/internal/harvester"
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
}

// parseAdoptArgs validates the adoption arguments; the target path is confined to the
// server root unless -allow-outside-root is set.
func (s *Server) parseAdoptArgs(args map[string]any) (adoptArgs, error) {
	var a adoptArgs
	var err error
	if a.path, err = s.resolvePath(args, "path", s.rootDir); err != nil {
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
			DryRun:         a.dryRun,
			Force:          a.force,
			RecordBaseline: a.recordBase,
		})
		if err != nil {
			return mcp.ErrorResult(fmt.Sprintf("Adoption failed: %v", err)), nil
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
	if dryRun {
		mode = "SIMULATED (DRY RUN)"
	}
	fmt.Fprintf(&sb, "=== Praetor Repository Adoption [%s] ===\n", mode)
	fmt.Fprintf(&sb, "State: %s | Archetype: %s\n", r.State, r.Archetype)
	fmt.Fprintf(&sb, "Legacy Debt Recorded: %d infractions\n", r.LegacyDebtCount)
	fmt.Fprintf(&sb, "Created Files: %d\n", len(r.CreatedFiles))
	for _, f := range r.CreatedFiles {
		fmt.Fprintf(&sb, "  + %s\n", f)
	}
	fmt.Fprintf(&sb, "Reconciled Files: %d\n", len(r.ReconciledFiles))
	for _, f := range r.ReconciledFiles {
		fmt.Fprintf(&sb, "  ~ %s\n", f)
	}
	return sb.String()
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
			"benchmark_popular": {
				Type:        "boolean",
				Description: "Benchmark against curated popular public OSS repositories (clones them; requires the server flag -allow-remote-benchmarks)",
			},
		},
	}

	handler := func(ctx context.Context, args map[string]any) (*mcp.ToolResult, error) {
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

		return mcp.TextResult(formatDogfoodMCPResult(rep)), nil
	}

	// Dogfooding scans the workstation's agent skill roots, spawns git for clones and
	// writes ephemeral checkouts under the temp directory: open-world, not read-only.
	return mcp.NewOpenWorldTool("standards_dogfood", "Execute self-governance verification and adoption benchmarking", schema, handler, false, true)
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
			"dev_dir": {
				Type:        "string",
				Description: "Path to developer repositories root directory (default: ~/dev; requires -allow-outside-root unless under the server root)",
			},
		},
	}

	handler := func(ctx context.Context, args map[string]any) (*mcp.ToolResult, error) {
		devDir, err := s.resolveDevDir(args)
		if err != nil {
			return mcp.ErrorResult(err.Error()), nil
		}

		scanCtx, cancel := context.WithTimeout(ctx, harvestBudget)
		defer cancel()

		scan, err := harvester.ScanLocalWorkstation(scanCtx, devDir)
		if err != nil {
			return mcp.ErrorResult(fmt.Sprintf("Workstation harvest scan failed: %v", err)), nil
		}

		var sb strings.Builder
		sb.WriteString("=== Workstation Governance & Fleet Audit ===\n")
		fmt.Fprintf(&sb, "Dev Root: %s\n", devDir)
		fmt.Fprintf(&sb, "Dev Repos Total: %d\n", scan.DevReposCount)
		fmt.Fprintf(&sb, "Unmanaged (Missing Rules) Repos: %d\n", len(scan.MissingRulesRepos))
		fmt.Fprintf(&sb, "Dirty Git Repos: %d\n", len(scan.DirtyRepos))
		fmt.Fprintf(&sb, "Stale Git Worktrees: %d\n", len(scan.StaleWorktrees))
		return mcp.TextResult(sb.String()), nil
	}

	// The harvest walks repositories outside the governed tree and runs git in each:
	// open-world, but it never writes.
	return mcp.NewOpenWorldTool("standards_harvest_workstation", "Audit workstation repositories and fleet adoption state", schema, handler, true, true)
}
