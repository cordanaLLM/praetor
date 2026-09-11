// SPDX-License-Identifier: EUPL-1.2
// SPDX-FileCopyrightText: 2026 Lusoris <lusoris@proton.me>

package main

import (
	"context"
	"fmt"
	"strings"

	"github.com/cordanaLLM/praetor/internal/hindsight"
	"github.com/cordanaLLM/praetor/internal/mcp"
)

// createMemoryRecallTool builds the standards_memory_recall tool for sub-millisecond local truth.
func (s *Server) createMemoryRecallTool() (mcp.Tool, error) {
	schema := mcp.ToolInputSchema{
		Type: "object",
		Properties: map[string]mcp.PropertySchema{
			"query": {
				Type:        "string",
				Description: "Keyword or topic query (e.g. 'yaml', 'ast dedupe', 'governance', 'flavor')",
			},
			"category": {
				Type:        "string",
				Description: "Optional category filter ('governance', 'architecture', 'canonical_utility', 'bug_ruling', 'dependency_doc', 'flavor')",
			},
			"path": {
				Type:        "string",
				Description: "Repository root path (default: workspace root)",
			},
		},
		Required: []string{"query"},
	}

	handler := func(ctx context.Context, args map[string]any) (*mcp.ToolResult, error) {
		query, _ := args["query"].(string)
		catStr, _ := args["category"].(string)
		repoPath := s.resolvePath(args, "path", s.rootDir)

		facts := hindsight.RecallLocalFacts(repoPath, query, hindsight.FactCategory(catStr))
		if len(facts) == 0 {
			return mcp.TextResult(fmt.Sprintf("No local memory facts match '%s'. Run 'standardsctl hindsight distill' to refresh cache.", query)), nil
		}

		var sb strings.Builder
		sb.WriteString(fmt.Sprintf("=== Local Memory Facts for '%s' (%d items) ===\n\n", query, len(facts)))
		for _, f := range facts {
			sb.WriteString(fmt.Sprintf("[%s] %s\n  Statement: %s\n  Evidence:  %s\n\n", f.Category, f.Subject, f.Statement, f.Evidence))
		}

		return mcp.TextResult(sb.String()), nil
	}

	return mcp.NewReadOnlyTool(
		"standards_memory_recall",
		"Rapid zero-token recall of verified repository facts, canonical utilities, and library rules from local memory cache",
		schema,
		handler,
	)
}

// createHindsightOptimizeTool builds the standards_hindsight_optimize tool to distill workspace truth.
func (s *Server) createHindsightOptimizeTool() (mcp.Tool, error) {
	schema := mcp.ToolInputSchema{
		Type: "object",
		Properties: map[string]mcp.PropertySchema{
			"path": {
				Type:        "string",
				Description: "Repository root path (default: workspace root)",
			},
		},
	}

	handler := func(ctx context.Context, args map[string]any) (*mcp.ToolResult, error) {
		repoPath := s.resolvePath(args, "path", s.rootDir)

		report, err := hindsight.DistillWorkspace(ctx, repoPath)
		if err != nil {
			return mcp.ErrorResult(fmt.Sprintf("distillation failed: %v", err)), nil
		}

		if err := hindsight.SaveLocalCache(repoPath, report.Facts); err != nil {
			return mcp.ErrorResult(fmt.Sprintf("failed saving local cache: %v", err)), nil
		}

		var sb strings.Builder
		sb.WriteString(fmt.Sprintf("Successfully distilled %d atomic facts across workspace subsystems:\n", report.TotalFacts))
		for cat, count := range report.Categories {
			sb.WriteString(fmt.Sprintf("  - %-20s: %d\n", cat, count))
		}
		sb.WriteString("Updated .workingdir/memory/distilled.json.\n")

		return mcp.TextResult(sb.String()), nil
	}

	return mcp.NewMutatingTool(
		"standards_hindsight_optimize",
		"Harvest and distill verified repository facts into local memory cache and Hindsight knowledge pages",
		schema,
		handler,
		false,
		true,
	)
}
