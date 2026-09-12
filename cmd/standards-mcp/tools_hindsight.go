// SPDX-License-Identifier: EUPL-1.2
// SPDX-FileCopyrightText: 2026 Lusoris <lusoris@proton.me>

package main

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/cordanaLLM/praetor/internal/hindsight"
	"github.com/cordanaLLM/praetor/internal/mcp"
)

// distillBudget bounds the workspace walk of standards_hindsight_optimize (HISS-02).
const distillBudget = 2 * time.Minute

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
				Description: "Repository root path (default: server root)",
			},
		},
		Required: []string{"query"},
	}

	return mcp.NewReadOnlyTool(
		"standards_memory_recall",
		"Rapid zero-token recall of verified repository facts, canonical utilities, and library rules from local memory cache",
		schema,
		s.recallMemory,
	)
}

// recallMemory is the standards_memory_recall handler.
func (s *Server) recallMemory(ctx context.Context, args map[string]any) (*mcp.ToolResult, error) {
	query, err := argString(args, "query")
	if err != nil {
		return mcp.ErrorResult(err.Error()), nil
	}
	if strings.TrimSpace(query) == "" {
		return mcp.ErrorResult("query argument is required"), nil
	}
	catStr, err := argString(args, "category")
	if err != nil {
		return mcp.ErrorResult(err.Error()), nil
	}
	repoPath, err := s.resolvePath(args, "path", s.rootDir)
	if err != nil {
		return mcp.ErrorResult(err.Error()), nil
	}
	if err := ctx.Err(); err != nil {
		return mcp.ErrorResult(fmt.Sprintf("memory recall cancelled: %v", err)), nil
	}

	facts := hindsight.RecallLocalFacts(repoPath, query, hindsight.FactCategory(catStr))
	if len(facts) == 0 {
		return mcp.TextResult(fmt.Sprintf("No local memory facts match '%s'. Run 'praetorctl hindsight distill' to refresh cache.", query)), nil
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "=== Local Memory Facts for '%s' (%d items) ===\n\n", query, len(facts))
	for _, f := range facts {
		fmt.Fprintf(&sb, "[%s] %s\n  Statement: %s\n  Evidence:  %s\n\n", f.Category, f.Subject, f.Statement, f.Evidence)
	}

	return mcp.TextResult(sb.String()), nil
}

// createHindsightOptimizeTool builds the standards_hindsight_optimize tool to distill workspace truth.
func (s *Server) createHindsightOptimizeTool() (mcp.Tool, error) {
	schema := mcp.ToolInputSchema{
		Type: "object",
		Properties: map[string]mcp.PropertySchema{
			"path": {
				Type:        "string",
				Description: "Repository root path (default: server root)",
			},
		},
	}

	handler := func(ctx context.Context, args map[string]any) (*mcp.ToolResult, error) {
		repoPath, err := s.resolvePath(args, "path", s.rootDir)
		if err != nil {
			return mcp.ErrorResult(err.Error()), nil
		}

		distillCtx, cancel := context.WithTimeout(ctx, distillBudget)
		defer cancel()

		report, err := hindsight.DistillWorkspace(distillCtx, repoPath)
		if err != nil {
			return mcp.ErrorResult(fmt.Sprintf("distillation failed: %v", err)), nil
		}
		if err := distillCtx.Err(); err != nil {
			return mcp.ErrorResult(fmt.Sprintf("distillation cancelled before saving: %v", err)), nil
		}

		if err := hindsight.SaveLocalCache(repoPath, report.Facts); err != nil {
			return mcp.ErrorResult(fmt.Sprintf("failed saving local cache: %v", err)), nil
		}

		var sb strings.Builder
		fmt.Fprintf(&sb, "Successfully distilled %d atomic facts across workspace subsystems:\n", report.TotalFacts)
		for cat, count := range report.Categories {
			fmt.Fprintf(&sb, "  - %-20s: %d\n", cat, count)
		}
		sb.WriteString("Updated .workingdir/memory/distilled.json.\n")

		return mcp.TextResult(sb.String()), nil
	}

	// The distilled cache file is replaced on every run: destructive, idempotent.
	return mcp.NewMutatingTool(
		"standards_hindsight_optimize",
		"Harvest and distill verified repository facts into local memory cache and Hindsight knowledge pages",
		schema,
		handler,
		true,
		true,
	)
}
