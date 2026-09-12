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
	optimize := fs.Bool("optimize", true, "Enable pre-build capability pruning and symbol stripping")
	if err := fs.Parse(args); err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	cfg, err := builder.LoadBuildConfig(*configPath)
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
	results, err := b.Build(ctx, cfg, *target)
	if err != nil {
		return fmt.Errorf("build execution failed: %w", err)
	}

	for _, res := range results {
		status := "[PASS]"
		if !res.Success {
			status = "[FAIL]"
		}
		fmt.Printf("%s Target: %-15s (Runtime: %-10s) [%v]\n", status, res.Target, res.Runtime, res.Duration.Round(time.Millisecond))
		if len(res.Artifacts) > 0 {
			fmt.Printf("   Artifacts: %s\n", res.Artifacts)
		}
		if res.OutputLogs != "" {
			fmt.Printf("   Logs: %s\n", res.OutputLogs)
		}
	}

	fmt.Printf("\n[PASS] Successfully compiled %d target(s).\n", len(results))
	return nil
}
