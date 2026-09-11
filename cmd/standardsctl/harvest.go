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
		printHarvestUsage()
		return nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	homeDir, _ := os.UserHomeDir()

	switch args[0] {
	case "help", "-h", "--help":
		printHarvestUsage()
		return nil
	case "workstation":
		return runHarvestWorkstation(ctx, homeDir, args[1:])
	case "skills":
		return runHarvestSkills(ctx, homeDir, args[1:])
	case "fleet":
		return runHarvestFleet()
	case "memory":
		return runHarvestMemory(ctx, homeDir, args[1:])
	case "onboard":
		return runHarvestOnboard(ctx, homeDir, args[1:])
	default:
		return fmt.Errorf("unknown harvest subcommand: %s", args[0])
	}
}

func printHarvestUsage() {
	fmt.Println("Usage: standardsctl harvest <subcommand> [arguments]")
	fmt.Println("\nSubcommands:")
	fmt.Println("  workstation [--dir=path]                    Audit local dev directory and worktree sprawl")
	fmt.Println("  skills [--gemini=path] [--dedupe] [--dry-run] Audit and deduplicate agent skills")
	fmt.Println("  fleet                                       Display multi-org remote fleet topology")
	fmt.Println("  memory [--brain=path]                       Extract agent memory insights from transcripts")
	fmt.Println("  onboard [--repo=path] [--dry-run]           Scaffold governance and harnesses into repos")
}

func runHarvestWorkstation(ctx context.Context, homeDir string, args []string) error {
	fs := flag.NewFlagSet("harvest workstation", flag.ContinueOnError)
	devDir := fs.String("dir", filepath.Join(homeDir, "dev"), "Path to development directory")
	if err := fs.Parse(args); err != nil {
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
}

func runHarvestSkills(ctx context.Context, homeDir string, args []string) error {
	fs := flag.NewFlagSet("harvest skills", flag.ContinueOnError)
	geminiDir := fs.String("gemini", filepath.Join(homeDir, ".gemini"), "Path to .gemini directory")
	dedupe := fs.Bool("dedupe", false, "Remove redundant shadowed duplicate skills")
	cleanBackups := fs.Bool("clean-backups", false, "Purge stale GEMINI.md backups")
	dryRun := fs.Bool("dry-run", false, "Simulate changes without deleting")
	if err := fs.Parse(args); err != nil {
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
		fmt.Printf("  - %s (%d copies)\n", name, len(paths))
	}
	if len(rep.StaleBackups) > 0 {
		fmt.Printf("Stale GEMINI.md Backups (%d)\n", len(rep.StaleBackups))
	}

	if *dedupe {
		dRep, dErr := harvester.DeduplicateSkills(ctx, rep, *dryRun)
		if dErr != nil {
			return fmt.Errorf("deduplicate skills: %w", dErr)
		}
		fmt.Printf("\n[DEDUPE] %d shadowed skill directories processed (DryRun: %v)\n", len(dRep.PrunedSkills), dRep.DryRun)
		for _, s := range dRep.PrunedSkills {
			fmt.Printf("  - %s\n", s)
		}
	}

	if *cleanBackups && len(rep.StaleBackups) > 0 {
		purged, pErr := harvester.PurgeBackups(ctx, *geminiDir, rep.StaleBackups, *dryRun)
		if pErr != nil {
			return fmt.Errorf("purge backups: %w", pErr)
		}
		fmt.Printf("\n[CLEAN] %d stale backup files processed (DryRun: %v)\n", len(purged), *dryRun)
	}

	return nil
}

func runHarvestFleet() error {
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
}

func runHarvestMemory(ctx context.Context, homeDir string, args []string) error {
	fs := flag.NewFlagSet("harvest memory", flag.ContinueOnError)
	brainDir := fs.String("brain", filepath.Join(homeDir, ".gemini", "antigravity", "brain"), "Path to brain directory")
	if err := fs.Parse(args); err != nil {
		return err
	}

	insights, err := harvester.ExtractMemoryInsights(ctx, *brainDir)
	if err != nil {
		return fmt.Errorf("failed extracting memory insights: %w", err)
	}

	fmt.Printf("=== Agent Memory & Rule Mining (%d insights discovered) ===\n", len(insights))
	for i, ins := range insights {
		if i >= 20 {
			fmt.Printf("... and %d more insights.\n", len(insights)-20)
			break
		}
		fmt.Printf("  [%s] %s: %s\n", ins.Category, ins.Source, ins.Summary)
	}
	return nil
}

func runHarvestOnboard(ctx context.Context, homeDir string, args []string) error {
	fs := flag.NewFlagSet("harvest onboard", flag.ContinueOnError)
	repoPath := fs.String("repo", "", "Target repository path to onboard")
	allMissing := fs.Bool("all-missing", false, "Onboard all unmanaged repositories in ~/dev")
	dryRun := fs.Bool("dry-run", true, "Preview onboarding actions without modifying files")
	devDir := fs.String("dir", filepath.Join(homeDir, "dev"), "Path to development directory")
	if err := fs.Parse(args); err != nil {
		return err
	}

	targets := make([]string, 0)
	if *repoPath != "" {
		targets = append(targets, *repoPath)
	} else if *allMissing {
		rep, err := harvester.ScanLocalWorkstation(ctx, *devDir)
		if err != nil {
			return fmt.Errorf("scanning workstation for missing repos: %w", err)
		}
		for _, name := range rep.MissingRulesRepos {
			targets = append(targets, filepath.Join(*devDir, name))
		}
	} else {
		return fmt.Errorf("either --repo=<path> or --all-missing must be specified")
	}

	fmt.Printf("=== Praetor Repository Onboarding (DryRun: %v, Repos: %d) ===\n", *dryRun, len(targets))
	for _, target := range targets {
		plan, err := harvester.OnboardRepository(ctx, target, *dryRun)
		if err != nil {
			fmt.Printf("[FAIL] %s: %v\n", target, err)
			continue
		}
		fmt.Printf("\n[TARGET] %s (Archetype: %s)\n", plan.RepoPath, plan.Archetype)
		for _, action := range plan.Actions {
			fmt.Printf("  - %s\n", action)
		}
	}
	return nil
}
