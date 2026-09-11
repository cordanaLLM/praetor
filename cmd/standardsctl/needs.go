package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/cordanaLLM/praetor/internal/adopt"
	"github.com/cordanaLLM/praetor/internal/config"
	"github.com/cordanaLLM/praetor/internal/forge"
	"github.com/cordanaLLM/praetor/internal/needs"
	"github.com/cordanaLLM/praetor/internal/util"
)

func runNeeds(args []string) error {
	if len(args) < 1 {
		printNeedsUsage()
		return nil
	}

	sub := args[0]
	subArgs := args[1:]
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	switch sub {
	case "-h", "--help", "help":
		printNeedsUsage()
		return nil
	case "scan":
		return runNeedsScan(ctx, subArgs)
	case "report":
		return runNeedsReport(ctx, subArgs)
	case "aggregate":
		return runNeedsAggregate(ctx, subArgs)
	case "migrate":
		return runNeedsMigrate(ctx, subArgs)
	case "requests":
		return runNeedsRequests(ctx, subArgs)
	case "epic":
		return runNeedsEpic(ctx, subArgs)
	default:
		return fmt.Errorf("unknown needs subcommand: %s", sub)
	}
}

func printNeedsUsage() {
	fmt.Println("Usage: standardsctl needs <subcommand> [arguments]")
	fmt.Println("\nSubcommands:")
	fmt.Println("  scan [--path=.] [--write]           Scan repo AST and go.mod, emit .needs.yaml")
	fmt.Println("  report [--path=.]                   Evaluate compatibility and replacement matrix against Golusoris")
	fmt.Println("  aggregate [--dev-dir=...] [--output=...] Aggregate fleet-wide demand and output gap report")
	fmt.Println("  requests [--dev-dir=...] [--output-dir=...] Synthesize and emit deduplicated Framework Demand Requests")
	fmt.Println("  epic [--path=.] [--framework=...] [--output=...] Generate pre-migration hardening epic")
	fmt.Println("  migrate [--path=.] [--dry-run|--apply]  Automated import and dependency rewrite to Golusoris")
}

func runNeedsScan(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("needs scan", flag.ContinueOnError)
	path := fs.String("path", ".", "Target repository path")
	writeManifest := fs.Bool("write", false, "Write discovered needs to .needs.yaml")
	if err := fs.Parse(args); err != nil {
		return err
	}

	if err := adopt.ValidateAdoptionTarget(*path); err != nil {
		return fmt.Errorf("invalid repository target: %w", err)
	}

	report, err := needs.ScanRepo(ctx, *path)
	if err != nil {
		return fmt.Errorf("failed to scan repository needs: %w", err)
	}

	fmt.Printf("=== Framework Needs Scan: %s ===\n", report.Repository)
	fmt.Printf("Go Version: %s | Target Framework: %s\n", report.GoVersion, report.Framework)
	fmt.Printf("Readiness Score: %.1f%% (%d covered, %d gaps, %d total third-party)\n\n",
		report.Readiness.Score, report.Readiness.CoveredDeps, report.Readiness.GapDeps, report.Readiness.TotalThirdPartyDeps)

	for _, dep := range report.Dependencies {
		statusIndicator := "[COVERED]"
		if dep.Status == needs.StatusGap {
			statusIndicator = "[GAP]    "
		}
		fmt.Printf("  %s %-36s -> %-18s (replacement: %s)\n",
			statusIndicator, dep.Package, dep.Capability, dep.GolusorisReplacement)
	}

	if *writeManifest {
		if err := needs.WriteNeedsManifest(*path, report); err != nil {
			return fmt.Errorf("failed to write .needs.yaml: %w", err)
		}
		fmt.Printf("\n[PASS] Wrote %s/.needs.yaml successfully.\n", *path)
	}
	return nil
}

func runNeedsReport(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("needs report", flag.ContinueOnError)
	path := fs.String("path", ".", "Target repository path")
	framework := fs.String("framework", "/home/kilian/dev/golusoris/golusoris", "Target framework repository path")
	if err := fs.Parse(args); err != nil {
		return err
	}

	if err := adopt.ValidateAdoptionTarget(*path); err != nil {
		return fmt.Errorf("invalid repository target: %w", err)
	}

	fwIndex, err := needs.InspectFramework(ctx, *framework)
	if err != nil {
		return fmt.Errorf("failed to inspect framework: %w", err)
	}

	rep, err := needs.ScanRepo(ctx, *path)
	if err != nil {
		return fmt.Errorf("failed to scan repository: %w", err)
	}

	fmt.Printf("=== Golusoris Migration Report: %s ===\n", rep.Repository)
	fmt.Printf("Framework: %s (%s) | Readiness Score: %.1f%%\n\n", fwIndex.Name, fwIndex.Version, rep.Readiness.Score)

	fmt.Println("Drop-In Replacement Matrix:")
	for _, dep := range rep.Dependencies {
		if dep.Status == needs.StatusCovered || dep.Status == needs.StatusAdapterAvailable {
			fmt.Printf("  ✓ %-35s -> %s\n", dep.Package, dep.GolusorisReplacement)
			if dep.Notes != "" {
				fmt.Printf("    Note: %s\n", dep.Notes)
			}
		} else {
			fmt.Printf("  ✗ %-35s -> NO DIRECT EQUIVALENT (Gap)\n", dep.Package)
		}
	}
	return nil
}

func runNeedsAggregate(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("needs aggregate", flag.ContinueOnError)
	devDir := fs.String("dev-dir", "/home/kilian/dev", "Fleet dev root directory")
	framework := fs.String("framework", "/home/kilian/dev/golusoris/golusoris", "Framework repository path")
	outputFile := fs.String("output", "", "Optional file path to write markdown report")
	if err := fs.Parse(args); err != nil {
		return err
	}

	report, err := needs.AggregateFleet(ctx, *devDir, *framework)
	if err != nil {
		return fmt.Errorf("fleet aggregation failed: %w", err)
	}

	md := needs.RenderFrameworkDemandMarkdown(report)
	if *outputFile != "" {
		if err := os.WriteFile(*outputFile, []byte(md), 0644); err != nil {
			return fmt.Errorf("failed to write output markdown: %w", err)
		}
		fmt.Printf("[PASS] Framework demand report written to %s\n", *outputFile)
	} else {
		fmt.Println(md)
	}
	return nil
}

func runNeedsMigrate(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("needs migrate", flag.ContinueOnError)
	path := fs.String("path", ".", "Target repository path")
	framework := fs.String("framework", "/home/kilian/dev/golusoris/golusoris", "Framework repository path")
	dryRun := fs.Bool("dry-run", true, "Preview migration without mutating files")
	apply := fs.Bool("apply", false, "Apply migration changes and create branch")
	if err := fs.Parse(args); err != nil {
		return err
	}

	if err := adopt.ValidateAdoptionTarget(*path); err != nil {
		return fmt.Errorf("invalid repository target: %w", err)
	}

	plan, err := needs.PlanMigration(ctx, *path, *framework)
	if err != nil {
		return fmt.Errorf("failed to plan migration: %w", err)
	}

	fmt.Printf("=== Migration Plan: %s -> %s ===\n", plan.Repository, plan.Framework)
	fmt.Printf("Added:   %s\n", strings.Join(plan.AddedRequires, ", "))
	fmt.Printf("Dropped: %s\n", strings.Join(plan.DroppedRequires, ", "))
	fmt.Printf("File import replacements: %d\n", len(plan.Replacements))

	for _, r := range plan.Replacements {
		fmt.Printf("  - %s: %s -> %s\n", util.CleanGitURL(r.File), r.OldImport, r.NewImport)
	}

	if *apply && !*dryRun {
		res, err := needs.ApplyMigration(ctx, *path, plan)
		if err != nil {
			return fmt.Errorf("failed to apply migration: %w", err)
		}
		fmt.Printf("\n[PASS] Migration applied on branch %s (%d files changed).\n", res.Branch, len(res.FilesChanged))
		fmt.Printf("[PASS] Migration guide generated at %s/MIGRATION.md\n", *path)
	} else {
		fmt.Println("\n[INFO] Dry-run complete. Pass --apply --dry-run=false to execute migration.")
	}
	return nil
}

func runNeedsRequests(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("needs requests", flag.ContinueOnError)
	devDir := fs.String("dev-dir", "/home/kilian/dev", "Fleet dev root directory")
	framework := fs.String("framework", "/home/kilian/dev/golusoris/golusoris", "Framework repository path")
	outputDir := fs.String("output-dir", "", "Optional output directory to write demand requests")
	if err := fs.Parse(args); err != nil {
		return err
	}

	report, err := needs.AggregateFleet(ctx, *devDir, *framework)
	if err != nil {
		return fmt.Errorf("fleet aggregation failed: %w", err)
	}

	requests := needs.SynthesizeDemands(report)
	fmt.Printf("=== Framework Demand Requests: %d Synthesized ===\n\n", len(requests))
	for _, req := range requests {
		fmt.Printf("  [%s] %s\n", req.RequestID, req.Title)
		fmt.Printf("    Target Kit: %s | Consuming Repos: %d | ROI: %s\n\n",
			req.TargetBuilderKit, req.ConsumerCount, req.MaintenanceROI)
	}

	if *outputDir != "" {
		if err := needs.EmitDemandRequests(requests, *outputDir); err != nil {
			return fmt.Errorf("failed to emit demand requests: %w", err)
		}
		fmt.Printf("[PASS] Emitted %d demand requests and FRAMEWORK_DEMAND.yaml to %s\n", len(requests), *outputDir)
	}
	return nil
}

func runNeedsEpic(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("needs epic", flag.ContinueOnError)
	path := fs.String("path", ".", "Target repository path")
	framework := fs.String("framework", "/home/kilian/dev/golusoris/golusoris", "Target framework repository path")
	output := fs.String("output", "", "Optional markdown file path to write pre-migration epic")
	publish := fs.Bool("publish", false, "Publish pre-migration parent epic and child tasks to remote forge")
	token := fs.String("token", "", "Forge API token (default: GITHUB_TOKEN or gh auth token)")
	endpoint := fs.String("endpoint", "", "Forge API endpoint (default: https://api.github.com)")
	if err := fs.Parse(args); err != nil {
		return err
	}

	if err := adopt.ValidateAdoptionTarget(*path); err != nil {
		return fmt.Errorf("invalid repository target: %w", err)
	}

	epic, err := needs.GeneratePreMigrationEpic(ctx, *path, *framework)
	if err != nil {
		return fmt.Errorf("failed to generate pre-migration epic: %w", err)
	}

	if *output != "" {
		if err := needs.WriteEpicMarkdown(epic, *output); err != nil {
			return fmt.Errorf("failed to write epic markdown: %w", err)
		}
		fmt.Printf("[PASS] Pre-migration epic written to %s\n", *output)
	} else if !*publish {
		fmt.Println(epic.ChecklistMarkdown)
	}

	if *publish {
		return publishEpicToForge(ctx, *path, *token, *endpoint, epic)
	}
	return nil
}

func publishEpicToForge(ctx context.Context, path, token, endpoint string, epic *needs.PreMigrationEpic) error {
	tok := resolveForgeAuthToken(ctx, token)
	if tok == "" {
		return fmt.Errorf("epic publishing requires GITHUB_TOKEN, GH_TOKEN, or an authenticated 'gh' CLI session")
	}

	owner, repo, err := resolveRepoCoordinates(path)
	if err != nil {
		return fmt.Errorf("failed resolving repository coordinates for %s: %w", path, err)
	}

	ghDriver := forge.NewGitHubDriver(tok, endpoint)
	ghDriver.SetRepository(owner, repo)

	fmt.Printf("[INFO] Publishing Pre-Migration Epic to %s/%s...\n", owner, repo)
	parentRes, childResults, err := needs.PublishPreMigrationEpic(ctx, ghDriver, epic)
	if err != nil {
		return fmt.Errorf("failed to publish epic to %s/%s: %w", owner, repo, err)
	}

	fmt.Printf("[PASS] Parent Epic created: %s#%d (%s)\n", owner+"/"+repo, parentRes.Number, parentRes.URL)
	for i, c := range childResults {
		fmt.Printf("       Task %d/%d: %s#%d (%s)\n", i+1, len(childResults), owner+"/"+repo, c.Number, c.URL)
	}
	return nil
}

func resolveForgeAuthToken(ctx context.Context, explicitToken string) string {
	if explicitToken != "" {
		return explicitToken
	}
	if tok := os.Getenv("GITHUB_TOKEN"); tok != "" {
		return tok
	}
	if tok := os.Getenv("GH_TOKEN"); tok != "" {
		return tok
	}
	if out, err := util.RunCommand(ctx, "", "gh", "auth", "token"); err == nil {
		return strings.TrimSpace(out)
	}
	return ""
}

func resolveRepoCoordinates(path string) (string, string, error) {
	manifestPath := filepath.Join(path, ".standards.yaml")
	if util.FileExists(manifestPath) {
		m, err := config.LoadManifest(manifestPath)
		if err == nil && m.Repository.Owner != "" && m.Repository.Name != "" {
			return m.Repository.Owner, m.Repository.Name, nil
		}
	}
	return util.ResolveRepoIdentity(context.Background(), path)
}
