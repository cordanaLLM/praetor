package main

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/cordanaLLM/praetor/internal/dogfood"
	"github.com/cordanaLLM/praetor/internal/mcp"
)

func (s *Server) createDogfoodSuiteTool() (mcp.Tool, error) {
	schema := mcp.ToolInputSchema{Type: "object", Required: []string{"config_path", "artifact_dir"}, Properties: map[string]mcp.PropertySchema{
		"config_path":  {Type: "string", Description: "Version-1 JSON suite file; embedded transcript paths also obey server confinement"},
		"artifact_dir": {Type: "string", Description: "Private evidence directory creation path under existing parent"},
		"source_root":  {Type: "string", Description: "Praetor source bundle for public cases; default server root"},
		"stage":        {Type: "string", Description: "plan (declarations only, default) or verify (bounded execution and replay); public verify requires server remote opt-in"},
	}}
	return mcp.NewOpenWorldTool("standards_dogfood_suite", "Run configured pinned public adoption and private transcript replay cases; retain explicit completion/failure metadata without executing transcript content or upstream code", schema, s.runDogfoodSuite, false, false)
}

func (s *Server) runDogfoodSuite(ctx context.Context, args map[string]any) (*mcp.ToolResult, error) {
	opts, err := s.dogfoodSuiteArguments(args)
	if err != nil {
		return mcpErrorResult(err.Error(), mcpTextUntrusted), nil
	}
	report, runErr := dogfood.RunSuite(ctx, opts)
	result := struct {
		Report *dogfood.SuiteReport `json:"report,omitempty"`
		Error  string               `json:"error,omitempty"`
	}{Report: report}
	if runErr != nil {
		result.Error = runErr.Error()
	}
	data, err := json.Marshal(result)
	if err != nil {
		return nil, fmt.Errorf("encode suite report: %w", err)
	}
	response := mcpTextResult(string(data), mcpTextStructuredJSON)
	response.IsError = runErr != nil
	return response, nil
}

func (s *Server) dogfoodSuiteArguments(args map[string]any) (dogfood.SuiteOptions, error) {
	var opts dogfood.SuiteOptions
	if err := requireStringArguments(args); err != nil {
		return opts, err
	}
	paths := []struct {
		key      string
		target   *string
		fallback string
	}{
		{"config_path", &opts.ConfigPath, ""}, {"artifact_dir", &opts.ArtifactDir, ""}, {"source_root", &opts.SourceRoot, s.rootDir},
	}
	for _, path := range paths {
		value, err := argString(args, path.key)
		if err != nil {
			return opts, err
		}
		if value == "" {
			value = path.fallback
		}
		if value == "" {
			return opts, fmt.Errorf("%s is required", path.key)
		}
		*path.target, err = s.confinePath(value)
		if err != nil {
			return opts, err
		}
	}
	stage, err := argString(args, "stage")
	if err != nil {
		return opts, err
	}
	if stage == "" {
		stage = "plan"
	}
	opts.Stage, opts.AllowRemote, opts.Engine = stage, s.opts.AllowRemoteBenchmarks, s.opts.Build
	if !s.opts.AllowOutsideRoot {
		opts.InputRoot = s.rootDir
	}
	return opts, nil
}
