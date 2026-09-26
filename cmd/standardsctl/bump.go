package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"strings"
	"time"

	"github.com/cordanaLLM/praetor/internal/bump"
)

func runBump(args []string) error {
	if len(args) < 1 {
		printBumpUsage()
		return nil
	}

	sub := args[0]
	subArgs := args[1:]
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	switch sub {
	case "-h", "--help", "help":
		printBumpUsage()
		return nil
	case "audit":
		return runBumpAudit(ctx, subArgs)
	case "scan":
		return runBumpScan(ctx, subArgs)
	case "canary":
		return runBumpCanary(ctx, subArgs)
	case "train":
		return runBumpTrain(ctx, subArgs)
	case "apply":
		return runBumpApply(ctx, subArgs)
	case "update":
		return runBumpUpdate(ctx, subArgs)
	case "unify":
		return runBumpUnify(ctx, subArgs)
	default:
		return fmt.Errorf("unknown bump subcommand: %s", sub)
	}
}

func printBumpUsage() {
	fmt.Println("Usage: standardsctl bump <subcommand> [arguments]")
	fmt.Println("\nSubcommands:")
	fmt.Println("  audit [--prerelease] [--path=.]     Audit all dependencies and workflow actions for drift")
	fmt.Println("  scan [--prerelease] [--path=.]      Scan dependencies for pending stable and prerelease bumps")
	fmt.Println("  update [--all] [--path=.]           Update dependencies to latest versions")
	fmt.Println("  unify [--apply] [--path=.]          Reconcile dependencies against fleet catalog")
	fmt.Println("  canary <package> [--target=...]     Speculatively test bump in isolated ephemeral worktree")
	fmt.Println("  train [--dry-run]                   Run proactive bump train across all pending upgrades")
	fmt.Println("  apply <package> [--version=...]     Update dependency and apply an optional supplied patch")
}

func runBumpScan(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("bump scan", flag.ContinueOnError)
	path := fs.String("path", ".", "Target repository path")
	includePrerelease := fs.Bool("prerelease", true, "Include alpha, beta, rc, and nightly channels")
	if err := fs.Parse(args); err != nil {
		return err
	}

	rep, err := bump.ScanDependencies(ctx, *path, *includePrerelease)
	if err != nil {
		return fmt.Errorf("scan dependencies: %w", err)
	}

	fmt.Printf("=== Dependency Version Scan (%d dependencies scanned, %d candidates discovered) ===\n", rep.TotalScanned, rep.TotalCandidates)
	if len(rep.Stables) > 0 {
		fmt.Printf("\n[STABLE CHANNEL] (%d):\n", len(rep.Stables))
		for _, s := range rep.Stables {
			fmt.Printf("  - %s: %s -> %s (%s)\n", s.Package, s.CurrentVersion, s.TargetVersion, s.ManifestType)
		}
	}
	if len(rep.Prereleases) > 0 {
		fmt.Printf("\n[PRERELEASE CANARY CHANNEL] (%d):\n", len(rep.Prereleases))
		for _, p := range rep.Prereleases {
			fmt.Printf("  - [%s] %s: %s -> %s (%s)\n", p.Channel, p.Package, p.CurrentVersion, p.TargetVersion, p.ManifestType)
		}
	}
	return nil
}

func runBumpUpdate(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("bump update", flag.ContinueOnError)
	path := fs.String("path", ".", "Target repository path")
	all := fs.Bool("all", false, "Update all discovered outdated dependencies")
	prerelease := fs.Bool("prerelease", false, "Include prerelease versions")
	if err := fs.Parse(args); err != nil {
		return err
	}

	if *all {
		rep, err := bump.ScanDependencies(ctx, *path, *prerelease)
		if err != nil {
			return fmt.Errorf("scan dependencies: %w", err)
		}
		cands := rep.Stables
		if *prerelease {
			cands = append(cands, rep.Prereleases...)
		}
		if len(cands) == 0 {
			fmt.Println("All dependencies are already up-to-date.")
			return nil
		}
		fmt.Printf("Updating %d dependencies in %s...\n", len(cands), *path)
		n, err := bump.UpdateAll(ctx, *path, cands)
		if err != nil {
			return fmt.Errorf("update all: %w", err)
		}
		fmt.Printf("Successfully updated %d dependencies.\n", n)
		return nil
	}

	if fs.NArg() < 1 {
		return fmt.Errorf("specify package or --all: standardsctl bump update <pkg>@<ver> OR standardsctl bump update --all")
	}

	spec := fs.Arg(0)
	parts := strings.Split(spec, "@")
	if len(parts) != 2 {
		return fmt.Errorf("format must be <package>@<version>")
	}
	cand := bump.UpgradeCandidate{
		Package:       parts[0],
		TargetVersion: parts[1],
		ManifestType:  "go.mod",
	}
	if err := bump.ApplyUpdate(ctx, *path, cand); err != nil {
		return err
	}
	fmt.Printf("Successfully updated %s to %s\n", parts[0], parts[1])
	return nil
}

func runBumpUnify(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("bump unify", flag.ContinueOnError)
	path := fs.String("path", ".", "Target repository path")
	apply := fs.Bool("apply", false, "Apply unified versions to repository")
	if err := fs.Parse(args); err != nil {
		return err
	}

	report, err := bump.ReconcileCatalogReport(ctx, *path)
	if err != nil {
		return fmt.Errorf("reconcile catalog: %w", err)
	}
	printCatalogDrift(report)

	candidates := report.Upgrades
	if len(candidates) == 0 {
		return nil
	}
	if *apply {
		n, err := bump.UpdateAll(ctx, *path, candidates)
		if err != nil {
			return fmt.Errorf("apply unified dependencies: %w", err)
		}
		fmt.Printf("Successfully unified %d dependencies with fleet catalog.\n", n)
	} else {
		fmt.Println("\nRun 'standardsctl bump unify --apply' to align dependencies with the fleet catalog.")
	}

	return nil
}

// printCatalogDrift prints the catalog report: upgrades first, then the dependencies unify
// leaves unchanged because they are ahead of the catalog or cannot be ordered.
func printCatalogDrift(report *bump.CatalogReport) {
	if len(report.Upgrades) == 0 && len(report.Ahead) == 0 && len(report.Unranked) == 0 {
		fmt.Println("All repository dependencies are unified with the fleet catalog.")
		return
	}
	if len(report.Upgrades) == 0 {
		fmt.Println("No repository dependency is behind the fleet catalog.")
	} else {
		fmt.Printf("=== Fleet Catalog Dependency Drift (%d packages) ===\n", len(report.Upgrades))
		for _, c := range report.Upgrades {
			fmt.Printf("  - %s: %s -> %s (%s)\n", c.Package, c.CurrentVersion, c.TargetVersion, c.ManifestType)
		}
	}
	printCatalogHeld("AHEAD OF CATALOG, left unchanged", report.Ahead)
	printCatalogHeld("NOT SEMVER-COMPARABLE, left unchanged", report.Unranked)
}

func printCatalogHeld(label string, held []bump.CatalogDrift) {
	if len(held) == 0 {
		return
	}
	fmt.Printf("\n[%s] (%d):\n", label, len(held))
	for _, d := range held {
		fmt.Printf("  - %s: %s (catalog %s, %s)\n", d.Package, d.CurrentVersion, d.CatalogVersion, d.ManifestType)
	}
}

func runBumpCanary(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("bump canary", flag.ContinueOnError)
	path := fs.String("path", ".", "Target repository path")
	targetVer := fs.String("target", "", "Target prerelease or stable version")
	dryRun := fs.Bool("dry-run", false, "Simulate canary test without creating worktree")
	retention := fs.Bool("retention", false, "Retain worktree on failure for debugging")
	positional, err := parseInterspersed(fs, args)
	if err != nil {
		return err
	}

	if len(positional) < 1 {
		return fmt.Errorf("package name required: praetorctl bump canary <package> [--target=...]")
	}
	pkg := positional[0]
	if *targetVer == "" {
		return fmt.Errorf("--target is required: praetorctl bump canary %s --target=<version>", pkg)
	}

	cand := newGoUpgradeCandidate(ctx, *path, pkg, *targetVer)

	opts := bump.CanaryOptions{
		RepoPath:  *path,
		Candidate: cand,
		DryRun:    *dryRun,
		Retention: *retention,
	}

	res, err := bump.RunCanary(ctx, opts)
	if res != nil {
		printCanaryResult(res)
	}
	if err != nil {
		return fmt.Errorf("canary execution failed: %w", err)
	}

	return nil
}

func printCanaryResult(res *bump.CanaryResult) {
	fmt.Println("=== Ephemeral Worktree Canary Result ===")
	fmt.Printf("Package:          %s\n", res.Candidate.Package)
	fmt.Printf("Target Version:   %s (%s)\n", res.Candidate.TargetVersion, res.Candidate.Channel)
	fmt.Printf("Canary Certified: %v\n", res.CanaryCertified)
	switch res.Status {
	case bump.CanaryPlanned:
		fmt.Println("Status:           [PLANNED] Dependency update and tests were not executed.")
	case bump.CanaryPassed:
		fmt.Println("Status:           [PASS] Configured test command exited zero; no certification issued.")
	case bump.CanaryCancelled:
		fmt.Println("Status:           [CANCELLED] Deadline or cancellation stopped the canary before it finished; no verdict on the candidate.")
	default:
		fmt.Println("Status:           [FAIL] Canary execution did not pass.")
		if res.DistilledErrors != "" {
			fmt.Printf("\n--- Distilled Breakage Diagnostics ---\n%s\n", res.DistilledErrors)
		}
	}
	if res.DiagnosticPath != "" {
		fmt.Printf("Diagnostics:      %s (SARIF; not an adaptation patch)\n", res.DiagnosticPath)
	}
}

func runBumpTrain(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("bump train", flag.ContinueOnError)
	path := fs.String("path", ".", "Target repository path")
	dryRun := fs.Bool("dry-run", false, "Simulate bump train without modifying files")
	if err := fs.Parse(args); err != nil {
		return err
	}

	rep, err := bump.ScanDependencies(ctx, *path, true)
	if err != nil {
		return fmt.Errorf("scan dependencies: %w", err)
	}

	fmt.Printf("=== Proactive Prerelease Bump Train (Candidates: %d, DryRun: %v) ===\n", rep.TotalCandidates, *dryRun)
	var failures []error
	for _, cand := range append(rep.Prereleases, rep.Stables...) {
		opts := bump.CanaryOptions{
			RepoPath:  *path,
			Candidate: cand,
			DryRun:    *dryRun,
		}
		res, err := bump.RunCanary(ctx, opts)
		if err != nil {
			fmt.Printf("  - [%s] %s: [%s] %v\n", cand.Channel, cand.Package, canaryErrorLabel(err), err)
			failures = append(failures, fmt.Errorf("canary %s: %w", cand.Package, err))
			continue
		}
		fmt.Printf("  - [%s] %s (%s): [%s] No certification issued\n", cand.Channel, cand.Package, cand.TargetVersion, res.Status)
	}
	return errors.Join(failures...)
}

// canaryErrorLabel names a failed train entry. A canary stopped by the train's
// shared deadline is labelled CANCELLED, so it does not read as a candidate
// that broke the build.
func canaryErrorLabel(err error) string {
	if errors.Is(err, bump.ErrCanaryCancelled) {
		return "CANCELLED"
	}
	return "ERROR"
}

func runBumpApply(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("bump apply", flag.ContinueOnError)
	path := fs.String("path", ".", "Target repository path")
	version := fs.String("version", "", "Target version to apply")
	patch := fs.String("patch", "", "Optional path to adaptation patch")
	positional, err := parseInterspersed(fs, args)
	if err != nil {
		return err
	}

	if len(positional) < 1 {
		return fmt.Errorf("package name required: praetorctl bump apply <package> --version=<version>")
	}
	pkg := positional[0]
	if *version == "" {
		return fmt.Errorf("--version is required: praetorctl bump apply %s --version=<version>", pkg)
	}

	cand := newGoUpgradeCandidate(ctx, *path, pkg, *version)

	if err := bump.ApplyBump(ctx, *path, cand, *patch); err != nil {
		return fmt.Errorf("apply bump: %w", err)
	}

	fmt.Printf("[APPLIED] Successfully bumped %s to %s\n", pkg, *version)
	if *patch != "" {
		fmt.Printf("[PATCHED] Applied adaptation patch from %s\n", *patch)
	}
	return nil
}

// newGoUpgradeCandidate builds a go.mod upgrade candidate whose CurrentVersion is read
// from the repository instead of being a placeholder: internal/bump's go.mod fallback
// edit matches on "<package> <current version>", so a placeholder can never match.
func newGoUpgradeCandidate(ctx context.Context, repoPath, pkg, targetVersion string) bump.UpgradeCandidate {
	current, moduleDir, err := bump.CurrentGoModVersion(ctx, repoPath, pkg)
	if err != nil {
		fmt.Printf("[WARN] no current go.mod version for %s under %s: %v\n", pkg, repoPath, err)
	}
	return bump.UpgradeCandidate{
		Package:        pkg,
		CurrentVersion: current,
		TargetVersion:  targetVersion,
		Channel:        bump.ClassifyChannel(targetVersion),
		ManifestType:   "go.mod",
		ModuleDir:      moduleDir,
	}
}

func runBumpAudit(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("bump audit", flag.ContinueOnError)
	path := fs.String("path", ".", "Target repository path")
	prerelease := fs.Bool("prerelease", false, "Include prerelease channels")
	if err := fs.Parse(args); err != nil {
		return err
	}

	report, err := bump.AuditCodebaseVersions(ctx, *path, *prerelease)
	if err != nil {
		return err
	}

	fmt.Printf("=== Codebase Version Modernization Audit: %s ===\n", *path)
	fmt.Printf("  Modernization Score: %.1f%%\n", report.ModernizationScore)
	fmt.Printf("  Total Scanned:       %d components\n", report.TotalScanned)
	fmt.Printf("  Up To Date:          %d components\n", report.UpToDate)
	fmt.Printf("  Pending Upgrades:    %d\n", len(report.PendingUpgrades))
	fmt.Printf("  Deprecations:        %d\n\n", len(report.Deprecations))

	printActionsInventory(report.Actions)
	printPendingUpgrades(report.PendingUpgrades)
	printDeprecations(report.Deprecations)
	return nil
}

func printActionsInventory(actions []bump.ActionCandidate) {
	if len(actions) == 0 {
		return
	}
	fmt.Println("GitHub Actions Inventory:")
	for _, a := range actions {
		fmt.Printf("  %-12s %-32s %s -> %s (%s)\n",
			actionDriftStatus(a), a.Action, a.CurrentVersion, a.LatestVersion, a.WorkflowFile)
	}
	fmt.Println()
}

func actionDriftStatus(a bump.ActionCandidate) string {
	switch {
	case a.Deprecated:
		return "[DEPRECATED]"
	case a.CurrentVersion != a.LatestVersion:
		return "[DRIFT]"
	default:
		return "[UP-TO-DATE]"
	}
}

func printPendingUpgrades(upgrades []bump.UpgradeCandidate) {
	if len(upgrades) == 0 {
		return
	}
	fmt.Println("Pending Dependency Upgrades:")
	for _, u := range upgrades {
		fmt.Printf("  - [%s] %-32s %s -> %s (%s)\n", u.Channel, u.Package, u.CurrentVersion, u.TargetVersion, u.ManifestType)
	}
	fmt.Println()
}

func printDeprecations(deprecations []bump.DeprecationWarning) {
	if len(deprecations) == 0 {
		return
	}
	fmt.Println("Deprecation Warnings & Breaking Advisories:")
	for _, d := range deprecations {
		fmt.Printf("  ! [%s] %s: %s\n", d.Kind, d.Component, d.Details)
	}
	fmt.Println()
}
