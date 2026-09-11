package main

import (
	"context"
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
	fmt.Println("  scan [--prerelease] [--path=.]      Scan dependencies for pending stable and prerelease bumps")
	fmt.Println("  update [--all] [--path=.]           Update dependencies to latest versions")
	fmt.Println("  unify [--apply] [--path=.]          Reconcile dependencies against fleet catalog")
	fmt.Println("  canary <package> [--target=...]     Speculatively test bump in isolated ephemeral worktree")
	fmt.Println("  train [--dry-run]                   Run proactive bump train across all pending upgrades")
	fmt.Println("  apply <package> [--version=...]     Apply verified bump and adaptation patch to repository")
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

	fmt.Printf("=== Dependency Version Scan (%d candidates discovered) ===\n", rep.TotalCandidates)
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

	candidates, err := bump.ReconcileCatalog(ctx, *path)
	if err != nil {
		return fmt.Errorf("reconcile catalog: %w", err)
	}

	if len(candidates) == 0 {
		fmt.Println("All repository dependencies are unified with the fleet catalog.")
		return nil
	}

	fmt.Printf("=== Fleet Catalog Dependency Drift (%d packages) ===\n", len(candidates))
	for _, c := range candidates {
		fmt.Printf("  - %s: %s -> %s (%s)\n", c.Package, c.CurrentVersion, c.TargetVersion, c.ManifestType)
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

func runBumpCanary(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("bump canary", flag.ContinueOnError)
	path := fs.String("path", ".", "Target repository path")
	targetVer := fs.String("target", "", "Target prerelease or stable version")
	dryRun := fs.Bool("dry-run", false, "Simulate canary test without creating worktree")
	retention := fs.Bool("retention", false, "Retain worktree on failure for debugging")
	var flagArgs, posArgs []string
	for _, a := range args {
		if strings.HasPrefix(a, "-") {
			flagArgs = append(flagArgs, a)
		} else {
			posArgs = append(posArgs, a)
		}
	}
	if err := fs.Parse(append(flagArgs, posArgs...)); err != nil {
		return err
	}

	if fs.NArg() < 1 {
		return fmt.Errorf("package name required: standardsctl bump canary <package>")
	}
	pkg := fs.Arg(0)

	cand := bump.UpgradeCandidate{
		Package:        pkg,
		CurrentVersion: "current",
		TargetVersion:  *targetVer,
		Channel:        bump.ClassifyChannel(*targetVer),
		ManifestType:   "go.mod",
	}

	opts := bump.CanaryOptions{
		RepoPath:  *path,
		Candidate: cand,
		DryRun:    *dryRun,
		Retention: *retention,
	}

	res, err := bump.RunCanary(ctx, opts)
	if err != nil {
		return fmt.Errorf("canary execution failed: %w", err)
	}

	fmt.Println("=== Ephemeral Worktree Canary Result ===")
	fmt.Printf("Package:          %s\n", res.Candidate.Package)
	fmt.Printf("Target Version:   %s (%s)\n", res.Candidate.TargetVersion, res.Candidate.Channel)
	fmt.Printf("Canary Certified: %v\n", res.CanaryCertified)
	if res.Success {
		fmt.Println("Status:           [PASS] Tests and invariants passed cleanly.")
	} else {
		fmt.Println("Status:           [FAIL] Breakage detected in candidate version.")
		if res.DistilledErrors != "" {
			fmt.Printf("\n--- Distilled Breakage Diagnostics ---\n%s\n", res.DistilledErrors)
		}
	}
	return nil
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
	for _, cand := range append(rep.Prereleases, rep.Stables...) {
		opts := bump.CanaryOptions{
			RepoPath:  *path,
			Candidate: cand,
			DryRun:    *dryRun,
		}
		res, err := bump.RunCanary(ctx, opts)
		if err != nil {
			fmt.Printf("  - [%s] %s: [ERROR] %v\n", cand.Channel, cand.Package, err)
			continue
		}
		if res.CanaryCertified {
			fmt.Printf("  - [%s] %s (%s): [CERTIFIED] Ready for instant landing\n", cand.Channel, cand.Package, cand.TargetVersion)
		} else {
			fmt.Printf("  - [%s] %s (%s): [BREAKAGE] Adaptation patch staged\n", cand.Channel, cand.Package, cand.TargetVersion)
		}
	}
	return nil
}

func runBumpApply(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("bump apply", flag.ContinueOnError)
	path := fs.String("path", ".", "Target repository path")
	version := fs.String("version", "", "Target version to apply")
	patch := fs.String("patch", "", "Optional path to adaptation patch")
	if err := fs.Parse(args); err != nil {
		return err
	}

	if fs.NArg() < 1 {
		return fmt.Errorf("package name required: standardsctl bump apply <package>")
	}
	pkg := fs.Arg(0)

	cand := bump.UpgradeCandidate{
		Package:        pkg,
		CurrentVersion: "current",
		TargetVersion:  *version,
		ManifestType:   "go.mod",
	}

	if err := bump.ApplyBump(ctx, *path, cand, *patch); err != nil {
		return fmt.Errorf("apply bump: %w", err)
	}

	fmt.Printf("[APPLIED] Successfully bumped %s to %s\n", pkg, *version)
	if *patch != "" {
		fmt.Printf("[PATCHED] Applied adaptation patch from %s\n", *patch)
	}
	return nil
}
