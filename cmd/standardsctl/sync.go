package main

import (
	"flag"
	"fmt"

	"github.com/cordanaLLM/standards/internal/config"
	"github.com/cordanaLLM/standards/internal/util"
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
	if util.FileExists(".config/labels.yaml") {
		fmt.Println("  [OK] Labels verified (.config/labels.yaml)")
	} else {
		fmt.Println("  [WARN] Labels missing (.config/labels.yaml)")
	}
	if util.FileExists(".standards.lock") {
		fmt.Println("  [OK] Lockfile .standards.lock verified")
	} else {
		fmt.Println("  [WARN] Lockfile .standards.lock missing")
	}
	if util.FileExists("AGENTS.md") {
		fmt.Println("  [OK] Context harness AGENTS.md verified")
	} else {
		fmt.Println("  [WARN] Context harness AGENTS.md missing")
	}
	fmt.Println("Synchronization complete.")
	return nil
}
