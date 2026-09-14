package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/cordanaLLM/praetor/internal/dogfood"
	"github.com/cordanaLLM/praetor/internal/mcp"
)

func (s *Server) runPublicDogfood(ctx context.Context, args map[string]any) *mcp.ToolResult {
	opts, err := s.parsePublicDogfood(args)
	if err != nil {
		return mcp.ErrorResult(err.Error())
	}
	report, runErr := dogfood.RunPublicLoop(ctx, opts)
	if report == nil {
		return mcp.ErrorResult(fmt.Sprintf("Public dogfood: %v", runErr))
	}
	data, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return mcp.ErrorResult(fmt.Sprintf("Encode public dogfood: %v", err))
	}
	if runErr != nil {
		return mcp.ErrorResult(string(data) + "\n" + runErr.Error())
	}
	return mcp.TextResult(string(data))
}

func (s *Server) parsePublicDogfood(args map[string]any) (dogfood.PublicLoopOptions, error) {
	var opts dogfood.PublicLoopOptions
	if !s.opts.AllowRemoteBenchmarks {
		return opts, ErrRemoteBenchmarksDisabled
	}
	for _, name := range []string{"targets_dir", "benchmark_popular"} {
		if _, present := args[name]; present {
			return opts, fmt.Errorf("%s is incompatible with public_loop", name)
		}
	}
	host, err := s.resolvePath(args, "host_path", s.rootDir)
	if err != nil {
		return opts, err
	}
	if opts.SourceRoot, err = s.resolvePath(args, "source_root", host); err != nil {
		return opts, err
	}
	if opts.ArtifactDir, err = s.resolveOptionalPath(args, "artifact_dir"); err != nil {
		return opts, err
	}
	repos, err := argString(args, "public_repos")
	if err != nil {
		return opts, err
	}
	if len(repos) > 32768 {
		return opts, fmt.Errorf("public_repos exceeds 32768 bytes")
	}
	if repos != "" {
		opts.Repositories = strings.Split(repos, ",")
	}
	dryRun, err := argBool(args, "dry_run", true)
	if err != nil {
		return opts, err
	}
	opts.Apply = !dryRun
	opts.MaxAttempts, err = publicAttemptArgument(args)
	return opts, err
}

func publicAttemptArgument(args map[string]any) (int, error) {
	value, ok := args["max_attempts"]
	if !ok {
		return 2, nil
	}
	switch number := value.(type) {
	case float64:
		if number == 2 || number == 3 {
			return int(number), nil
		}
	case int:
		if number == 2 || number == 3 {
			return number, nil
		}
	}
	return 0, fmt.Errorf("max_attempts must be the integer 2 or 3")
}

func rejectPublicOnlyArgs(args map[string]any) error {
	for _, key := range []string{"public_repos", "source_root", "artifact_dir", "max_attempts", "dry_run"} {
		if _, present := args[key]; present {
			return fmt.Errorf("%s requires public_loop=true", key)
		}
	}
	return nil
}
