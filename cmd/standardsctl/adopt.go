package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/cordanaLLM/standards/internal/adopt"
	"github.com/cordanaLLM/standards/internal/harvester"
)

func runAdopt(args []string) error {
	fs := flag.NewFlagSet("adopt", flag.ContinueOnError)
	profile := fs.String("profile", "", "Primary repository profile (auto-detected if empty)")
	facets := fs.String("facets", "", "Comma-separated list of facets")
	dryRun := fs.Bool("dry-run", false, "Simulate adoption without writing files")
	force := fs.Bool("force", false, "Overwrite existing standards configurations")
	recordBaseline := fs.Bool("record-baseline", true, "Record legacy debt into .standards-baseline.json")
	allMissing := fs.Bool("all-missing", false, "Adopt all detected unmanaged repositories in ~/dev")
	path := fs.String("path", ".", "Target repository path to adopt")

	if err := fs.Parse(args); err != nil {
		return err
	}

	// Positional path override if provided
	if fs.NArg() > 0 && *path == "." {
		*path = fs.Arg(0)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	homeDir, _ := os.UserHomeDir()

	if *allMissing {
		return batchAdoptMissing(ctx, filepath.Join(homeDir, "dev"), *dryRun, *force, *recordBaseline)
	}

	var facetList []string
	if *facets != "" {
		for _, f := range strings.Split(*facets, ",") {
			trimmed := strings.TrimSpace(f)
			if trimmed != "" {
				facetList = append(facetList, trimmed)
			}
		}
	}

	opts := adopt.AdoptOptions{
		Path:           *path,
		Profile:        *profile,
		Facets:         facetList,
		DryRun:         *dryRun,
		Force:          *force,
		RecordBaseline: *recordBaseline,
	}

	report, err := adopt.Adopt(ctx, opts)
	if err != nil {
		return fmt.Errorf("adopt repository failed: %w", err)
	}

	printAdoptReport(report)
	return nil
}

func batchAdoptMissing(ctx context.Context, devDir string, dryRun, force, recordBaseline bool) error {
	scan, err := harvester.ScanLocalWorkstation(ctx, devDir)
	if err != nil {
		return fmt.Errorf("scanning workstation: %w", err)
	}

	fmt.Printf("=== Batch Repository Adoption (%d unmanaged repos found) ===\n", len(scan.MissingRulesRepos))
	for _, repoName := range scan.MissingRulesRepos {
		targetPath := filepath.Join(devDir, repoName)
		opts := adopt.AdoptOptions{
			Path:           targetPath,
			DryRun:         dryRun,
			Force:          force,
			RecordBaseline: recordBaseline,
		}

		rep, err := adopt.Adopt(ctx, opts)
		if err != nil {
			fmt.Printf("[FAIL] %s: %v\n", repoName, err)
			continue
		}
		fmt.Printf("\n[ADOPTED] %s (State: %s, Archetype: %s, DryRun: %v)\n", repoName, rep.State, rep.Archetype, dryRun)
		fmt.Printf("  Created:    %d files\n", len(rep.CreatedFiles))
		fmt.Printf("  Reconciled: %d files\n", len(rep.ReconciledFiles))
		if rep.LegacyDebtCount > 0 {
			fmt.Printf("  Legacy Debt Baselined: %d infractions\n", rep.LegacyDebtCount)
		}
	}
	return nil
}

func printAdoptReport(rep *adopt.AdoptReport) {
	fmt.Println("=== Praetor Universal Repository Adoption ===")
	if rep.DryRun {
		fmt.Println("[DRY-RUN MODE: No files modified]")
	}
	fmt.Printf("Target State:      %s\n", rep.State)
	fmt.Printf("Archetype:         %s\n", rep.Archetype)
	fmt.Printf("Facets:            %v\n", rep.Facets)
	fmt.Printf("Files Created:     %d\n", len(rep.CreatedFiles))
	for _, f := range rep.CreatedFiles {
		fmt.Printf("  + [NEW] %s\n", f)
	}
	fmt.Printf("Files Reconciled:  %d\n", len(rep.ReconciledFiles))
	for _, f := range rep.ReconciledFiles {
		fmt.Printf("  ~ [SYNC] %s\n", f)
	}
	if rep.LegacyDebtCount > 0 {
		fmt.Printf("Legacy Debt Recorded: %d infractions in .standards-baseline.json\n", rep.LegacyDebtCount)
	}
	fmt.Println("\nRepository successfully adopted into cordanaLLM/praetor governance!")
}
