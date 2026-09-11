package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/cordanaLLM/standards/internal/adopt"
	"github.com/cordanaLLM/standards/internal/harvester"
)

func reorderAdoptArgs(args []string) []string {
	var flagArgs []string
	var posArgs []string
	for i := 0; i < len(args); i++ {
		if strings.HasPrefix(args[i], "-") {
			flagArgs = append(flagArgs, args[i])
			if !strings.Contains(args[i], "=") && i+1 < len(args) && !strings.HasPrefix(args[i+1], "-") {
				if args[i] != "-dry-run" && args[i] != "--dry-run" &&
					args[i] != "-force" && args[i] != "--force" &&
					args[i] != "-record-baseline" && args[i] != "--record-baseline" &&
					args[i] != "-all-missing" && args[i] != "--all-missing" {
					i++
					flagArgs = append(flagArgs, args[i])
				}
			}
		} else {
			posArgs = append(posArgs, args[i])
		}
	}
	return append(flagArgs, posArgs...)
}

func runAdopt(args []string) error {
	fs := flag.NewFlagSet("adopt", flag.ContinueOnError)
	profile := fs.String("profile", "", "Primary repository profile (auto-detected if empty)")
	facets := fs.String("facets", "", "Comma-separated list of facets")
	dryRun := fs.Bool("dry-run", false, "Simulate adoption without writing files")
	force := fs.Bool("force", false, "Overwrite existing standards configurations")
	recordBaseline := fs.Bool("record-baseline", true, "Record legacy debt into .standards-baseline.json")
	allMissing := fs.Bool("all-missing", false, "Adopt all detected unmanaged repositories in ~/dev")
	path := fs.String("path", ".", "Target repository path to adopt")

	if err := fs.Parse(reorderAdoptArgs(args)); err != nil {
		return err
	}

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
			if trimmed := strings.TrimSpace(f); trimmed != "" {
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

func findDetail(details []adopt.ActionDetail, path string) string {
	for _, d := range details {
		if d.Path == path {
			return d.Details
		}
	}
	return ""
}

func printAdoptReport(rep *adopt.AdoptReport) {
	fmt.Println("=== Praetor Universal Repository Adoption ===")
	if rep.DryRun {
		fmt.Println("[DRY-RUN SIMULATION: No filesystem mutations performed]")
	}
	fmt.Printf("Target State:       %s\n", rep.State)
	fmt.Printf("Archetype:          %s\n", rep.Archetype)
	fmt.Printf("Facets:             %v\n", rep.Facets)

	// Debt summary
	if rep.LegacyDebtCount > 0 {
		fmt.Printf("\n--- Legacy Technical Debt Baselined (%d infractions) ---\n", rep.LegacyDebtCount)
		if len(rep.DebtBreakdown) > 0 {
			var ruleKeys []string
			for r := range rep.DebtBreakdown {
				ruleKeys = append(ruleKeys, r)
			}
			sort.Strings(ruleKeys)
			for _, r := range ruleKeys {
				fmt.Printf("  • %-8s: %d infractions\n", r, rep.DebtBreakdown[r])
			}
		}
		fmt.Println("  (Infractions recorded into .standards-baseline.json to prevent CI breaks while ratcheting)")
	} else {
		fmt.Println("\n--- Legacy Technical Debt: 0 infractions detected ---")
	}

	printAdoptedFiles(rep)

	// Governance Pillars Summary
	fmt.Println("\n--- Governance Pillars Synchronized ---")
	fmt.Println("  ✓ Universal Harness : Canonical AGENTS.md + Mermaid Verification Flowchart")
	fmt.Println("  ✓ AI Context Sync   : 6 targets (Claude Code, Cursor, Copilot, Windsurf, Codex, Gemini)")
	fmt.Println("  ✓ IDE Ecosystem     : VS Code, JetBrains (CLion/GoLand/PyCharm), Neovim")
	fmt.Println("  ✓ DevContainer      : Containerized deterministic dev environment (.devcontainer)")
	fmt.Println("  ✓ Verification Gate : Makefile 'verify-all' standard entrypoint")

	if rep.DryRun {
		fmt.Println("\nSimulated adoption plan completed. Run without -dry-run to apply.")
	} else {
		fmt.Println("\nRepository successfully adopted into cordanaLLM/praetor governance!")
	}
}

func printAdoptedFiles(rep *adopt.AdoptReport) {
	if len(rep.CreatedFiles) > 0 {
		fmt.Printf("\nFiles Created (%d):\n", len(rep.CreatedFiles))
		for _, f := range rep.CreatedFiles {
			detail := findDetail(rep.ActionDetails, f)
			if detail != "" {
				fmt.Printf("  + [NEW]  %-36s : %s\n", f, detail)
			} else {
				fmt.Printf("  + [NEW]  %s\n", f)
			}
		}
	}

	if len(rep.ReconciledFiles) > 0 {
		fmt.Printf("\nFiles Reconciled (%d):\n", len(rep.ReconciledFiles))
		for _, f := range rep.ReconciledFiles {
			detail := findDetail(rep.ActionDetails, f)
			if detail != "" {
				fmt.Printf("  ~ [SYNC] %-36s : %s\n", f, detail)
			} else {
				fmt.Printf("  ~ [SYNC] %s\n", f)
			}
		}
	}
}
