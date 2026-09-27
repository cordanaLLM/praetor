package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"
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
	fmt.Println("  audit [--dev-root=...] Audits dev tree against the workstation topology rules DEV-01 and DEV-02")
	fmt.Println("  clean [--dev-root=...] [--dry-run=true|false] Safely removes stray governance files from org roots")
	fmt.Println("\nBoth accept --fleet-config, --workstation-config and --manifest; the selected operator settings'")
	fmt.Println("topology.org_containers names organization folders beyond the built-in ones. A directory")
	fmt.Println("without a .git of its own that holds a child repository is recognised as one without")
	fmt.Println("configuration.")
}

// topologyContainers loads the operator's topology.org_containers through the shared
// operator-settings loader.
func topologyContainers(ctx context.Context, settings *operatorSettingsFlags) ([]string, error) {
	loaded, err := settings.load(ctx)
	if err != nil {
		return nil, err
	}
	return loaded.Topology.OrgContainers, nil
}

// defaultDevRoot returns the resolved dev root ($PRAETOR_DEV_ROOT, $PRAETOR_DEV_DIR or
// <home>/dev) when it exists and is a directory. It never falls back to a hard-coded
// workstation path: on a machine without that directory the caller must pass --dev-root
// rather than have the command operate on somebody else's tree.
func defaultDevRoot() (string, error) {
	devPath, err := resolveDevRootDir("", "--dev-root")
	if err != nil {
		return "", err
	}
	info, err := os.Stat(devPath)
	if err != nil {
		return "", fmt.Errorf("stat %s: %w", devPath, err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("%s is not a directory", devPath)
	}
	return devPath, nil
}

// resolveDevRoot picks the dev root from --dev-root, then the first positional argument,
// and only then from the resolved default dev root.
func resolveDevRoot(flagValue string, positional []string) (string, error) {
	if flagValue != "" {
		return flagValue, nil
	}
	if len(positional) > 0 && positional[0] != "" {
		return positional[0], nil
	}
	root, err := defaultDevRoot()
	if err != nil {
		return "", fmt.Errorf("no --dev-root given and the default dev root is unusable: %w", err)
	}
	return root, nil
}

func runTopologyAudit(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("topology audit", flag.ContinueOnError)
	devRootFlag := fs.String("dev-root", "", "Target workstation dev root directory "+devRootUsageDefault)
	settings := registerOperatorSettingsFlags(fs)
	if err := fs.Parse(reorderArgs(args, boolFlagNames(fs))); err != nil {
		return err
	}
	devRoot, err := resolveDevRoot(*devRootFlag, fs.Args())
	if err != nil {
		return fmt.Errorf("topology audit: %w", err)
	}
	containers, err := topologyContainers(ctx, settings)
	if err != nil {
		return fmt.Errorf("topology audit: %w", err)
	}

	report, err := topology.AuditWorkstationTopology(ctx, devRoot, containers)
	if err != nil {
		return fmt.Errorf("topology audit failed: %w", err)
	}
	printTopologyAuditReport(report)
	return topologyAuditVerdict(report)
}

// printTopologyAuditReport prints the inventory, the invariant violations and the stray
// files an audit found.
func printTopologyAuditReport(report *topology.TopologyReport) {
	fmt.Printf("=== Workstation Topology Audit: %s ===\n", report.DevRoot)
	fmt.Printf("Organization Containers: %d (%v)\n", len(report.OrgContainers), report.OrgContainers)
	fmt.Printf("Valid Leaf Repositories: %d\n", len(report.ValidRepos))
	fmt.Printf("Compatibility Symlinks:  %d (%v)\n\n", len(report.Symlinks), report.Symlinks)

	printReportSection("Incomplete Scan", "!", report.TruncationReasons)
	printReportSection("Notes", "i", report.Notes)
	printReportSection("Invariant Violations", "✗", report.Violations)

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
	}
}

// printReportSection prints a titled, counted list of report lines, or nothing when the
// list is empty.
func printReportSection(title, marker string, lines []string) {
	if len(lines) == 0 {
		return
	}
	fmt.Printf("--- %s (%d) ---\n", title, len(lines))
	for _, line := range lines {
		fmt.Printf("  %s %s\n", marker, line)
	}
	fmt.Println()
}

// topologyAuditVerdict fails on any invariant violation or stray file. A violation alone,
// such as a repository placed directly in the dev root, used to fall through to the pass
// line; and that line claimed DEV-01 through DEV-05 while internal/topology evaluates only
// DEV-01 and DEV-02 (BUG-817). A truncated scan fails too: findings from a partial scan
// are a lower bound, so a pass line would claim coverage the audit never had (BUG-904).
func topologyAuditVerdict(report *topology.TopologyReport) error {
	problems := make([]string, 0, 3)
	if report.Truncated {
		problems = append(problems, fmt.Sprintf("an incomplete scan (%d truncation reasons)", len(report.TruncationReasons)))
	}
	if n := len(report.Violations); n > 0 {
		problems = append(problems, fmt.Sprintf("%d invariant violations", n))
	}
	if n := len(report.StrayFiles); n > 0 {
		problems = append(problems, fmt.Sprintf("%d stray governance files", n))
	}
	if len(problems) > 0 {
		return fmt.Errorf("topology audit failed with %s", strings.Join(problems, " and "))
	}
	fmt.Println("[PASS] Workstation topology complies with DEV-01 and DEV-02, the rules this audit evaluates.")
	return nil
}

func runTopologyClean(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("topology clean", flag.ContinueOnError)
	devRootFlag := fs.String("dev-root", "", "Target workstation dev root directory "+devRootUsageDefault)
	dryRun := fs.Bool("dry-run", true, "Simulate cleaning without deleting files")
	settings := registerOperatorSettingsFlags(fs)
	if err := fs.Parse(reorderArgs(args, boolFlagNames(fs))); err != nil {
		return err
	}
	devRoot, err := resolveDevRoot(*devRootFlag, fs.Args())
	if err != nil {
		return fmt.Errorf("topology clean: %w", err)
	}
	containers, err := topologyContainers(ctx, settings)
	if err != nil {
		return fmt.Errorf("topology clean: %w", err)
	}

	fmt.Printf("=== Workstation Topology Clean: %s (DryRun: %v) ===\n", devRoot, *dryRun)
	result, cleanErr := topology.CleanWorkstationTopologyDetailed(ctx, devRoot, containers, *dryRun)
	resultErr := printTopologyCleanResult(result, *dryRun, cleanErr == nil)
	if cleanErr != nil {
		return fmt.Errorf("topology clean failed: %w", cleanErr)
	}
	return resultErr
}

func printTopologyCleanResult(result *topology.CleanResult, dryRun, complete bool) error {
	if result == nil {
		return nil
	}
	if len(result.Cleaned) == 0 && len(result.Blocked) == 0 {
		if complete {
			fmt.Println("[INFO] No stray governance files found to clean.")
		}
		return nil
	}
	printCleanedTopologyEntries(result.Cleaned, dryRun)
	if len(result.Blocked) != 0 {
		printBlockedTopologyEntries(result.Blocked)
		return fmt.Errorf("topology clean incomplete: %d entries require manual review", len(result.Blocked))
	}
	if !complete {
		return nil
	}
	if dryRun {
		fmt.Println("\n[INFO] Dry-run complete. Pass --dry-run=false to execute cleanup.")
	} else {
		fmt.Printf("\n[PASS] Successfully cleaned %d safe stray files.\n", len(result.Cleaned))
	}
	return nil
}

func printCleanedTopologyEntries(cleaned []string, dryRun bool) {
	if len(cleaned) == 0 {
		return
	}
	action := "Removed"
	if dryRun {
		action = "Would remove"
	}
	fmt.Printf("\n%s %d safe stray files/directories:\n", action, len(cleaned))
	for _, path := range cleaned {
		fmt.Printf("  - %s\n", path)
	}
}

func printBlockedTopologyEntries(blocked []topology.StrayFile) {
	fmt.Printf("\nSkipped %d entries [MANUAL REVIEW REQUIRED]:\n", len(blocked))
	for _, finding := range blocked {
		fmt.Printf("  - %s (%s)\n", finding.RelPath, finding.Reason)
	}
}
