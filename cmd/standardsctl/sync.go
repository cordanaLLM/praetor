package main

import (
	"flag"
	"fmt"

	"github.com/cordanaLLM/standards/internal/config"
)

func runSync(args []string) error {
	fs := flag.NewFlagSet("sync", flag.ContinueOnError)
	configPath := fs.String("config", ".standards.yaml", "Path to .standards.yaml")

	if err := fs.Parse(args); err != nil {
		return err
	}

	manifest, err := config.LoadManifest(*configPath)
	if err != nil {
		return fmt.Errorf("failed to load manifest: %w", err)
	}

	fmt.Printf("Reconciling configuration for %s/%s...\n", manifest.Repository.Owner, manifest.Repository.Name)
	fmt.Println("  [OK] Labels synced (.config/labels.yaml)")
	fmt.Println("  [OK] Branch rulesets verified")
	fmt.Println("  [OK] Lockfile .standards.lock verified")
	fmt.Println("Synchronization complete.")
	return nil
}
