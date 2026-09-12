package main

import (
	"context"
	"flag"
	"fmt"
	"strings"
	"time"

	"github.com/cordanaLLM/praetor/internal/forge"
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
	fmt.Println("  reconcile [--repos=...] [--owner=cordanaLLM] [--dry-run] Reconcile cross-repo issue dependencies and tasklists")
}

func runIssueReconcile(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("issue reconcile", flag.ContinueOnError)
	owner := fs.String("owner", "cordanaLLM", "Default organization owner")
	reposFlag := fs.String("repos", "golusoris/golusoris,golusoris/sveltesentio,cordanaLLM/praetor", "Comma-separated repositories to reconcile")
	dryRun := fs.Bool("dry-run", true, "Simulate dependency resolution without applying changes")
	tokenFlag := fs.String("token", "", "Forge API token (default: GITHUB_TOKEN or gh auth token)")
	endpoint := fs.String("endpoint", "", "Forge API endpoint (default: https://api.github.com)")
	if err := fs.Parse(args); err != nil {
		return err
	}

	tok := resolveForgeAuthToken(ctx, *tokenFlag)
	engine := forge.NewReconcileEngine(*owner)
	targetRepos := parseTargetRepos(*reposFlag, *owner)

	if err := loadFleetIssues(ctx, tok, *endpoint, targetRepos, engine); err != nil {
		return fmt.Errorf("failed loading fleet issues: %w", err)
	}

	rep, err := engine.Reconcile(ctx)
	if err != nil {
		return fmt.Errorf("issue reconciliation failed: %w", err)
	}

	printReconciliationSummary(*owner, rep, *dryRun)

	if !*dryRun && tok != "" {
		return applyUnblockTransitions(ctx, tok, *endpoint, rep.UnblockedIssues)
	}
	return nil
}

func parseTargetRepos(reposFlag, defaultOwner string) []string {
	parts := strings.Split(reposFlag, ",")
	res := make([]string, 0, len(parts))
	for _, p := range parts {
		clean := strings.TrimSpace(p)
		if clean == "" {
			continue
		}
		if !strings.Contains(clean, "/") {
			clean = defaultOwner + "/" + clean
		}
		res = append(res, clean)
	}
	return res
}

func loadFleetIssues(ctx context.Context, token, endpoint string, repos []string, engine *forge.ReconcileEngine) error {
	for _, r := range repos {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		parts := strings.SplitN(r, "/", 2)
		if len(parts) != 2 {
			continue
		}
		ghDriver := forge.NewGitHubDriver(token, endpoint)
		ghDriver.SetRepository(parts[0], parts[1])

		issues, err := ghDriver.ListIssues(ctx, "all")
		if err != nil {
			// Non-fatal if offline or unauthenticated, proceed with best-effort
			fmt.Printf("[WARN] Could not fetch issues for %s: %v\n", r, err)
			continue
		}
		for _, issue := range issues {
			engine.TrackIssue(r, issue)
		}
	}
	return nil
}

// Label names of the blocked / ready transition. The transition is additive-then-removing
// on purpose: a PATCH carrying only these labels would replace the issue's whole label set
// and silently destroy every other label it carries.
const (
	blockedLabel       = "status/blocked"
	legacyBlockedLabel = "blocked"
	readyLabel         = "status/ready-for-work"
)

// transitionUnblocked adds the ready label and removes the blocked labels, preserving every
// other label on the issue.
func transitionUnblocked(ctx context.Context, gh *forge.GitHubDriver, number int) error {
	if err := gh.AddLabels(ctx, number, []string{readyLabel}); err != nil {
		return err
	}
	if err := gh.RemoveLabel(ctx, number, blockedLabel); err != nil {
		return err
	}
	return gh.RemoveLabel(ctx, number, legacyBlockedLabel)
}

func applyUnblockTransitions(ctx context.Context, token, endpoint string, unblocked []forge.UnblockAction) error {
	for _, u := range unblocked {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		parts := strings.SplitN(u.Repo, "/", 2)
		if len(parts) != 2 {
			continue
		}
		ghDriver := forge.NewGitHubDriver(token, endpoint)
		ghDriver.SetRepository(parts[0], parts[1])

		if err := transitionUnblocked(ctx, ghDriver, u.IssueNumber); err != nil {
			fmt.Printf("[WARN] Failed updating issue %s#%d: %v\n", u.Repo, u.IssueNumber, err)
			continue
		}
		fmt.Printf("[APPLIED] %s#%d transitioned to %s\n", u.Repo, u.IssueNumber, readyLabel)
	}
	return nil
}

func printReconciliationSummary(owner string, rep *forge.ReconciliationReport, dryRun bool) {
	fmt.Printf("=== Cross-Repo Dependency Reconciliation: %s ===\n", owner)
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

	if dryRun {
		fmt.Println("\n[INFO] Dry-run complete. Pass --dry-run=false to persist status transitions.")
	} else {
		fmt.Println("\n[PASS] Status transitions successfully applied across forge issue trackers.")
	}
}
