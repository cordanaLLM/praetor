// SPDX-License-Identifier: EUPL-1.2
// SPDX-FileCopyrightText: 2026 Lusoris <lusoris@proton.me>

package main

import (
	"context"
	"fmt"
	"path/filepath"
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
	if _, err := s.confinePath(filepath.Join(repoPath, hindsight.MemoryFileRel)); err != nil {
		return mcp.ErrorResult(err.Error()), nil
	}
	if err := ctx.Err(); err != nil {
		return mcp.ErrorResult(fmt.Sprintf("memory recall cancelled: %v", err)), nil
	}

	facts, err := hindsight.RecallLocalFactsContext(ctx, repoPath, query, hindsight.FactCategory(catStr))
	if err != nil {
		return mcp.ErrorResult(fmt.Sprintf("memory recall failed: %v", err)), nil
	}
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
		if _, err := s.confinePath(filepath.Join(repoPath, hindsight.MemoryFileRel)); err != nil {
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

		if err := hindsight.SaveLocalCacheContext(distillCtx, repoPath, report.Facts); err != nil {
			return mcp.ErrorResult(fmt.Sprintf("failed saving local cache: %v", err)), nil
		}

		return mcp.TextResult(formatDistillationReport(report)), nil
	}

	// The distilled cache file is replaced on every run: destructive, idempotent.
	return mcp.NewMutatingTool(
		"standards_hindsight_optimize",
		"Harvest and distill verified repository facts into the local memory cache, naming every fact source that failed",
		schema,
		handler,
		true,
		true,
	)
}

// formatDistillationReport renders a saved distillation. A report carrying warnings is
// headed as partial, so a caller cannot read a run that lost sources as a clean success.
func formatDistillationReport(report *hindsight.DistillationReport) string {
	var sb strings.Builder
	if len(report.Warnings) == 0 {
		fmt.Fprintf(&sb, "Successfully distilled %d atomic facts across workspace subsystems:\n", report.TotalFacts)
	} else {
		fmt.Fprintf(&sb, "Partially distilled %d atomic facts; failed fact sources: %d\n", report.TotalFacts, len(report.Warnings))
	}
	for _, cat := range hindsight.SortedCategories(report.Categories) {
		fmt.Fprintf(&sb, "  - %-20s: %d\n", cat, report.Categories[cat])
	}
	for _, warning := range report.Warnings {
		fmt.Fprintf(&sb, "  Warning: %s\n", warning)
	}
	sb.WriteString("Updated .workingdir/memory/distilled.json.\n")
	return sb.String()
}
