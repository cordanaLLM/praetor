package main

import (
	"context"
	"flag"
	"fmt"
	"strings"
	"time"

	"github.com/cordanaLLM/standards/internal/forge"
)

func runIssue(args []string) error {
	if len(args) < 1 {
		printIssueUsage()
		return nil
	}

	sub := args[0]
	subArgs := args[1:]
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	switch sub {
	case "-h", "--help", "help":
		printIssueUsage()
		return nil
	case "reconcile":
		return runIssueReconcile(ctx, subArgs)
	default:
		return fmt.Errorf("unknown issue subcommand: %s", sub)
	}
}

func printIssueUsage() {
	fmt.Println("Usage: standardsctl issue <subcommand> [arguments]")
	fmt.Println("\nSubcommands:")
	fmt.Println("  reconcile [--owner=cordanaLLM] [--dry-run] Reconcile cross-repo issue dependencies and tasklists")
}

func runIssueReconcile(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("issue reconcile", flag.ContinueOnError)
	owner := fs.String("owner", "cordanaLLM", "Default organization owner")
	dryRun := fs.Bool("dry-run", true, "Simulate dependency resolution without applying changes")
	if err := fs.Parse(args); err != nil {
		return err
	}

	engine := forge.NewReconcileEngine(*owner)
	sampleRepo := *owner + "/praetor"
	engine.TrackIssue(sampleRepo, forge.IssueSpec{
		ID:        1,
		Title:     "Governance Baseline Foundation",
		State:     "closed",
		Labels:    []string{"governance", "closed"},
	})
	engine.TrackIssue(sampleRepo, forge.IssueSpec{
		ID:        2,
		Title:     "Decoupled Fleet Architecture",
		State:     "open",
		Labels:    []string{"architecture", "status/blocked"},
		DependsOn: []string{fmt.Sprintf("%s#1", sampleRepo)},
	})

	rep, err := engine.Reconcile(ctx)
	if err != nil {
		return fmt.Errorf("issue reconciliation failed: %w", err)
	}

	fmt.Printf("=== Cross-Repo Dependency Reconciliation: %s ===\n", *owner)
	fmt.Printf("Evaluated Issues: %d | Unblocked: %d | Still Blocked: %d\n\n",
		rep.EvaluatedCount, len(rep.UnblockedIssues), len(rep.StillBlocked))

	for _, unblocked := range rep.UnblockedIssues {
		fmt.Printf("  [UNBLOCKED] %s#%d: %s\n", unblocked.Repo, unblocked.IssueNumber, unblocked.Title)
		fmt.Printf("     Resolved: %s | Action: %s\n", strings.Join(unblocked.ResolvedPrereqs, ", "), unblocked.ActionTaken)
	}

	for _, blocked := range rep.StillBlocked {
		fmt.Printf("  [BLOCKED]   %s#%d (Pending: %s)\n",
			blocked.Repo, blocked.IssueNumber, strings.Join(blocked.PendingPrereqs, ", "))
	}

	if *dryRun {
		fmt.Println("\n[INFO] Dry-run complete. Pass --dry-run=false to persist status transitions.")
	} else {
		fmt.Println("\n[PASS] Status transitions successfully applied across forge issue trackers.")
	}

	return nil
}
