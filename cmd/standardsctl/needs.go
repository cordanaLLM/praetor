package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/cordanallm/praetor/internal/needs"
	"github.com/cordanallm/praetor/internal/util"
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
	default:
		return fmt.Errorf("unknown needs subcommand: %s", sub)
	}
}

// resolveFleetDir picks the fleet root: the flag, $PRAETOR_FLEET_DIR, else the
// grandparent of the current directory (dev/<org>/<repo> layout) or its parent.
func resolveFleetDir(explicit string) string {
	if explicit != "" {
		return explicit
	}
	if env := os.Getenv("PRAETOR_FLEET_DIR"); env != "" {
		return env
	}
	cwd, err := os.Getwd()
	if err != nil {
		return "."
	}
	if gp := filepath.Dir(filepath.Dir(cwd)); util.DirExists(gp) {
		return gp
	}
	return filepath.Dir(cwd)
}

func printNeedsUsage() {
	fmt.Println("Usage: standardsctl needs <subcommand> [arguments]")
	fmt.Println("\nSubcommands:")
	fmt.Println("  scan [--path=.] [--write]           Scan repo AST and go.mod, emit .needs.yaml")
	fmt.Println("  report [--path=.]                   Evaluate compatibility and replacement matrix against Golusoris")
	fmt.Println("  aggregate [--dev-dir=...] [--output=...] Aggregate fleet-wide demand and output gap report")
	fmt.Println("  migrate [--path=.] [--dry-run|--apply]  Automated import and dependency rewrite to Golusoris")
}

func runNeedsScan(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("needs scan", flag.ContinueOnError)
	path := fs.String("path", ".", "Target repository path")
	framework := fs.String("framework", "", "Framework checkout (default: $"+needs.FrameworkPathEnv+", then the module cache)")
	writeManifest := fs.Bool("write", false, "Write discovered needs to .needs.yaml")
	if err := fs.Parse(args); err != nil {
		return err
	}

	fwIndex, err := needs.InspectFramework(ctx, needs.ResolveFrameworkPath(ctx, *framework))
	if err != nil {
		return fmt.Errorf("failed to inspect framework: %w", err)
	}
	report, err := needs.ScanRepoWith(ctx, *path, fwIndex)
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
	framework := fs.String("framework", "", "Framework checkout (default: $"+needs.FrameworkPathEnv+", then the module cache)")
	if err := fs.Parse(args); err != nil {
		return err
	}

	fwIndex, err := needs.InspectFramework(ctx, needs.ResolveFrameworkPath(ctx, *framework))
	if err != nil {
		return fmt.Errorf("failed to inspect framework: %w", err)
	}

	rep, err := needs.ScanRepoWith(ctx, *path, fwIndex)
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
	devDir := fs.String("dev-dir", "", "Fleet dev root directory (default: $PRAETOR_FLEET_DIR, then the parent of the current repository)")
	framework := fs.String("framework", "", "Framework checkout (default: $"+needs.FrameworkPathEnv+", then the module cache)")
	outputFile := fs.String("output", "", "Optional file path to write markdown report")
	if err := fs.Parse(args); err != nil {
		return err
	}
	fleetRoot := resolveFleetDir(*devDir)

	report, err := needs.AggregateFleet(ctx, fleetRoot, needs.ResolveFrameworkPath(ctx, *framework))
	if err != nil {
		return fmt.Errorf("fleet aggregation failed: %w", err)
	}

	md := needs.RenderFrameworkDemandMarkdown(report)
	if *outputFile != "" {
		if err := os.WriteFile(*outputFile, []byte(md), 0o644); err != nil {
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
	framework := fs.String("framework", "", "Framework checkout (default: $"+needs.FrameworkPathEnv+", then the module cache)")
	dryRun := fs.Bool("dry-run", true, "Preview migration without mutating files")
	apply := fs.Bool("apply", false, "Apply migration changes and create branch")
	if err := fs.Parse(args); err != nil {
		return err
	}

	plan, err := needs.PlanMigration(ctx, *path, needs.ResolveFrameworkPath(ctx, *framework))
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
