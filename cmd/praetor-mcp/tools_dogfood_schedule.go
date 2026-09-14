package main

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/cordanaLLM/praetor/internal/dogfood"
	"github.com/cordanaLLM/praetor/internal/mcp"
)

func (s *Server) createDogfoodScheduleStatusTool() (mcp.Tool, error) {
	schema := mcp.ToolInputSchema{Type: "object", Required: []string{"config_path"}, Properties: map[string]mcp.PropertySchema{
		"config_path": {Type: "string", Description: "Private version-1 schedule JSON; every embedded read path obeys server confinement"},
	}}
	return mcp.NewReadOnlyTool("standards_dogfood_schedule_status", "Inspect local dogfood schedule admission and retained attempt state without running suites, creating files or dispatching agents", schema, s.runDogfoodScheduleStatus)
}

func (s *Server) runDogfoodScheduleStatus(ctx context.Context, args map[string]any) (*mcp.ToolResult, error) {
	value, ok := args["config_path"].(string)
	if len(args) != 1 || !ok || value == "" {
		return mcp.ErrorResult("schedule status requires exactly one nonempty string config_path"), nil
	}
	path, err := s.confinePath(value)
	if err != nil {
		return mcp.ErrorResult(err.Error()), nil
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	var report *dogfood.ScheduleReport
	if s.opts.AllowOutsideRoot {
		report, err = dogfood.ScheduleStatus(ctx, path)
	} else {
		report, err = dogfood.ScheduleStatusWithinRoot(ctx, path, s.rootDir)
	}
	if err != nil {
		return mcp.ErrorResult(err.Error()), nil
	}
	data, err := json.Marshal(report)
	if err != nil {
		return nil, fmt.Errorf("encode schedule status: %w", err)
	}
	return mcp.TextResult(string(data)), nil
}
