package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/cordanaLLM/praetor/internal/harvester"
)

func runHarvest(args []string) error {
	if len(args) < 1 {
		printHarvestUsage()
		return nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	homeDir, homeErr := os.UserHomeDir()
	if homeErr != nil {
		return fmt.Errorf("resolve user home directory: %w", homeErr)
	}

	switch args[0] {
	case "help", "-h", "--help":
		printHarvestUsage()
		return nil
	case "bundle":
		return runHarvestBundle(ctx, homeDir, args[1:])
	case "ingest":
		return runHarvestIngest(ctx, homeDir, args[1:])
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
	fmt.Println("  bundle [--name=name] [--out=dir] [--include-shell-history]")
	fmt.Println("                                              Capture workstation state bundle (memories, skills, logs, patches)")
	fmt.Println("  ingest [--bundle=dir] [--dry-run]           Analyze or ingest workstation bundle into local agent harness")
	fmt.Println("  workstation [--dir=path]                    Audit local dev directory and worktree sprawl")
	fmt.Println("  skills [--gemini=path] [--repo=path] [--dedupe] [--dry-run]")
	fmt.Println("                                              Audit and deduplicate agent skills")
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
	repoDir := fs.String("repo", ".", "Repository root whose .agents/skills directory is included in the audit")
	dedupe := fs.Bool("dedupe", false, "Remove ~/.gemini/skills entries shadowed by ~/.gemini/config/skills")
	cleanBackups := fs.Bool("clean-backups", false, "Purge stale GEMINI.md backups")
	dryRun := fs.Bool("dry-run", true, "Simulate changes without deleting")
	if err := fs.Parse(args); err != nil {
		return err
	}

	repoSkills := ""
	if *repoDir != "" {
		repoSkills = filepath.Join(*repoDir, ".agents", "skills")
	}

	rep, err := harvester.AuditSkills(ctx, *geminiDir, repoSkills)
	if err != nil {
		return fmt.Errorf("failed auditing skills: %w", err)
	}

	fmt.Println("=== Agent Skills & Hygiene Audit ===")
	fmt.Printf("Total Skill Manifests: %d\n", rep.TotalSkills)
	fmt.Printf("Unique Skills:         %d\n", rep.UniqueSkills)
	fmt.Printf("Duplicate Skills (%d):\n", len(rep.Duplicates))
	for name, locations := range rep.Duplicates {
		fmt.Printf("  - %s (%d copies)\n", name, len(locations))
		for _, loc := range locations {
			fmt.Printf("      [%s] %s\n", loc.Origin, loc.Path)
		}
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

func runHarvestBundle(ctx context.Context, homeDir string, args []string) error {
	fs := flag.NewFlagSet("harvest bundle", flag.ContinueOnError)
	name := fs.String("name", "", "Workstation name identifier (e.g. --name=office-1)")
	outDir := fs.String("out", "", "Output destination directory for bundle (required)")
	vaultDir := fs.String("vault", "", "Optional path to workstation vault containing patches/inventory")
	includeHistory := fs.Bool("include-shell-history", false,
		"Also bundle ~/.bash_history, ~/.zsh_history and the PowerShell console history (they commonly contain exported credentials)")

	if err := fs.Parse(args); err != nil {
		return err
	}

	if *name == "" || *outDir == "" {
		return fmt.Errorf("both --name=<id> and --out=<path> are required flags")
	}

	opts := harvester.BundleOptions{
		WorkstationName:     *name,
		OutputDir:           *outDir,
		HomeDir:             homeDir,
		VaultDir:            *vaultDir,
		IncludeShellHistory: *includeHistory,
	}

	fmt.Printf("=== Harvesting Workstation Bundle (%s) ===\n", *name)
	fmt.Printf("Output directory: %s\n", *outDir)

	rep, err := harvester.BundleWorkstation(ctx, opts)
	if err != nil {
		return fmt.Errorf("failed bundling workstation: %w", err)
	}

	fmt.Printf("Harvest Complete: %d files bundled (%d bytes)\n", rep.TotalFiles, rep.TotalBytes)
	fmt.Println("Categories:")
	for cat, count := range rep.Categories {
		fmt.Printf("  - %s: %d files\n", cat, count)
	}
	printBundleWarnings(rep)
	fmt.Printf("Cryptographic manifest: %s\n", rep.ManifestPath)
	return nil
}

// printBundleWarnings names the captured categories that routinely contain credentials and
// lists anything the bundler refused to copy.
func printBundleWarnings(rep *harvester.WorkstationBundleReport) {
	sensitive := make([]string, 0, len(harvester.SensitiveBundleCategories))
	for _, cat := range harvester.SensitiveBundleCategories {
		if rep.Categories[cat] > 0 {
			sensitive = append(sensitive, fmt.Sprintf("%s (%d)", cat, rep.Categories[cat]))
		}
	}
	if len(sensitive) > 0 {
		fmt.Printf("[WARNING] This bundle contains credential-bearing categories: %s\n", strings.Join(sensitive, ", "))
		fmt.Println("[WARNING] The bundle is written owner-only (0700/0600). Review it before transferring it anywhere.")
	}
	if len(rep.Skipped) == 0 {
		return
	}
	fmt.Printf("Skipped sources (%d):\n", len(rep.Skipped))
	for i, s := range rep.Skipped {
		if i >= 20 {
			fmt.Printf("  ... and %d more.\n", len(rep.Skipped)-20)
			break
		}
		fmt.Printf("  - %s\n", s)
	}
}

func runHarvestIngest(ctx context.Context, homeDir string, args []string) error {
	fs := flag.NewFlagSet("harvest ingest", flag.ContinueOnError)
	bundleDir := fs.String("bundle", "", "Path to bundle directory containing manifest.json (required)")
	dryRun := fs.Bool("dry-run", true, "Analyze without copying files")

	if err := fs.Parse(args); err != nil {
		return err
	}

	if *bundleDir == "" {
		return fmt.Errorf("--bundle=<path> is required")
	}

	localSkills := filepath.Join(homeDir, ".gemini", "config", "skills")
	rep, err := harvester.IngestBundle(ctx, *bundleDir, localSkills, *dryRun)
	if err != nil {
		return fmt.Errorf("failed ingesting bundle: %w", err)
	}

	fmt.Printf("=== Ingesting Bundle from %s (DryRun: %v) ===\n", rep.WorkstationName, *dryRun)
	fmt.Printf("Novel Skills to Ingest (%d):\n", len(rep.NovelSkills))
	for _, s := range rep.NovelSkills {
		fmt.Printf("  - [NEW] %s\n", s)
	}
	fmt.Printf("Overlapping Existing Skills (%d):\n", len(rep.ExistingSkills))
	fmt.Printf("Project Memories Discovered (%d):\n", len(rep.NovelMemories))
	fmt.Printf("Design Patches Discovered (%d):\n", len(rep.NovelPatches))
	fmt.Printf("Manifest Integrity Verified: %v\n", rep.ValidIntegrity)
	for i, rejected := range rep.RejectedRecords {
		if i >= 20 {
			fmt.Printf("  ... and %d more rejected records.\n", len(rep.RejectedRecords)-20)
			break
		}
		fmt.Printf("  [REJECTED] %s\n", rejected)
	}
	if !rep.ValidIntegrity {
		return fmt.Errorf("bundle %s failed manifest integrity verification (%d rejected records)", *bundleDir, len(rep.RejectedRecords))
	}
	return nil
}
