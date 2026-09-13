package main

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/cordanaLLM/praetor/internal/mcp"
	"github.com/cordanaLLM/praetor/internal/repairrun"
)

func (s *Server) createDogfoodRepairStatusTool() (mcp.Tool, error) {
	schema := mcp.ToolInputSchema{Type: "object", Required: []string{"config_path", "report_path"}, Properties: map[string]mcp.PropertySchema{
		"config_path": {Type: "string", Description: "Private repair execution configuration, confined with every embedded input to this server root"},
		"report_path": {Type: "string", Description: "Retained completed dogfood suite report inside this server root"},
	}}
	return mcp.NewReadOnlyTool("standards_dogfood_repair_status", "Inspect bounded local dogfood repair admission and retained outcomes without running repairs, reading credentials, creating files or dispatching providers", schema, s.runDogfoodRepairStatus)
}

func (s *Server) runDogfoodRepairStatus(ctx context.Context, args map[string]any) (*mcp.ToolResult, error) {
	config, configOK := args["config_path"].(string)
	reportPath, reportOK := args["report_path"].(string)
	if len(args) != 2 || !configOK || !reportOK || config == "" || reportPath == "" {
		return mcp.ErrorResult("repair status requires exactly nonempty string config_path and report_path"), nil
	}
	config, err := s.confinePath(config)
	if err != nil {
		return mcp.ErrorResult(err.Error()), nil
	}
	reportPath, err = s.confinePath(reportPath)
	if err != nil {
		return mcp.ErrorResult(err.Error()), nil
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	report, err := repairrun.StatusWithinRoot(ctx, config, reportPath, s.rootDir)
	if err != nil {
		return mcp.ErrorResult(err.Error()), nil
	}
	data, err := json.Marshal(report)
	if err != nil {
		return nil, fmt.Errorf("encode repair status: %w", err)
	}
	return mcp.TextResult(string(data)), nil
}
