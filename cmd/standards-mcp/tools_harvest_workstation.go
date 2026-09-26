package main

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/cordanaLLM/praetor/internal/harvester"
	"github.com/cordanaLLM/praetor/internal/mcp"
)

func (s *Server) runHarvestWorkstation(ctx context.Context, args map[string]any) (*mcp.ToolResult, error) {
	jsonOutput, err := argBool(args, "json", false)
	if err != nil {
		return mcpErrorResult(err.Error(), mcpTextUntrusted), nil
	}
	devDir, err := s.resolveDevDir(args)
	if err != nil {
		return mcpErrorResult(err.Error(), mcpTextUntrusted), nil
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
		return mcpTextResult(string(data), mcpTextStructuredJSON), nil
	}
	var sb mcpTextBuilder
	sb.Template("audit: workstation governance.\ndev_root: %s.\nrepositories_total: %d.\nrepositories_unmanaged: %d.\nrepositories_dirty: %d.\nworktrees_stale: %d.\ninventory_complete: %t; truncated: %t; errors: %d.\n",
		devDir, scan.DevReposCount, len(scan.MissingRulesRepos), len(scan.DirtyRepos),
		len(scan.StaleWorktrees), scan.RepositoryInventoryComplete,
		scan.RepositoryInventoryTruncated, len(scan.RepositoryInventoryErrors))
	return mcpComposedTextResult(sb.Text()), nil
}
