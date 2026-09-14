// SPDX-License-Identifier: EUPL-1.2
// SPDX-FileCopyrightText: 2026 Lusoris <lusoris@proton.me>

package main

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/cordanaLLM/praetor/internal/contextopt"
	"github.com/cordanaLLM/praetor/internal/mcp"
)

func (s *Server) createContextAnalyzeTool() (mcp.Tool, error) {
	schema := mcp.ToolInputSchema{Type: "object", Required: []string{"sources"},
		Properties: map[string]mcp.PropertySchema{
			"root":    {Type: "string", Description: "Selected source root, confined to server root by default"},
			"sources": {Type: "array", Description: "1..64 explicit, unique relative UTF-8 text file paths; no symlinks; 1 MiB/file, 8 MiB total"},
		}}
	return mcp.NewReadOnlyTool("standards_context_analyze",
		"Analyze exact duplicates and verified compiler projections in selected context files. Return hashes, aliases and byte counts only; no writes, activation, contents, token estimates or per-turn savings claims.",
		schema, s.analyzeContext)
}

func (s *Server) analyzeContext(ctx context.Context, args map[string]any) (*mcp.ToolResult, error) {
	root, err := s.resolvePath(args, "root", s.rootDir)
	if err != nil {
		return mcp.ErrorResult(err.Error()), nil
	}
	sources, err := contextSourceArguments(args)
	if err != nil {
		return mcp.ErrorResult(err.Error()), nil
	}
	plan, err := contextopt.Analyze(ctx, contextopt.Options{Root: root, Sources: sources})
	if err != nil {
		return mcp.ErrorResult(err.Error()), nil
	}
	data, err := json.Marshal(plan.Metadata())
	if err != nil {
		return nil, fmt.Errorf("encode context metadata: %w", err)
	}
	return mcp.TextResult(string(data)), nil
}

func contextSourceArguments(args map[string]any) ([]string, error) {
	if len(args) > 2 {
		return nil, fmt.Errorf("only root and sources arguments are accepted")
	}
	for key := range args {
		if key != "root" && key != "sources" {
			return nil, fmt.Errorf("only root and sources arguments are accepted; this tool never writes candidates")
		}
	}
	raw, ok := args["sources"].([]any)
	if !ok || len(raw) == 0 || len(raw) > contextopt.MaxSources {
		return nil, fmt.Errorf("sources must be an array of 1..%d file paths", contextopt.MaxSources)
	}
	sources := make([]string, len(raw))
	for i := 0; i < len(raw); i++ {
		name, ok := raw[i].(string)
		if !ok {
			return nil, fmt.Errorf("source %d must be a string", i+1)
		}
		sources[i] = name
	}
	return sources, nil
}
