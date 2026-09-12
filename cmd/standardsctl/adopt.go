package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/cordanaLLM/praetor/internal/adopt"
	"github.com/cordanaLLM/praetor/internal/harvester"
)

// adoptTimeout bounds one adoption run, including its git and scanner subprocesses.
const adoptTimeout = 2 * time.Minute

// maxAdoptArgs bounds the argument re-ordering loop (HISS-02).
const maxAdoptArgs = 256

// errAdoptIncomplete is returned when adoption ran but left the repository short of the
// advertised state; the report printed above it lists the individual failures.
var errAdoptIncomplete = errors.New("adoption completed with errors")

// adoptBoolFlags lists the flags that never consume a following positional value.
func adoptBoolFlags() map[string]bool {
	return map[string]bool{
		"dry-run": true, "force": true, "record-baseline": true, "all-missing": true,
	}
}

// reorderAdoptArgs moves positional arguments after flags so that
// `adopt <path> --flag` and `adopt --flag <path>` parse identically.
func reorderAdoptArgs(args []string) []string {
	boolFlags := adoptBoolFlags()
	var flagArgs []string
	var posArgs []string
	for i := 0; i < len(args) && i < maxAdoptArgs; i++ {
		arg := args[i]
		if !strings.HasPrefix(arg, "-") {
			posArgs = append(posArgs, arg)
			continue
		}
		flagArgs = append(flagArgs, arg)
		if flagTakesNextValue(args, i, boolFlags) {
			i++
			flagArgs = append(flagArgs, args[i])
		}
	}
	return append(flagArgs, posArgs...)
}

// flagTakesNextValue reports whether args[i] is a value-taking flag whose value is the
// next argument.
func flagTakesNextValue(args []string, i int, boolFlags map[string]bool) bool {
	arg := args[i]
	if strings.Contains(arg, "=") || i+1 >= len(args) || strings.HasPrefix(args[i+1], "-") {
		return false
	}
	name := strings.TrimLeft(arg, "-")
	return !boolFlags[name]
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

	ctx, cancel := context.WithTimeout(context.Background(), adoptTimeout)
	defer cancel()

	if *allMissing {
		homeDir, err := os.UserHomeDir()
		if err != nil {
			return fmt.Errorf("resolve home directory for --all-missing: %w", err)
		}
		return batchAdoptMissing(ctx, filepath.Join(homeDir, "dev"), *dryRun, *force, *recordBaseline)
	}

	opts := adopt.AdoptOptions{
		Path:           *path,
		Profile:        *profile,
		Facets:         splitFacets(*facets),
		DryRun:         *dryRun,
		Force:          *force,
		RecordBaseline: *recordBaseline,
	}

	report, err := adopt.Adopt(ctx, opts)
	if report != nil {
		printAdoptReport(report)
	}
	if err != nil {
		return fmt.Errorf("adopt repository failed: %w", err)
	}
	if len(report.Errors) > 0 {
		return fmt.Errorf("%w: %d error(s) listed above", errAdoptIncomplete, len(report.Errors))
	}
	return nil
}

// splitFacets parses the comma-separated --facets value.
func splitFacets(raw string) []string {
	var facetList []string
	parts := strings.Split(raw, ",")
	for i := 0; i < len(parts) && i < maxAdoptArgs; i++ {
		if trimmed := strings.TrimSpace(parts[i]); trimmed != "" {
			facetList = append(facetList, trimmed)
		}
	}
	return facetList
}

func batchAdoptMissing(ctx context.Context, devDir string, dryRun, force, recordBaseline bool) error {
	scan, err := harvester.ScanLocalWorkstation(ctx, devDir)
	if err != nil {
		return fmt.Errorf("scanning workstation: %w", err)
	}

	fmt.Printf("=== Batch Repository Adoption (%d unmanaged repos found) ===\n", len(scan.MissingRulesRepos))
	failed := 0
	for i := 0; i < len(scan.MissingRulesRepos) && i < harvester.MaxDevScanEntries; i++ {
		repoName := scan.MissingRulesRepos[i]
		opts := adopt.AdoptOptions{
			Path:           filepath.Join(devDir, repoName),
			DryRun:         dryRun,
			Force:          force,
			RecordBaseline: recordBaseline,
		}
		rep, err := adopt.Adopt(ctx, opts)
		if !printBatchResult(repoName, dryRun, rep, err) {
			failed++
		}
	}
	if failed > 0 {
		return fmt.Errorf("%w: %d of %d repositories failed", errAdoptIncomplete, failed, len(scan.MissingRulesRepos))
	}
	return nil
}

// printBatchResult prints one batch entry and reports whether it succeeded.
func printBatchResult(repoName string, dryRun bool, rep *adopt.AdoptReport, err error) bool {
	if err != nil {
		fmt.Printf("[FAIL] %s: %v\n", repoName, err)
		if rep != nil {
			fmt.Printf("  Written before failure: %d created, %d reconciled\n", len(rep.CreatedFiles), len(rep.ReconciledFiles))
		}
		return false
	}
	fmt.Printf("\n[ADOPTED] %s (State: %s, Archetype: %s, DryRun: %v)\n", repoName, rep.State, rep.Archetype, dryRun)
	fmt.Printf("  Created:    %d files\n", len(rep.CreatedFiles))
	fmt.Printf("  Reconciled: %d files\n", len(rep.ReconciledFiles))
	if rep.LegacyDebtCount > 0 {
		fmt.Printf("  Legacy Debt Baselined: %d infractions\n", rep.LegacyDebtCount)
	}
	printAdoptIssues(rep)
	return len(rep.Errors) == 0
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

	printDebtSummary(rep)
	printAdoptedFiles(rep)
	printAdoptIssues(rep)

	fmt.Println("\n--- Governance Pillars Synchronized ---")
	fmt.Println("  ✓ Universal Harness : Canonical AGENTS.md + Mermaid Verification Flowchart")
	fmt.Println("  ✓ AI Context Sync   : 6 targets (Claude Code, Cursor, Copilot, Windsurf, Codex, Gemini)")
	fmt.Println("  ✓ IDE Ecosystem     : VS Code, JetBrains (CLion/GoLand/PyCharm), Neovim")
	fmt.Println("  ✓ DevContainer      : Containerized deterministic dev environment (.devcontainer)")
	fmt.Println("  ✓ Verification Gate : Makefile 'verify-all' standard entrypoint")

	switch {
	case len(rep.Errors) > 0:
		fmt.Printf("\nAdoption finished with %d error(s); the repository is not fully governed yet.\n", len(rep.Errors))
	case rep.DryRun:
		fmt.Println("\nSimulated adoption plan completed. Run without -dry-run to apply.")
	default:
		fmt.Println("\nRepository successfully adopted into cordanaLLM/praetor governance!")
	}
}

// printDebtSummary prints the baselined legacy debt breakdown.
func printDebtSummary(rep *adopt.AdoptReport) {
	if rep.LegacyDebtCount == 0 {
		fmt.Println("\n--- Legacy Technical Debt: 0 infractions detected ---")
		return
	}
	fmt.Printf("\n--- Legacy Technical Debt Baselined (%d infractions) ---\n", rep.LegacyDebtCount)
	var ruleKeys []string
	for r := range rep.DebtBreakdown {
		ruleKeys = append(ruleKeys, r)
	}
	sort.Strings(ruleKeys)
	for _, r := range ruleKeys {
		fmt.Printf("  • %-8s: %d infractions\n", r, rep.DebtBreakdown[r])
	}
	fmt.Println("  (Infractions recorded into .standards-baseline.json to prevent CI breaks while ratcheting)")
}

// printAdoptIssues prints the warnings and errors an adoption run recorded.
func printAdoptIssues(rep *adopt.AdoptReport) {
	if len(rep.Warnings) > 0 {
		fmt.Printf("\nWarnings (%d):\n", len(rep.Warnings))
		for _, w := range rep.Warnings {
			fmt.Printf("  ! %s\n", w)
		}
	}
	if len(rep.Errors) > 0 {
		fmt.Printf("\nErrors (%d):\n", len(rep.Errors))
		for _, e := range rep.Errors {
			fmt.Printf("  x %s\n", e)
		}
	}
}

func printAdoptedFiles(rep *adopt.AdoptReport) {
	if len(rep.CreatedFiles) > 0 {
		fmt.Printf("\nFiles Created (%d):\n", len(rep.CreatedFiles))
		for _, f := range rep.CreatedFiles {
			printAdoptedFile("+ [NEW] ", f, findDetail(rep.ActionDetails, f))
		}
	}

	if len(rep.ReconciledFiles) > 0 {
		fmt.Printf("\nFiles Reconciled (%d):\n", len(rep.ReconciledFiles))
		for _, f := range rep.ReconciledFiles {
			printAdoptedFile("~ [SYNC]", f, findDetail(rep.ActionDetails, f))
		}
	}
}

func printAdoptedFile(prefix, file, detail string) {
	if detail != "" {
		fmt.Printf("  %s %-36s : %s\n", prefix, file, detail)
		return
	}
	fmt.Printf("  %s %s\n", prefix, file)
}
