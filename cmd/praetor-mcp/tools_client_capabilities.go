package main

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/cordanaLLM/praetor/internal/clientsetup"
	"github.com/cordanaLLM/praetor/internal/mcp"
)

func (s *Server) createClientCapabilitiesTool() (mcp.Tool, error) {
	return mcp.NewReadOnlyTool("standards_client_capabilities",
		"List versioned client configuration and lifecycle adapter capabilities from the shared registry. Definitions only: no workstation discovery, installation, native trust or runtime enforcement claim.",
		mcp.ToolInputSchema{Type: "object"}, s.clientCapabilities)
}

func (s *Server) clientCapabilities(ctx context.Context, args map[string]any) (*mcp.ToolResult, error) {
	if len(args) != 0 {
		return mcp.ErrorResult("client capabilities accepts no arguments"), nil
	}
	report, err := clientsetup.Capabilities(ctx)
	if err != nil {
		return mcp.ErrorResult(err.Error()), nil
	}
	data, err := json.Marshal(report)
	if err != nil {
		return nil, fmt.Errorf("encode client capabilities: %w", err)
	}
	return mcp.TextResult(string(data)), nil
}
