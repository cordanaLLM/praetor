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

// createAdoptTool builds the standards_adopt tool for agents.
func (s *Server) createAdoptTool() (mcp.Tool, error) {
	schema := mcp.ToolInputSchema{
		Type: "object",
		Properties: map[string]mcp.PropertySchema{
			"path": {
				Type:        "string",
				Description: "Path to repository to adopt (default: workspace root)",
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
		targetPath := s.resolvePath(args, "path", s.rootDir)
		dryRun, _ := args["dry_run"].(bool)
		force, _ := args["force"].(bool)
		recordBase := true
		if rb, ok := args["record_baseline"].(bool); ok {
			recordBase = rb
		}

		adoptCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
		defer cancel()

		opts := adopt.AdoptOptions{
			Path:           targetPath,
			DryRun:         dryRun,
			Force:          force,
			RecordBaseline: recordBase,
		}

		report, err := adopt.Adopt(adoptCtx, opts)
		if err != nil {
			return mcp.ErrorResult(fmt.Sprintf("Adoption failed: %v", err)), nil
		}

		return mcp.TextResult(formatAdoptMCPResult(report, dryRun)), nil
	}

	return mcp.NewMutatingTool("standards_adopt", "Adopt any codebase into Praetor governance in 1 step", schema, handler, false, true)
}

func formatAdoptMCPResult(r *adopt.AdoptReport, dryRun bool) string {
	var sb strings.Builder
	mode := "APPLIED"
	if dryRun {
		mode = "SIMULATED (DRY RUN)"
	}
	sb.WriteString(fmt.Sprintf("=== Praetor Repository Adoption [%s] ===\n", mode))
	sb.WriteString(fmt.Sprintf("State: %s | Archetype: %s\n", r.State, r.Archetype))
	sb.WriteString(fmt.Sprintf("Legacy Debt Recorded: %d infractions\n", r.LegacyDebtCount))
	sb.WriteString(fmt.Sprintf("Created Files: %d\n", len(r.CreatedFiles)))
	for _, f := range r.CreatedFiles {
		sb.WriteString(fmt.Sprintf("  + %s\n", f))
	}
	sb.WriteString(fmt.Sprintf("Reconciled Files: %d\n", len(r.ReconciledFiles)))
	for _, f := range r.ReconciledFiles {
		sb.WriteString(fmt.Sprintf("  ~ %s\n", f))
	}
	return sb.String()
}

// createDogfoodTool builds the standards_dogfood tool for agents.
func (s *Server) createDogfoodTool() (mcp.Tool, error) {
	schema := mcp.ToolInputSchema{
		Type: "object",
		Properties: map[string]mcp.PropertySchema{
			"host_path": {
				Type:        "string",
				Description: "Path to host repository (default: workspace root)",
			},
			"targets_dir": {
				Type:        "string",
				Description: "Optional local directory containing target repos to test",
			},
			"benchmark_popular": {
				Type:        "boolean",
				Description: "Benchmark against curated popular public OSS repositories",
			},
		},
	}

	handler := func(ctx context.Context, args map[string]any) (*mcp.ToolResult, error) {
		hostPath := s.resolvePath(args, "host_path", s.rootDir)
		targetsDir, _ := args["targets_dir"].(string)
		benchmarkPopular, _ := args["benchmark_popular"].(bool)

		dfCtx, cancel := context.WithTimeout(ctx, 3*time.Minute)
		defer cancel()

		opts := dogfood.DogfoodOptions{
			HostRepoPath:     hostPath,
			TargetReposDir:   targetsDir,
			BenchmarkPopular: benchmarkPopular,
			MaxScanTargets:   10,
		}

		rep, err := dogfood.RunDogfood(dfCtx, opts)
		if err != nil {
			return mcp.ErrorResult(fmt.Sprintf("Dogfood run failed: %v", err)), nil
		}

		return mcp.TextResult(formatDogfoodMCPResult(rep)), nil
	}

	return mcp.NewReadOnlyTool("standards_dogfood", "Execute self-governance verification and adoption benchmarking", schema, handler)
}

func formatDogfoodMCPResult(rep *dogfood.DogfoodReport) string {
	var sb strings.Builder
	sb.WriteString("=== Praetor Universal Dogfooding Report ===\n")
	sb.WriteString(fmt.Sprintf("Host: %s\n", rep.HostRepoPath))
	sb.WriteString(fmt.Sprintf("Context Sync: %t | Invariants Audit: %t\n", rep.ContextSyncPassed, rep.SelfAuditPassed))
	if len(rep.TargetResults) > 0 {
		sb.WriteString(fmt.Sprintf("Local Targets Evaluated: %d\n", len(rep.TargetResults)))
		for _, tr := range rep.TargetResults {
			sb.WriteString(fmt.Sprintf("  - %s: Archetype: %s | Debt: %d | Actions: %d\n", tr.RepoName, tr.Archetype, tr.DebtCount, tr.Actions))
		}
	}
	if len(rep.RemoteResults) > 0 {
		sb.WriteString(fmt.Sprintf("Remote Benchmarks Evaluated: %d\n", len(rep.RemoteResults)))
		for _, rr := range rep.RemoteResults {
			sb.WriteString(fmt.Sprintf("  - %s: Grade: %s | Archetype: %s | Debt: %d | HISS: %d\n", rr.RepoURL, rr.ReadinessGrade, rr.Archetype, rr.DebtCount, rr.HISSInfractions))
		}
	}
	sb.WriteString(fmt.Sprintf("Overall Status: %t\n", rep.OverallPassed))
	return sb.String()
}

// createHarvestWorkstationTool builds the standards_harvest_workstation tool.
func (s *Server) createHarvestWorkstationTool() (mcp.Tool, error) {
	schema := mcp.ToolInputSchema{
		Type: "object",
		Properties: map[string]mcp.PropertySchema{
			"dev_dir": {
				Type:        "string",
				Description: "Path to developer repositories root directory (default: ~/dev)",
			},
		},
	}

	handler := func(ctx context.Context, args map[string]any) (*mcp.ToolResult, error) {
		devDir, _ := args["dev_dir"].(string)
		if devDir == "" {
			home, hErr := os.UserHomeDir()
			if hErr == nil {
				devDir = filepath.Join(home, "dev")
			}
		}

		scanCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
		defer cancel()

		scan, err := harvester.ScanLocalWorkstation(scanCtx, devDir)
		if err != nil {
			return mcp.ErrorResult(fmt.Sprintf("Workstation harvest scan failed: %v", err)), nil
		}

		var sb strings.Builder
		sb.WriteString("=== Workstation Governance & Fleet Audit ===\n")
		sb.WriteString(fmt.Sprintf("Dev Root: %s\n", devDir))
		sb.WriteString(fmt.Sprintf("Dev Repos Total: %d\n", scan.DevReposCount))
		sb.WriteString(fmt.Sprintf("Unmanaged (Missing Rules) Repos: %d\n", len(scan.MissingRulesRepos)))
		sb.WriteString(fmt.Sprintf("Dirty Git Repos: %d\n", len(scan.DirtyRepos)))
		sb.WriteString(fmt.Sprintf("Stale Git Worktrees: %d\n", len(scan.StaleWorktrees)))
		return mcp.TextResult(sb.String()), nil
	}

	return mcp.NewReadOnlyTool("standards_harvest_workstation", "Audit workstation repositories and fleet adoption state", schema, handler)
}
