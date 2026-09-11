package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/cordanaLLM/praetor/internal/topology"
)

func runTopology(args []string) error {
	if len(args) < 1 {
		printTopologyUsage()
		return nil
	}

	sub := args[0]
	subArgs := args[1:]
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	switch sub {
	case "-h", "--help", "help":
		printTopologyUsage()
		return nil
	case "audit":
		return runTopologyAudit(ctx, subArgs)
	case "clean":
		return runTopologyClean(ctx, subArgs)
	default:
		return fmt.Errorf("unknown topology subcommand: %s", sub)
	}
}

func printTopologyUsage() {
	fmt.Println("Usage: standardsctl topology <subcommand> [arguments]")
	fmt.Println("\nSubcommands:")
	fmt.Println("  audit [--dev-root=...] Audits dev tree against workstation topology contract (DEV-01 to DEV-05)")
	fmt.Println("  clean [--dev-root=...] [--dry-run=true|false] Safely removes stray governance files from org roots")
}

func defaultDevRoot() string {
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		devPath := filepath.Join(home, "dev")
		if info, err := os.Stat(devPath); err == nil && info.IsDir() {
			return devPath
		}
	}
	return "/home/kilian/dev"
}

func runTopologyAudit(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("topology audit", flag.ContinueOnError)
	devRoot := fs.String("dev-root", defaultDevRoot(), "Target workstation dev root directory")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		*devRoot = fs.Arg(0)
	}

	report, err := topology.AuditWorkstationTopology(ctx, *devRoot)
	if err != nil {
		return fmt.Errorf("topology audit failed: %w", err)
	}

	fmt.Printf("=== Workstation Topology Audit: %s ===\n", report.DevRoot)
	fmt.Printf("Organization Containers: %d (%v)\n", len(report.OrgContainers), report.OrgContainers)
	fmt.Printf("Valid Leaf Repositories: %d\n", len(report.ValidRepos))
	fmt.Printf("Compatibility Symlinks:  %d (%v)\n\n", len(report.Symlinks), report.Symlinks)

	if len(report.Violations) > 0 {
		fmt.Printf("--- Invariant Violations (%d) ---\n", len(report.Violations))
		for _, v := range report.Violations {
			fmt.Printf("  ✗ %s\n", v)
		}
		fmt.Println()
	}

	if len(report.StrayFiles) > 0 {
		fmt.Printf("--- Stray / Misplaced Files Detected (%d) ---\n", len(report.StrayFiles))
		for _, s := range report.StrayFiles {
			safeStr := "MANUAL REVIEW REQUIRED"
			if s.IsSafeToDelete {
				safeStr = "SAFE TO CLEAN"
			}
			fmt.Printf("  • %-42s [%s] (%s)\n", s.RelPath, safeStr, s.Reason)
		}
		fmt.Printf("\nRun 'standardsctl topology clean --dev-root=%s' to purge safe stray files.\n", report.DevRoot)
		return fmt.Errorf("topology audit failed with %d stray governance files", len(report.StrayFiles))
	}

	fmt.Println("[PASS] Workstation topology is 100% compliant with DEV-01 through DEV-05.")
	return nil
}

func runTopologyClean(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("topology clean", flag.ContinueOnError)
	devRoot := fs.String("dev-root", defaultDevRoot(), "Target workstation dev root directory")
	dryRun := fs.Bool("dry-run", true, "Simulate cleaning without deleting files")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		*devRoot = fs.Arg(0)
	}

	fmt.Printf("=== Workstation Topology Clean: %s (DryRun: %v) ===\n", *devRoot, *dryRun)
	cleaned, err := topology.CleanWorkstationTopology(ctx, *devRoot, *dryRun)
	if err != nil {
		return fmt.Errorf("topology clean failed: %w", err)
	}

	if len(cleaned) == 0 {
		fmt.Println("[INFO] No stray governance files found to clean.")
		return nil
	}

	action := "Removed"
	if *dryRun {
		action = "Would remove"
	}

	fmt.Printf("\n%s %d stray files/directories:\n", action, len(cleaned))
	for _, p := range cleaned {
		fmt.Printf("  - %s\n", p)
	}

	if *dryRun {
		fmt.Println("\n[INFO] Dry-run complete. Pass --dry-run=false to execute cleanup.")
	} else {
		fmt.Printf("\n[PASS] Successfully cleaned %d stray files. Topology restored.\n", len(cleaned))
	}
	return nil
}
