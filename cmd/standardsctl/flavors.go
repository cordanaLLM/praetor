package main

import (
	"context"
	"flag"
	"fmt"
	"strings"

	"github.com/cordanaLLM/praetor/internal/flavors"
	"github.com/cordanaLLM/praetor/internal/util"
)

func fetchCurrentTags() map[string]string {
	currentTags := make(map[string]string)
	tagOut, err := util.RunGit(context.Background(), ".", "tag", "-l")
	if err == nil {
		for _, tag := range strings.Split(tagOut, "\n") {
			tag = strings.TrimSpace(tag)
			if tag != "" {
				currentTags[tag] = tag
			}
		}
	}
	return currentTags
}

func applyFlavorTransitions(transitions []flavors.TagTransition) error {
	ctx := context.Background()
	for _, tr := range transitions {
		if tr.Action == "create" || tr.Action == "update" {
			target := tr.TargetRef
			if _, err := util.RunGit(ctx, ".", "rev-parse", "--verify", target); err != nil {
				target = "HEAD"
			}
			out, err := util.RunGit(ctx, ".", "tag", "-f", tr.FlavorName, target)
			if err != nil {
				return fmt.Errorf("failed to apply flavor tag %s: %w (%s)", tr.FlavorName, err, out)
			}
			fmt.Printf("Updated tag %s -> %s\n", tr.FlavorName, target)
		}
	}
	fmt.Println("Flavors synchronized successfully.")
	return nil
}

func runFlavors(args []string) error {
	fs := flag.NewFlagSet("flavors", flag.ContinueOnError)
	configPath := fs.String("config", ".config/flavors.yaml", "Path to flavors configuration")

	if err := fs.Parse(args); err != nil {
		return err
	}

	action := "plan"
	if len(fs.Args()) > 0 {
		action = fs.Args()[0]
	}

	cfg, err := flavors.LoadConfig(*configPath)
	if err != nil {
		return fmt.Errorf("failed to load flavors config: %w", err)
	}

	commit, err := util.RunGit(context.Background(), ".", "rev-parse", "--short", "HEAD")
	if err != nil || commit == "" {
		commit = "HEAD"
	}

	transitions := flavors.PlanTransitions(cfg, fetchCurrentTags(), commit, "1.0.0")

	fmt.Println("=== cordanaLLM/praetor Release Flavor Reconciler ===")
	for _, tr := range transitions {
		fmt.Printf("  [%s] %-10s : %s -> %s\n", strings.ToUpper(tr.Action), tr.FlavorName, tr.CurrentRef, tr.TargetRef)
	}

	if action == "sync" {
		return applyFlavorTransitions(transitions)
	}
	fmt.Println("\nRun 'standardsctl flavors sync' to apply tag updates.")
	return nil
}
