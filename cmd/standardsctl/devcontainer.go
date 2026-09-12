package main

import (
	"context"
	"flag"
	"fmt"
	"time"

	"github.com/cordanaLLM/praetor/internal/config"
	"github.com/cordanaLLM/praetor/internal/devcontainer"
)

func runDevContainer(args []string) error {
	fs := flag.NewFlagSet("devcontainer", flag.ContinueOnError)
	configPath := fs.String("config", ".standards.yaml", "Path to .standards.yaml")
	outputPath := fs.String("output", ".devcontainer/devcontainer.json", "Target path for devcontainer.json")
	verify := fs.Bool("verify", false, "Verify that target devcontainer.json matches declared standards")

	positional, err := parseInterspersed(fs, args)
	if err != nil {
		return err
	}
	action := positionalAt(positional, 0, "generate")
	if action != "generate" && action != "verify" {
		return fmt.Errorf("unknown devcontainer action: %s (supported: generate, verify)", action)
	}

	manifest, err := config.LoadManifest(*configPath)
	if err != nil {
		return fmt.Errorf("failed to load manifest: %w", err)
	}

	dc, err := devcontainer.Synthesize(manifest)
	if err != nil {
		return fmt.Errorf("failed to synthesize devcontainer: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if *verify || action == "verify" {
		fmt.Printf("Verifying %s against %s...\n", *outputPath, *configPath)
		if err := devcontainer.Verify(ctx, *outputPath, dc); err != nil {
			return fmt.Errorf("devcontainer verification failed: %w", err)
		}
		fmt.Println("[PASS] .devcontainer/devcontainer.json is 100% in sync with declared standards.")
		return nil
	}

	if err := devcontainer.WriteDevContainer(ctx, *outputPath, dc); err != nil {
		return fmt.Errorf("failed to write devcontainer: %w", err)
	}

	fmt.Printf("[SYNTHESIZED] %s for %s (%d features, %d extensions)\n",
		*outputPath, dc.Name, len(dc.Features), len(dc.Customizations.VSCode.Extensions))
	return nil
}
