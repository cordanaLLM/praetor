package main

import (
	"context"
	"flag"
	"fmt"
	"strings"
	"time"

	"github.com/cordanaLLM/praetor/internal/forge"
)

// maxReconciledRepos and maxUnblockTransitions are the scalar upper bounds (HISS-02) on
// the reconciliation fan-out of a single command invocation.
const (
	maxReconciledRepos    = 256
	maxUnblockTransitions = 1000
)

// readyLabel is the label an unblocked issue is transitioned to.
const readyLabel = "status/ready-for-work"

// blockedLabels are the labels removed when an issue becomes unblocked.
var blockedLabels = []string{"status/blocked", "blocked"}

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
	fmt.Println("Usage: praetorctl issue <subcommand> [arguments]")
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
	if _, err := parseInterspersed(fs, args); err != nil {
		return err
	}

	tok := resolveForgeAuthToken(ctx, *tokenFlag)
	if !*dryRun && tok == "" {
		return fmt.Errorf("--dry-run=false needs a forge token: set GITHUB_TOKEN, sign in with gh, or pass --token")
	}

	engine := forge.NewReconcileEngine(*owner)
	labels := newIssueLabelIndex()
	if err := loadFleetIssues(ctx, tok, *endpoint, parseTargetRepos(*reposFlag, *owner), engine, labels); err != nil {
		return fmt.Errorf("failed loading fleet issues: %w", err)
	}

	rep, err := engine.Reconcile(ctx)
	if err != nil {
		return fmt.Errorf("issue reconciliation failed: %w", err)
	}

	// The transitions are applied before the summary is printed: printing "[PASS] applied"
	// ahead of the PATCH calls reported success for updates that had not happened yet.
	applied, failed := 0, 0
	if !*dryRun {
		applied, failed = applyUnblockTransitions(ctx, tok, *endpoint, rep.UnblockedIssues, labels)
	}

	printReconciliationSummary(*owner, rep, *dryRun, applied, failed)
	if failed > 0 {
		return fmt.Errorf("%d of %d status transitions failed", failed, applied+failed)
	}
	return nil
}

func parseTargetRepos(reposFlag, defaultOwner string) []string {
	parts := strings.Split(reposFlag, ",")
	res := make([]string, 0, len(parts))
	for i := 0; i < len(parts) && i < maxReconciledRepos; i++ {
		clean := strings.TrimSpace(parts[i])
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

// issueLabelIndex remembers the label set each tracked issue carries, keyed by
// "<owner>/<repo>#<number>". GitHub's PATCH .../issues/{n} `labels` field REPLACES the
// whole label set, so an unblock transition has to merge against the current labels
// instead of sending the single status label on its own.
type issueLabelIndex map[string][]string

func newIssueLabelIndex() issueLabelIndex {
	return make(issueLabelIndex)
}

func issueLabelKey(repo string, number int) string {
	return fmt.Sprintf("%s#%d", repo, number)
}

func (idx issueLabelIndex) record(repo string, issue forge.IssueSpec) {
	if idx == nil {
		return
	}
	idx[issueLabelKey(repo, issue.ID)] = issue.Labels
}

// mergedReadyLabels returns the label set an unblocked issue must end up with: its known
// labels minus the blocked markers, plus the ready label. known is false when the issue's
// current labels were never observed, in which case no replacing PATCH may be sent.
func (idx issueLabelIndex) mergedReadyLabels(repo string, number int) (merged []string, known bool) {
	current, known := idx[issueLabelKey(repo, number)]
	if !known {
		return nil, false
	}
	merged = make([]string, 0, len(current)+1)
	for _, label := range current {
		if isBlockedLabel(label) || strings.EqualFold(label, readyLabel) {
			continue
		}
		merged = append(merged, label)
	}
	return append(merged, readyLabel), true
}

func isBlockedLabel(label string) bool {
	for _, blocked := range blockedLabels {
		if strings.EqualFold(label, blocked) {
			return true
		}
	}
	return false
}

func loadFleetIssues(ctx context.Context, token, endpoint string, repos []string,
	engine *forge.ReconcileEngine, labels issueLabelIndex) error {
	for i := 0; i < len(repos) && i < maxReconciledRepos; i++ {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		owner, name, ok := strings.Cut(repos[i], "/")
		if !ok {
			continue
		}
		ghDriver := forge.NewGitHubDriver(token, endpoint)
		ghDriver.SetRepository(owner, name)

		issues, err := ghDriver.ListIssues(ctx, "all")
		if err != nil {
			// Non-fatal if offline or unauthenticated, proceed with best-effort
			fmt.Printf("[WARN] Could not fetch issues for %s: %v\n", repos[i], err)
			continue
		}
		for _, issue := range issues {
			engine.TrackIssue(repos[i], issue)
			labels.record(repos[i], issue)
		}
	}
	return nil
}

// applyUnblockTransitions performs the label transitions and returns how many succeeded
// and how many failed, so the caller can report the real outcome and exit non-zero.
func applyUnblockTransitions(ctx context.Context, token, endpoint string,
	unblocked []forge.UnblockAction, labels issueLabelIndex) (applied, failed int) {
	if len(unblocked) > maxUnblockTransitions {
		fmt.Printf("[WARN] Refusing %d transitions: maximum is %d\n", len(unblocked), maxUnblockTransitions)
		return 0, len(unblocked)
	}
	for i := 0; i < len(unblocked) && i < maxUnblockTransitions; i++ {
		u := unblocked[i]
		if ctx.Err() != nil {
			fmt.Printf("[WARN] Aborting transitions: %v\n", ctx.Err())
			return applied, failed + (len(unblocked) - i)
		}
		owner, name, ok := strings.Cut(u.Repo, "/")
		if !ok || owner == "" || name == "" {
			failed++
			continue
		}
		ghDriver := forge.NewGitHubDriver(token, endpoint)
		ghDriver.SetRepository(owner, name)

		_, known := labels.mergedReadyLabels(u.Repo, u.IssueNumber)
		if !known {
			fmt.Printf("[WARN] Skipping %s#%d: the issue was not observed in this reconciliation\n",
				u.Repo, u.IssueNumber)
			failed++
			continue
		}
		if err := transitionUnblocked(ctx, ghDriver, u.IssueNumber); err != nil {
			fmt.Printf("[WARN] Failed updating issue %s#%d: %v\n", u.Repo, u.IssueNumber, err)
			failed++
			continue
		}
		fmt.Printf("[APPLIED] %s#%d transitioned to %s\n", u.Repo, u.IssueNumber, readyLabel)
		applied++
	}
	return applied, failed
}

// transitionUnblocked uses additive APIs to preserve labels added since the scan.
func transitionUnblocked(ctx context.Context, gh *forge.GitHubDriver, number int) error {
	if err := gh.AddLabels(ctx, number, []string{readyLabel}); err != nil {
		return err
	}
	for _, label := range blockedLabels {
		if err := gh.RemoveLabel(ctx, number, label); err != nil {
			return err
		}
	}
	return nil
}

func printReconciliationSummary(owner string, rep *forge.ReconciliationReport, dryRun bool, applied, failed int) {
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
		return
	}
	if failed > 0 {
		fmt.Printf("\n[FAIL] %d status transition(s) applied, %d failed.\n", applied, failed)
		return
	}
	fmt.Printf("\n[PASS] %d status transition(s) applied across forge issue trackers.\n", applied)
}
