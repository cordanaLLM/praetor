package main

import (
	"context"
	"flag"
	"fmt"
	"path/filepath"
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

	switch args[0] {
	case "help", "-h", "--help":
		printHarvestUsage()
		return nil
	case "bundle":
		return runHarvestBundle(ctx, args[1:])
	case "ingest":
		return runHarvestIngest(ctx, args[1:])
	case "workstation":
		return runHarvestWorkstation(ctx, args[1:])
	case "skills":
		return runHarvestSkills(ctx, args[1:])
	case "fleet":
		return runHarvestFleet(args[1:])
	case "memory":
		return runHarvestMemory(ctx, args[1:])
	case "onboard":
		return runHarvestOnboard(ctx, args[1:])
	default:
		return fmt.Errorf("unknown harvest subcommand: %s", args[0])
	}
}

func printHarvestUsage() {
	fmt.Println("Usage: standardsctl harvest <subcommand> [arguments]")
	fmt.Println("\nSubcommands:")
	fmt.Println("  bundle [--name=name] [--out=dir] [--home=path] Capture workstation state bundle (memories, skills, logs, patches)")
	fmt.Println("  ingest [--bundle=dir] [--skills-dir=path] [--dry-run] Analyze or ingest workstation bundle into local agent harness")
	fmt.Println("  workstation [--dir=path]                    Audit local dev directory and worktree sprawl")
	fmt.Println("  skills [--gemini=path] [--dedupe] [--dry-run] Audit and deduplicate agent skills")
	fmt.Println("  fleet                                       Display multi-org remote fleet topology")
	fmt.Println("  memory [--brain=path]                       Extract agent memory insights from transcripts")
	fmt.Println("  onboard [--repo=path] [--dry-run]           Scaffold governance and harnesses into repos")
}

func runHarvestWorkstation(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("harvest workstation", flag.ContinueOnError)
	dirFlag := fs.String("dir", "", "Path to development directory (default: $HOME/dev)")
	if err := fs.Parse(args); err != nil {
		return err
	}

	devDir, err := resolveHomeSubdir(*dirFlag, "--dir", "dev")
	if err != nil {
		return fmt.Errorf("harvest workstation: %w", err)
	}

	rep, err := harvester.ScanLocalWorkstation(ctx, devDir)
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

func runHarvestSkills(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("harvest skills", flag.ContinueOnError)
	geminiFlag := fs.String("gemini", "", "Path to .gemini directory (default: $HOME/.gemini)")
	dedupe := fs.Bool("dedupe", false, "Remove redundant shadowed duplicate skills")
	cleanBackups := fs.Bool("clean-backups", false, "Purge stale GEMINI.md backups")
	dryRun := fs.Bool("dry-run", false, "Simulate changes without deleting")
	if err := fs.Parse(args); err != nil {
		return err
	}

	geminiDir, err := resolveHomeSubdir(*geminiFlag, "--gemini", ".gemini")
	if err != nil {
		return fmt.Errorf("harvest skills: %w", err)
	}

	rep, err := harvester.AuditSkills(ctx, geminiDir, ".agents/skills")
	if err != nil {
		return fmt.Errorf("failed auditing skills: %w", err)
	}

	printSkillsAudit(rep)
	return applySkillHygiene(ctx, geminiDir, rep, *dedupe, *cleanBackups, *dryRun)
}

// printSkillsAudit renders the read-only part of the skills audit.
func printSkillsAudit(rep *harvester.SkillAuditReport) {
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
}

// applySkillHygiene performs the optional mutating half of `harvest skills`.
func applySkillHygiene(ctx context.Context, geminiDir string, rep *harvester.SkillAuditReport, dedupe, cleanBackups, dryRun bool) error {
	if dedupe {
		dRep, dErr := harvester.DeduplicateSkills(ctx, rep, dryRun)
		if dErr != nil {
			return fmt.Errorf("deduplicate skills: %w", dErr)
		}
		fmt.Printf("\n[DEDUPE] %d shadowed skill directories processed (DryRun: %v)\n", len(dRep.PrunedSkills), dRep.DryRun)
		for _, s := range dRep.PrunedSkills {
			fmt.Printf("  - %s\n", s)
		}
	}

	if cleanBackups && len(rep.StaleBackups) > 0 {
		purged, pErr := harvester.PurgeBackups(ctx, geminiDir, rep.StaleBackups, dryRun)
		if pErr != nil {
			return fmt.Errorf("purge backups: %w", pErr)
		}
		fmt.Printf("\n[CLEAN] %d stale backup files processed (DryRun: %v)\n", len(purged), dryRun)
	}
	return nil
}

// runHarvestFleet prints the static fleet topology. It takes no arguments; trailing
// tokens are rejected so a flag the command does not implement cannot look accepted.
func runHarvestFleet(args []string) error {
	if len(args) > 0 {
		return fmt.Errorf("harvest fleet takes no arguments, got %q", args[0])
	}
	return printHarvestFleet()
}

func printHarvestFleet() error {
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

func runHarvestMemory(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("harvest memory", flag.ContinueOnError)
	brainFlag := fs.String("brain", "", "Path to brain directory (default: $HOME/.gemini/antigravity/brain)")
	if err := fs.Parse(args); err != nil {
		return err
	}

	brainDir, err := resolveHomeSubdir(*brainFlag, "--brain", ".gemini", "antigravity", "brain")
	if err != nil {
		return fmt.Errorf("harvest memory: %w", err)
	}

	insights, err := harvester.ExtractMemoryInsights(ctx, brainDir)
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

func runHarvestOnboard(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("harvest onboard", flag.ContinueOnError)
	repoPath := fs.String("repo", "", "Target repository path to onboard")
	allMissing := fs.Bool("all-missing", false, "Onboard all unmanaged repositories under --dir")
	dryRun := fs.Bool("dry-run", true, "Preview onboarding actions without modifying files")
	dirFlag := fs.String("dir", "", "Path to development directory (default: $HOME/dev)")
	if err := fs.Parse(args); err != nil {
		return err
	}

	targets, err := onboardTargets(ctx, *repoPath, *dirFlag, *allMissing)
	if err != nil {
		return err
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

// onboardTargets resolves the repositories `harvest onboard` must act on: either the
// single --repo path, or every unmanaged repository under the development directory.
func onboardTargets(ctx context.Context, repoPath, dirFlag string, allMissing bool) ([]string, error) {
	if repoPath != "" {
		return []string{repoPath}, nil
	}
	if !allMissing {
		return nil, fmt.Errorf("either --repo=<path> or --all-missing must be specified")
	}

	devDir, err := resolveHomeSubdir(dirFlag, "--dir", "dev")
	if err != nil {
		return nil, fmt.Errorf("harvest onboard: %w", err)
	}
	rep, err := harvester.ScanLocalWorkstation(ctx, devDir)
	if err != nil {
		return nil, fmt.Errorf("scanning workstation for missing repos: %w", err)
	}

	targets := make([]string, 0, len(rep.MissingRulesRepos))
	for _, name := range rep.MissingRulesRepos {
		targets = append(targets, filepath.Join(devDir, name))
	}
	return targets, nil
}

func runHarvestBundle(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("harvest bundle", flag.ContinueOnError)
	name := fs.String("name", "", "Workstation name identifier (e.g. --name=office-1)")
	outDir := fs.String("out", "", "Output destination directory for bundle (required)")
	devFlag := fs.String("dev", "", "Path to local development directory (default: $HOME/dev)")
	homeFlag := fs.String("home", "", "Workstation home directory to harvest (default: $HOME)")
	vaultDir := fs.String("vault", "", "Optional path to workstation vault containing patches/inventory")

	if err := fs.Parse(args); err != nil {
		return err
	}

	if *name == "" || *outDir == "" {
		return fmt.Errorf("both --name=<id> and --out=<path> are required flags")
	}

	homeDir, err := resolveHomeSubdir(*homeFlag, "--home")
	if err != nil {
		return fmt.Errorf("harvest bundle: %w", err)
	}
	devDir, err := resolveHomeSubdir(*devFlag, "--dev", "dev")
	if err != nil {
		return fmt.Errorf("harvest bundle: %w", err)
	}

	opts := harvester.BundleOptions{
		WorkstationName: *name,
		OutputDir:       *outDir,
		HomeDir:         homeDir,
		DevDir:          devDir,
		VaultDir:        *vaultDir,
	}

	fmt.Printf("=== Harvesting Workstation Bundle (%s) ===\n", *name)
	fmt.Printf("Output directory: %s\n", *outDir)

	rep, bundleErr := harvester.BundleWorkstation(ctx, opts)
	if bundleErr != nil {
		return fmt.Errorf("failed bundling workstation: %w", bundleErr)
	}

	fmt.Printf("Harvest Complete: %d files bundled (%d bytes)\n", rep.TotalFiles, rep.TotalBytes)
	fmt.Println("Categories:")
	for cat, count := range rep.Categories {
		fmt.Printf("  - %s: %d files\n", cat, count)
	}
	fmt.Printf("Cryptographic manifest: %s\n", rep.ManifestPath)
	return nil
}

func runHarvestIngest(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("harvest ingest", flag.ContinueOnError)
	bundleDir := fs.String("bundle", "", "Path to bundle directory containing manifest.json (required)")
	skillsFlag := fs.String("skills-dir", "", "Destination skills directory (default: $HOME/.gemini/config/skills)")
	dryRun := fs.Bool("dry-run", true, "Analyze without copying files")

	if err := fs.Parse(args); err != nil {
		return err
	}

	if *bundleDir == "" {
		return fmt.Errorf("--bundle=<path> is required")
	}

	localSkills, err := resolveHomeSubdir(*skillsFlag, "--skills-dir", ".gemini", "config", "skills")
	if err != nil {
		return fmt.Errorf("harvest ingest: %w", err)
	}

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
	return nil
}
