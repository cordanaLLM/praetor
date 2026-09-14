package main

import (
	"context"
	"flag"
	"fmt"
	"time"

	"github.com/cordanaLLM/praetor/internal/builder"
)

func runBuild(args []string) error {
	fs := flag.NewFlagSet("build", flag.ContinueOnError)
	configPath := fs.String("config", ".framework-build.yaml", "Path to .framework-build.yaml manifest")
	target := fs.String("target", "all", "Specific target to build (or 'all')")
	optimize := fs.Bool("optimize", true, "Request optimization; execution backends are currently unavailable")
	if err := fs.Parse(args); err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	cfg, err := builder.LoadBuildConfigContext(ctx, *configPath)
	if err != nil {
		return fmt.Errorf("failed to load build configuration: %w", err)
	}

	// fs.Lookup always finds a registered flag, so it cannot tell "the operator passed
	// --optimize" from "the flag has its default". fs.Visit only reports flags that were
	// actually set, so `optimize: false` in the manifest survives an unflagged run.
	optimizeSet := false
	fs.Visit(func(f *flag.Flag) {
		if f.Name == "optimize" {
			optimizeSet = true
		}
	})
	if optimizeSet {
		cfg.Optimize = *optimize
	}

	fmt.Printf("=== Universal Polyglot Builder: %s ===\n", cfg.Project)
	fmt.Printf("Config: %s | Output Dir: %s | Optimize: %t\n\n", *configPath, cfg.OutputDir, cfg.Optimize)

	b := builder.NewUniversalBuilder()
	// Results accompany a refusal, so they are reported before the error is returned:
	// the per-target status and reason say which runtimes were refused and why, which the
	// joined error text alone does not separate.
	results, buildErr := b.Build(ctx, cfg, *target)
	compiled := reportBuildResults(results)
	if buildErr != nil {
		return fmt.Errorf("build execution failed: %w", buildErr)
	}

	fmt.Printf("\n[PASS] Compiled %d of %d selected target(s).\n", compiled, len(results))
	return nil
}

// reportBuildResults prints one line per inspected target and returns how many of them the
// builder actually compiled. A target counts only when the builder marked it successful, so
// a run that executed no compilation can never be summarized as a success.
func reportBuildResults(results []builder.BuildResult) int {
	compiled := 0
	for _, res := range results {
		if res.Success {
			compiled++
		}
		fmt.Printf("%s Target: %-15s (Runtime: %-10s) [%v]\n",
			buildStatusLabel(res), res.Target, res.Runtime, res.Duration.Round(time.Millisecond))
		if res.Reason != "" {
			fmt.Printf("   Reason: %s\n", res.Reason)
		}
		if len(res.Artifacts) > 0 {
			fmt.Printf("   Artifacts: %s\n", res.Artifacts)
		}
		if res.OutputLogs != "" {
			fmt.Printf("   Logs: %s\n", res.OutputLogs)
		}
	}
	return compiled
}

// buildStatusLabel maps a result onto its report label. A refused target carries its refusal
// class rather than a generic failure, so an unrecognized runtime is never reported as an
// implemented backend that broke.
func buildStatusLabel(res builder.BuildResult) string {
	switch {
	case res.Success:
		return "[PASS]"
	case res.Status == builder.BuildUnavailable:
		return "[UNAVAILABLE]"
	case res.Status == builder.BuildUnsupported:
		return "[UNSUPPORTED]"
	default:
		return "[FAIL]"
	}
}
