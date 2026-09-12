package main

import (
	"context"
	"flag"
	"fmt"
	"time"

	"github.com/cordanaLLM/praetor/internal/dogfood"
)

func runDogfood(args []string) error {
	if len(args) > 0 && args[0] == "suite" {
		return runDogfoodSuite(context.Background(), args[1:])
	}
	return runDogfoodFlags(args)
}

func runDogfoodFlags(args []string) error {
	fs := flag.NewFlagSet("dogfood", flag.ContinueOnError)
	path := fs.String("path", ".", "Path to host repository")
	targets := fs.String("targets", "", "Directory containing target repositories for adoption testing")
	remote := fs.String("remote", "", "Comma-separated list of remote public Git URLs to test")
	benchmarkPopular := fs.Bool("benchmark-popular", false, "Benchmark against curated popular public OSS repositories")
	dryRun := fs.Bool("dry-run", true, "Execute adoption in dry-run simulation mode")
	reportPath := fs.String("report", "", "Path to write dogfood JSON report")
	verifyOnly := fs.Bool("verify-only", false, "Strict verify mode: exit with error if host fails audit")
	maxTargets := fs.Int("max-targets", 20, "Maximum target repositories to simulate")
	public := addPublicDogfoodFlags(fs)

	if err := fs.Parse(args); err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	if fs.NArg() != 0 {
		return fmt.Errorf("dogfood accepts flags only")
	}
	remoteURLs := splitCommaList(*remote)
	if *public.enabled {
		if incompatiblePublicFlags(*targets, *benchmarkPopular, *verifyOnly, *reportPath) {
			return fmt.Errorf("public-loop uses --remote and --artifacts; targets, benchmark-popular, verify-only and report are incompatible")
		}
		return runPublicDogfood(ctx, public, *path, remoteURLs, !*dryRun)
	}
	if *public.source != "" || *public.artifacts != "" || *public.attempts != 2 {
		return fmt.Errorf("source-root, artifacts and attempts require --public-loop")
	}

	opts := dogfood.DogfoodOptions{
		HostRepoPath:     *path,
		TargetReposDir:   *targets,
		RemoteRepos:      remoteURLs,
		BenchmarkPopular: *benchmarkPopular,
		ApplyAdoption:    !*dryRun,
		ReportPath:       *reportPath,
		VerifyOnly:       *verifyOnly,
		MaxScanTargets:   *maxTargets,
	}

	fmt.Printf("=== Praetor Universal Dogfooding & Adoption Suite ===\n")
	fmt.Printf("Host Repository: %s\n", *path)

	rep, err := dogfood.RunDogfood(ctx, opts)
	if err != nil {
		return fmt.Errorf("dogfood run failed: %w", err)
	}

	printDogfoodSummary(rep)
	if !rep.OverallPassed {
		return fmt.Errorf("dogfood verification failed: context sync passed=%t, self audit passed=%t, targets=%d, remotes=%d",
			rep.ContextSyncPassed, rep.SelfAuditPassed, len(rep.TargetResults), len(rep.RemoteResults))
	}
	return nil
}

func printRemoteResults(remotes []dogfood.RemoteAdoptionResult) {
	if len(remotes) == 0 {
		return
	}
	fmt.Printf("\nRemote Public Repository Benchmarks (%d repos):\n", len(remotes))
	for _, r := range remotes {
		status := "[PASS]"
		if !r.Passed {
			status = "[FAIL]"
		}
		if r.Error != "" {
			fmt.Printf("  %s %s: Error: %s (%dms)\n", status, r.RepoURL, r.Error, r.DurationMs)
			continue
		}
		fmt.Printf("  %s %s: Grade: %s | Archetype: %s | Debt: %d | HISS: %d | Actions: %d (%dms)\n",
			status, r.RepoURL, r.ReadinessGrade, r.Archetype, r.DebtCount, r.HISSInfractions, r.SimulatedActions, r.DurationMs)
	}
}

func printDogfoodSummary(rep *dogfood.DogfoodReport) {
	fmt.Println("\nGovernance Self-Audit:")
	if rep.ContextSyncPassed {
		fmt.Println("  [PASS] Cross-agent context targets in sync (AGENTS.md -> CLAUDE/Cursor/Gemini/Codex)")
	} else {
		fmt.Println("  [FAIL] Context targets out of sync")
	}

	if rep.SelfAuditPassed {
		fmt.Println("  [PASS] Host repository satisfies all HISS-16 invariants (0 infractions)")
	} else {
		fmt.Println("  [WARN] Host repository has active unbaselined HISS infractions")
	}

	if len(rep.TargetResults) > 0 {
		fmt.Printf("\nLocal Target Adoption Simulations (%d repos):\n", len(rep.TargetResults))
		for _, tr := range rep.TargetResults {
			status := "[PASS]"
			if !tr.Passed {
				status = "[FAIL]"
			}
			fmt.Printf("  %s %s: Archetype: %s | Debt: %d | Actions: %d\n", status, tr.RepoName, tr.Archetype, tr.DebtCount, tr.Actions)
		}
	}

	printRemoteResults(rep.RemoteResults)

	if rep.TotalSkillsAudited > 0 {
		fmt.Printf("\nWorkstation Agent Skills Audited: %d skills\n", rep.TotalSkillsAudited)
	}

	if rep.OverallPassed {
		fmt.Println("\n[SUCCESS] Dogfooding verification passed 100%. Ready for fleet adoption.")
	} else {
		fmt.Println("\n[WARNING] Dogfooding detected governance discrepancies.")
	}
}
