package main

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"

	"github.com/cordanaLLM/praetor/internal/dogfood"
	"github.com/cordanaLLM/praetor/internal/mcp"
	"github.com/cordanaLLM/praetor/internal/util"
)

func (s *Server) createDogfoodDiscoveryTool() (mcp.Tool, error) {
	schema := mcp.ToolInputSchema{Type: "object", Required: []string{"policy_path", "artifact_dir"}, Properties: map[string]mcp.PropertySchema{
		"config_path":  {Type: "string", Description: "Pinned public cohort; exclusive with path"},
		"path":         {Type: "string", Description: "Explicit local source tree; exclusive with config_path"},
		"policy_path":  {Type: "string", Description: "Reviewed capability detection rules, never loaded from observed repositories"},
		"artifact_dir": {Type: "string", Description: "New private evidence directory under an existing parent"},
		"stage":        {Type: "string", Description: "plan (default) or observe; public observation needs server remote opt-in"},
	}}
	return mcp.NewOpenWorldTool("standards_dogfood_discover", "Observe selected Praetor capability gaps in local or pinned public source; retain deduplicated review candidates without upstream execution or automatic repairs", schema, s.runDogfoodDiscovery, false, false)
}

func (s *Server) runDogfoodDiscovery(ctx context.Context, args map[string]any) (*mcp.ToolResult, error) {
	opts, err := s.discoveryArguments(args)
	if err != nil {
		return mcp.ErrorResult(err.Error()), nil
	}
	report, runErr := dogfood.RunDiscovery(ctx, opts)
	response := discoveryToolSummary(report, runErr)
	data, err := json.Marshal(response)
	if err != nil {
		return nil, fmt.Errorf("encode discovery result: %w", err)
	}
	result := mcp.TextResult(string(data))
	result.IsError = runErr != nil
	return result, nil
}

// Keep source paths and per-file evidence in retained case files, not repeated
// in every tool response. CLI and MCP use the same report and evaluator.
func discoveryToolSummary(report *dogfood.DiscoveryReport, runErr error) map[string]any {
	result := map[string]any{"verified": false}
	if runErr != nil {
		result["error"] = util.TruncateExcerpt(runErr.Error(), 2048)
		result["error_truncated"] = len(runErr.Error()) > 2048
	}
	if report == nil {
		return result
	}
	result["report_path"] = filepath.Join(report.Options.ArtifactDir, "report.json")
	result["status"], result["complete"] = report.Status, report.Complete
	result["requested_cases"], result["candidate_count"] = len(report.Cases), len(report.Candidates)
	counts := make(map[string]int)
	for i := 0; i < len(report.Cases) && i < dogfood.MaxDiscoveryRepositories; i++ {
		counts[report.Cases[i].Status]++
	}
	result["case_statuses"] = counts
	result["policy_sha256"], result["config_sha256"] = report.PolicySHA256, report.ConfigSHA256
	result["scope"] = report.Scope
	return result
}

func (s *Server) discoveryArguments(args map[string]any) (dogfood.DiscoveryOptions, error) {
	opts := dogfood.DiscoveryOptions{Stage: "plan", Concurrency: 4, AllowRemote: s.opts.AllowRemoteBenchmarks}
	if err := validateDiscoveryArguments(args); err != nil {
		return opts, err
	}
	paths := []struct {
		key    string
		target *string
	}{{"path", &opts.Path}, {"config_path", &opts.ConfigPath}, {"policy_path", &opts.PolicyPath}, {"artifact_dir", &opts.ArtifactDir}}
	for _, path := range paths {
		value, err := argString(args, path.key)
		if err != nil {
			return opts, err
		}
		if value == "" {
			continue
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
	if stage != "" {
		opts.Stage = stage
	}
	return opts, nil
}

func validateDiscoveryArguments(args map[string]any) error {
	if len(args) > 5 {
		return fmt.Errorf("discovery accepts at most five arguments")
	}
	for key, value := range args {
		if key != "path" && key != "config_path" && key != "policy_path" && key != "artifact_dir" && key != "stage" {
			return fmt.Errorf("unsupported discovery argument %q", key)
		}
		if _, ok := value.(string); !ok {
			return fmt.Errorf("discovery argument %s must be a string", key)
		}
	}
	return nil
}
