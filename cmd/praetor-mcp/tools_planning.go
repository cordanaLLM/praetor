package main

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/cordanaLLM/praetor/internal/contextopt"
	"github.com/cordanaLLM/praetor/internal/mcp"
	"github.com/cordanaLLM/praetor/internal/planning"
	"github.com/cordanaLLM/praetor/internal/util"
)

func (s *Server) createPlanningValidateTool() (mcp.Tool, error) {
	schema := mcp.ToolInputSchema{Type: "object", Required: []string{"input_path"}, Properties: map[string]mcp.PropertySchema{
		"input_path": {Type: "string", Description: "Strict planning draft JSON inside this server root"},
	}}
	return mcp.NewReadOnlyTool("standards_planning_validate",
		"Structurally validate a bounded planning draft and return metadata; source assertions remain unverified and no artifacts or project state are written",
		schema, s.validatePlanning)
}

func (s *Server) createPlanningPrepareTool() (mcp.Tool, error) {
	schema := mcp.ToolInputSchema{Type: "object", Required: []string{"input_path", "output_dir"}, Properties: map[string]mcp.PropertySchema{
		"input_path": {Type: "string", Description: "Strict planning draft JSON inside this server root"},
		"output_dir": {Type: "string", Description: "New private artifact directory below the server root .workingdir"},
	}}
	return mcp.NewMutatingTool("standards_planning_prepare",
		"Compile a structural planning proposal into a new private .workingdir artifact directory; no existing state, source verification, execution or publication",
		schema, s.preparePlanning, false, false)
}

func (s *Server) validatePlanning(ctx context.Context, args map[string]any) (*mcp.ToolResult, error) {
	if len(args) != 1 {
		return mcp.ErrorResult("exactly one input_path argument required"), nil
	}
	result, err := s.compilePlanningInput(ctx, args)
	if err != nil {
		return mcp.ErrorResult(err.Error()), nil
	}
	return planningMetadataResult(result, false)
}

func (s *Server) preparePlanning(ctx context.Context, args map[string]any) (*mcp.ToolResult, error) {
	if len(args) != 2 {
		return mcp.ErrorResult("exactly input_path and output_dir arguments required"), nil
	}
	result, err := s.compilePlanningInput(ctx, args)
	if err != nil {
		return mcp.ErrorResult(err.Error()), nil
	}
	directory, err := s.privatePlanningOutput(args)
	if err != nil {
		return mcp.ErrorResult(err.Error()), nil
	}
	if err := contextopt.WriteArtifacts(ctx, directory, result.Files); err != nil {
		return mcp.ErrorResult(fmt.Sprintf("write planning artifacts: %v", err)), nil
	}
	return planningMetadataResult(result, true)
}

func (s *Server) compilePlanningInput(ctx context.Context, args map[string]any) (*planning.Result, error) {
	path, err := planningStringArgument(args, "input_path")
	if err != nil {
		return nil, err
	}
	path, err = s.confinePlanningPath(path)
	if err != nil {
		return nil, err
	}
	raw, err := contextopt.ReadSnapshot(ctx, path)
	if err != nil {
		return nil, fmt.Errorf("read planning input: %w", err)
	}
	return planning.Compile(ctx, raw)
}

func (s *Server) privatePlanningOutput(args map[string]any) (string, error) {
	value, err := planningStringArgument(args, "output_dir")
	if err != nil {
		return "", err
	}
	directory, err := s.confinePlanningPath(value)
	if err != nil {
		return "", err
	}
	privateRoot, err := util.ConfinePath(s.rootDir, ".workingdir")
	if err != nil {
		return "", err
	}
	rel, err := filepath.Rel(privateRoot, directory)
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("output_dir must be a new directory below the server root .workingdir")
	}
	return directory, nil
}

func (s *Server) confinePlanningPath(value string) (string, error) {
	abs := filepath.Clean(value)
	if !filepath.IsAbs(abs) {
		abs = filepath.Join(s.rootDir, abs)
	}
	rel, err := filepath.Rel(s.rootDir, abs)
	if err != nil {
		return "", fmt.Errorf("resolve planning path: %w", err)
	}
	confined, err := util.ConfinePath(s.rootDir, rel)
	if err != nil {
		return "", fmt.Errorf("%w: %q: %w", ErrOutsideRoot, value, err)
	}
	return confined, nil
}

func planningStringArgument(args map[string]any, key string) (string, error) {
	value, ok := args[key].(string)
	if !ok || strings.TrimSpace(value) == "" {
		return "", fmt.Errorf("%s must be a nonempty string", key)
	}
	return value, nil
}

func planningMetadataResult(result *planning.Result, written bool) (*mcp.ToolResult, error) {
	data, err := json.Marshal(result.Report(written))
	if err != nil {
		return nil, fmt.Errorf("encode planning metadata: %w", err)
	}
	return mcp.TextResult(string(data)), nil
}
