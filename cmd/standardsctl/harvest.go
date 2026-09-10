package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/cordanaLLM/standards/internal/harvester"
)

func runHarvest(args []string) error {
	if len(args) < 1 {
		fmt.Println("Usage: standardsctl harvest <subcommand> [arguments]")
		fmt.Println("\nSubcommands:")
		fmt.Println("  workstation [--dir=path]     Audit local dev directory and detect worktree sprawl")
		fmt.Println("  skills [--gemini=path]       Audit and detect duplicate agent skills")
		fmt.Println("  fleet                        Display multi-org remote fleet discovery report")
		return nil
	}

	sub := args[0]
	subArgs := args[1:]
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	homeDir, _ := os.UserHomeDir()

	switch sub {
	case "workstation":
		fs := flag.NewFlagSet("harvest workstation", flag.ContinueOnError)
		devDir := fs.String("dir", filepath.Join(homeDir, "dev"), "Path to development directory")
		if err := fs.Parse(subArgs); err != nil {
			return err
		}

		rep, err := harvester.ScanLocalWorkstation(ctx, *devDir)
		if err != nil {
			return fmt.Errorf("failed scanning workstation: %w", err)
		}

		fmt.Println("=== Workstation Governance & Worktree Audit ===")
		fmt.Printf("Active Dev Repositories: %d\n", rep.DevReposCount)
		fmt.Printf("Agent Documents Found:   %d\n", len(rep.DiscoveredAgentDoc))
		fmt.Printf("Repositories Missing Rules (%d):\n", len(rep.MissingRulesRepos))
		for _, r := range rep.MissingRulesRepos {
			fmt.Printf("  - %s\n", r)
		}
		fmt.Printf("Stale Ephemeral Worktrees (%d):\n", len(rep.StaleWorktrees))
		for _, wt := range rep.StaleWorktrees {
			fmt.Printf("  - %s\n", wt)
		}
		return nil

	case "skills":
		fs := flag.NewFlagSet("harvest skills", flag.ContinueOnError)
		geminiDir := fs.String("gemini", filepath.Join(homeDir, ".gemini"), "Path to .gemini directory")
		if err := fs.Parse(subArgs); err != nil {
			return err
		}

		rep, err := harvester.AuditSkills(ctx, *geminiDir, ".agents/skills")
		if err != nil {
			return fmt.Errorf("failed auditing skills: %w", err)
		}

		fmt.Println("=== Agent Skills & Hygiene Audit ===")
		fmt.Printf("Total Skill Manifests: %d\n", rep.TotalSkills)
		fmt.Printf("Unique Skills:         %d\n", rep.UniqueSkills)
		fmt.Printf("Duplicate Skills (%d):\n", len(rep.Duplicates))
		for name, paths := range rep.Duplicates {
			fmt.Printf("  - %s (found in %d locations):\n", name, len(paths))
			for _, p := range paths {
				fmt.Printf("      %s\n", p)
			}
		}
		if len(rep.StaleBackups) > 0 {
			fmt.Printf("Stale GEMINI.md Backups (%d):\n", len(rep.StaleBackups))
			for _, b := range rep.StaleBackups {
				fmt.Printf("  - %s\n", b)
			}
		}
		return nil

	case "fleet":
		fmt.Println("=== cordanaLLM Multi-Org Fleet Topology ===")
		orgs := []string{"cordanaLLM", "golusoris", "VMAFx", "jellysin", "goph-arr", "lusoris"}
		fmt.Printf("Governance Orgs Monitored: %v\n", orgs)
		fmt.Println("Archetype Match Distribution:")
		fmt.Println("  - native-gpu-systems: vmafx, pelorus, template-native-gpu, model_server")
		fmt.Println("  - gitops-infra:       k8s, home-zeus, helm-charts, dockge-stacks")
		fmt.Println("  - framework:          golusoris, sveltesentio, watershed")
		fmt.Println("  - library-client:     goenvoy, claude-api-schemas, llama-swap")
		fmt.Println("  - app-service:        dispatcher, gateway, workstation-connector, qdo, xe-telemetry")
		fmt.Println("  - pages-site:         northlight, thedesknook, lusoris.github.io")
		return nil

	default:
		return fmt.Errorf("unknown harvest subcommand: %s", sub)
	}
}
