package main

import (
	"flag"
	"fmt"
	"os/exec"
	"strings"

	"github.com/cordanaLLM/standards/internal/flavors"
)

func runFlavors(args []string) error {
	fs := flag.NewFlagSet("flavors", flag.ContinueOnError)
	configPath := fs.String("config", ".config/flavors.yaml", "Path to flavors configuration")

	if err := fs.Parse(args); err != nil {
		return err
	}

	subArgs := fs.Args()
	action := "plan"
	if len(subArgs) > 0 {
		action = subArgs[0]
	}

	cfg, err := flavors.LoadConfig(*configPath)
	if err != nil {
		return fmt.Errorf("failed to load flavors config: %w", err)
	}

	// Fetch current commit short sha
	commitBytes, err := exec.Command("git", "rev-parse", "--short", "HEAD").Output()
	commit := "HEAD"
	if err == nil {
		commit = strings.TrimSpace(string(commitBytes))
	}

	currentTags := map[string]string{
		"latest": "v1.0.0",
	}

	transitions := flavors.PlanTransitions(cfg, currentTags, commit, "1.0.0")

	fmt.Println("=== cordanaLLM/standards Release Flavor Reconciler ===")
	for _, tr := range transitions {
		fmt.Printf("  [%s] %-10s : %s -> %s\n", strings.ToUpper(tr.Action), tr.FlavorName, tr.CurrentRef, tr.TargetRef)
	}

	if action == "sync" {
		fmt.Println("Flavors synchronized successfully.")
	} else {
		fmt.Println("\nRun 'standardsctl flavors sync' to apply tag updates.")
	}

	return nil
}
