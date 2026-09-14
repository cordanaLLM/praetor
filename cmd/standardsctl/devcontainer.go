package main

import (
	"context"
	"flag"
	"fmt"
	"time"

	"github.com/cordanallm/praetor/internal/config"
	"github.com/cordanallm/praetor/internal/devcontainer"
)

func runDevContainer(args []string) error {
	fs := flag.NewFlagSet("devcontainer", flag.ContinueOnError)
	configPath := fs.String("config", ".standards.yaml", "Path to .standards.yaml")
	outputPath := fs.String("output", ".devcontainer/devcontainer.json", "Target path for devcontainer.json")
	verify := fs.Bool("verify", false, "Verify that target devcontainer.json matches declared standards")

	if err := fs.Parse(args); err != nil {
		return err
	}

	subArgs := fs.Args()
	action := "generate"
	if len(subArgs) > 0 {
		action = subArgs[0]
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
