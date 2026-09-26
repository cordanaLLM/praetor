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

// adoptTimeout bounds one adoption run, including git and scanner subprocesses.
const adoptTimeout = 2 * time.Minute

// errAdoptIncomplete prevents a partially successful report from returning success.
var errAdoptIncomplete = errors.New("adoption completed with errors")

// boolFlagNames returns the names of every flag in fs whose value needs no separate
// argument. It replaces the hand-maintained literal list that reorderAdoptArgs used to
// carry, so adding a flag to a FlagSet can no longer desynchronise the reordering below.
func boolFlagNames(fs *flag.FlagSet) map[string]bool {
	names := make(map[string]bool)
	fs.VisitAll(func(f *flag.Flag) {
		if bf, ok := f.Value.(interface{ IsBoolFlag() bool }); ok && bf.IsBoolFlag() {
			names[f.Name] = true
		}
	})
	return names
}

// reorderArgs moves flags ahead of positional arguments. Go's flag package stops parsing
// at the first non-flag argument, so without this the documented form
// `state sync <dir> --log=msg` would silently drop the flag. Everything after a bare "--"
// terminator stays positional.
func reorderArgs(args []string, boolFlags map[string]bool) []string {
	flagArgs := make([]string, 0, len(args))
	posArgs := make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--" {
			posArgs = append(posArgs, args[i+1:]...)
			break
		}
		if len(arg) < 2 || !strings.HasPrefix(arg, "-") {
			posArgs = append(posArgs, arg)
			continue
		}
		flagArgs = append(flagArgs, arg)
		if i+1 < len(args) && flagNeedsValue(arg, args[i+1], boolFlags) {
			i++
			flagArgs = append(flagArgs, args[i])
		}
	}
	return append(flagArgs, posArgs...)
}

// flagNeedsValue reports whether arg consumes next as its value: only a non-boolean flag
// written without "=" and followed by a non-flag token does.
func flagNeedsValue(arg, next string, boolFlags map[string]bool) bool {
	if strings.Contains(arg, "=") || strings.HasPrefix(next, "-") {
		return false
	}
	return !boolFlags[strings.TrimLeft(arg, "-")]
}

// resolveHomeSubdir returns explicit when it is set, and otherwise joins segs onto the
// user's home directory. It never degrades to a working-directory-relative path: when the
// home directory cannot be resolved the caller gets an error naming the flag to pass
// instead, rather than silently retargeting the command at the current directory.
func resolveHomeSubdir(explicit, flagName string, segs ...string) (string, error) {
	if explicit != "" {
		return explicit, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home directory (pass %s explicitly): %w", flagName, err)
	}
	if home == "" {
		return "", fmt.Errorf("home directory is empty: pass %s explicitly", flagName)
	}
	return filepath.Join(append([]string{home}, segs...)...), nil
}

// splitCommaList splits a comma-separated flag value, dropping empty entries.
func splitCommaList(value string) []string {
	if value == "" {
		return nil
	}
	items := make([]string, 0, strings.Count(value, ",")+1)
	for _, f := range strings.Split(value, ",") {
		if trimmed := strings.TrimSpace(f); trimmed != "" {
			items = append(items, trimmed)
		}
	}
	return items
}

func runAdopt(args []string) error {
	fs := flag.NewFlagSet("adopt", flag.ContinueOnError)
	profile := fs.String("profile", "", "Primary repository profile (auto-detected if empty)")
	facets := fs.String("facets", "", "Comma-separated list of facets")
	dryRun := fs.Bool("dry-run", false, "Simulate adoption without writing files")
	force := fs.Bool("force", false, "Overwrite existing standards configurations")
	recordBaseline := fs.Bool("record-baseline", true, "Record or estimate legacy debt using verified local pins and catalog, or --lock-source-root (including --dry-run)")
	lockSource := fs.String("lock-source-root", "", "Praetor source bundle with validated pins and local archetypes for missing lockfiles")
	allMissing := fs.Bool("all-missing", false, "Adopt all detected unmanaged repositories under --dev-dir")
	devDir := fs.String("dev-dir", "", "Root directory scanned by --all-missing "+devRootUsageDefault)
	path := fs.String("path", ".", "Target repository path to adopt")
	defaults := adopt.DefaultVerificationLimits()
	maxEntries := fs.Int("verification-max-entries", 0, fmt.Sprintf("Directory entries verification discovery may walk (default %d, ceiling %d)", defaults.MaxEntries, adopt.VerificationEntriesCeiling))
	maxFiles := fs.Int("verification-max-files", 0, fmt.Sprintf("Verification input files discovery may read (default %d, ceiling %d)", defaults.MaxFiles, adopt.VerificationFilesCeiling))
	maxDepth := fs.Int("verification-max-depth", 0, fmt.Sprintf("Directory depth verification discovery may descend (default %d, ceiling %d)", defaults.MaxDepth, adopt.VerificationDepthCeiling))

	if err := fs.Parse(reorderArgs(args, boolFlagNames(fs))); err != nil {
		return err
	}

	if fs.NArg() > 0 && *path == "." {
		*path = fs.Arg(0)
	}

	ctx, cancel := context.WithTimeout(context.Background(), adoptTimeout)
	defer cancel()

	if *allMissing {
		root, err := resolveDevRootDir(*devDir, "--dev-dir")
		if err != nil {
			return fmt.Errorf("adopt --all-missing: %w", err)
		}
		return batchAdoptMissing(ctx, root, *dryRun, *force, *recordBaseline, *lockSource)
	}

	opts := adopt.AdoptOptions{
		Path:               *path,
		LockSourceRoot:     *lockSource,
		Profile:            *profile,
		Facets:             splitCommaList(*facets),
		DryRun:             *dryRun,
		Force:              *force,
		RecordBaseline:     *recordBaseline,
		VerificationLimits: verificationLimitsFromFlags(*maxEntries, *maxFiles, *maxDepth),
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

// verificationLimitsFromFlags returns nil when no discovery bound was raised so adoption
// keeps its defaults. A raised bound leaves the others at their defaults; adoption itself
// validates every value against its ceilings, nothing is widened here.
func verificationLimitsFromFlags(entries, files, depth int) *adopt.VerificationLimits {
	if entries == 0 && files == 0 && depth == 0 {
		return nil
	}
	limits := adopt.DefaultVerificationLimits()
	if entries != 0 {
		limits.MaxEntries = entries
	}
	if files != 0 {
		limits.MaxFiles = files
	}
	if depth != 0 {
		limits.MaxDepth = depth
	}
	return &limits
}

func batchAdoptMissing(ctx context.Context, devDir string, dryRun, force, recordBaseline bool, sourceRoots ...string) error {
	if len(sourceRoots) > 1 {
		return fmt.Errorf("batch adoption accepts one lock source root")
	}
	var sourceRoot string
	if len(sourceRoots) == 1 {
		sourceRoot = sourceRoots[0]
	}
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
			LockSourceRoot: sourceRoot,
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

	if len(rep.Errors) > 0 {
		fmt.Println("\n--- Governance Pillars Not Synchronized (incomplete adoption) ---")
	} else if rep.DryRun {
		fmt.Println("\n--- Governance Pillars Planned (dry-run; not written) ---")
	} else {
		fmt.Println("\n--- Governance Pillars Synchronized ---")
	}
	if len(rep.Errors) == 0 {
		fmt.Println("  ✓ Universal Harness : Canonical AGENTS.md, caveman-linted by compile-context --verify and audit")
		fmt.Println("  ✓ AI Context Sync   : 6 targets (Claude Code, Cursor, Copilot, Windsurf, Codex, Gemini)")
		fmt.Println("  ✓ IDE Ecosystem     : VS Code, JetBrains (CLion/GoLand/PyCharm), Neovim")
		fmt.Println("  ✓ DevContainer      : Containerized deterministic dev environment (.devcontainer)")
		fmt.Println("  ✓ Verification Gate : Makefile 'verify-all' standard entrypoint")
	}

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
	switch rep.BaselineStatus {
	case "scanned":
		printScannedDebt(rep)
	case "existing":
		printExistingDebt(rep)
	case "skipped":
		fmt.Println("\n--- Legacy Technical Debt: scan skipped (baseline recording disabled) ---")
	default:
		status := rep.BaselineStatus
		if status == "" {
			status = "not_run"
		}
		fmt.Printf("\n--- Legacy Technical Debt: baseline %s; no usable baseline result ---\n", status)
	}
}

func printScannedDebt(rep *adopt.AdoptReport) {
	label := "Baselined"
	if rep.DryRun {
		label = "Scanned (dry-run; not written)"
	}
	if rep.LegacyDebtCount == 0 {
		fmt.Printf("\n--- Legacy Technical Debt: 0 infractions (%s) ---\n", label)
		return
	}
	fmt.Printf("\n--- Legacy Technical Debt %s (%d infractions) ---\n", label, rep.LegacyDebtCount)
	printDebtBreakdown(rep, !rep.DryRun)
}

func printExistingDebt(rep *adopt.AdoptReport) {
	fmt.Printf("\n--- Existing Legacy Technical Debt Baseline: %d infractions ---\n", rep.LegacyDebtCount)
}

func printDebtBreakdown(rep *adopt.AdoptReport, explain bool) {
	var ruleKeys []string
	for r := range rep.DebtBreakdown {
		ruleKeys = append(ruleKeys, r)
	}
	sort.Strings(ruleKeys)
	for _, r := range ruleKeys {
		fmt.Printf("  • %-8s: %d infractions\n", r, rep.DebtBreakdown[r])
	}
	if explain {
		fmt.Println("  (Infractions recorded into .standards-baseline.json to prevent CI breaks while ratcheting)")
	}
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
	if rep.DryRun {
		printPlannedFiles(rep)
		return
	}
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

func printPlannedFiles(rep *adopt.AdoptReport) {
	if len(rep.CreatedFiles) > 0 {
		fmt.Printf("\nFiles Planned for Creation (%d):\n", len(rep.CreatedFiles))
		for _, f := range rep.CreatedFiles {
			printAdoptedFile("+ [PLAN] ", f, findDetail(rep.ActionDetails, f))
		}
	}
	if len(rep.ReconciledFiles) > 0 {
		fmt.Printf("\nFiles Planned for Reconciliation (%d):\n", len(rep.ReconciledFiles))
		for _, f := range rep.ReconciledFiles {
			printAdoptedFile("~ [PLAN] ", f, findDetail(rep.ActionDetails, f))
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
