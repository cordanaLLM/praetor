package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/cordanaLLM/praetor/internal/harvester"
	"github.com/cordanaLLM/praetor/internal/mcp"
)

func (s *Server) runHarvestWorkstation(ctx context.Context, args map[string]any) (*mcp.ToolResult, error) {
	jsonOutput, err := argBool(args, "json", false)
	if err != nil {
		return mcp.ErrorResult(err.Error()), nil
	}
	devDir, err := s.resolveDevDir(args)
	if err != nil {
		return mcp.ErrorResult(err.Error()), nil
	}
	scanCtx, cancel := context.WithTimeout(ctx, harvestBudget)
	defer cancel()
	scan, scanErr := harvester.ScanLocalWorkstation(scanCtx, devDir)
	if scan == nil {
		return mcp.ErrorResult(fmt.Sprintf("Workstation harvest scan failed: %v", scanErr)), nil
	}
	result, err := formatWorkstationHarvest(scan, devDir, jsonOutput)
	if err != nil {
		return nil, err
	}
	result.IsError = scanErr != nil || !scan.RepositoryInventoryComplete
	return result, nil
}

func formatWorkstationHarvest(scan *harvester.WorkstationReport, devDir string, jsonOutput bool) (*mcp.ToolResult, error) {
	if jsonOutput {
		data, err := json.Marshal(scan)
		if err != nil {
			return nil, fmt.Errorf("encode workstation inventory: %w", err)
		}
		return mcp.TextResult(string(data)), nil
	}
	var sb strings.Builder
	sb.WriteString("=== Workstation Governance & Fleet Audit ===\n")
	fmt.Fprintf(&sb, "Dev Root: %s\n", devDir)
	fmt.Fprintf(&sb, "Dev Repos Total: %d\n", scan.DevReposCount)
	fmt.Fprintf(&sb, "Unmanaged (Missing Rules) Repos: %d\n", len(scan.MissingRulesRepos))
	fmt.Fprintf(&sb, "Dirty Git Repos: %d\n", len(scan.DirtyRepos))
	fmt.Fprintf(&sb, "Stale Git Worktrees: %d\n", len(scan.StaleWorktrees))
	fmt.Fprintf(&sb, "Repository inventory complete: %t (truncated: %t; errors: %d)\n", scan.RepositoryInventoryComplete, scan.RepositoryInventoryTruncated, len(scan.RepositoryInventoryErrors))
	return mcp.TextResult(sb.String()), nil
}
