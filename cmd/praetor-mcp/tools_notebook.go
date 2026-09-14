package main

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/cordanaLLM/praetor/internal/contextopt"
	"github.com/cordanaLLM/praetor/internal/mcp"
	"github.com/cordanaLLM/praetor/internal/notebook"
)

func (s *Server) createNotebookPrepareTool() (mcp.Tool, error) {
	schema := mcp.ToolInputSchema{Type: "object", Required: []string{"bundle_path"}, Properties: map[string]mcp.PropertySchema{
		"bundle_path": {Type: "string", Description: "Explicit local NotebookLM source snapshot inside this server root"},
	}}
	return mcp.NewReadOnlyTool("standards_notebook_prepare", "Validate a bounded NotebookLM snapshot and report planning preparation metadata; no source text, network, generation or writes", schema, s.prepareNotebook)
}

func (s *Server) prepareNotebook(ctx context.Context, args map[string]any) (*mcp.ToolResult, error) {
	path, ok := args["bundle_path"].(string)
	if len(args) != 1 || !ok || path == "" {
		return mcp.ErrorResult("exactly one nonempty bundle_path required"), nil
	}
	path, err := s.confinePath(path)
	if err != nil {
		return mcp.ErrorResult(err.Error()), nil
	}
	raw, err := contextopt.ReadSnapshot(ctx, path)
	if err != nil {
		return mcp.ErrorResult(err.Error()), nil
	}
	pack, err := notebook.Prepare(ctx, raw)
	if err != nil {
		return mcp.ErrorResult(err.Error()), nil
	}
	data, err := json.Marshal(pack)
	if err != nil {
		return nil, fmt.Errorf("encode notebook report: %w", err)
	}
	return mcp.TextResult(string(data)), nil
}
