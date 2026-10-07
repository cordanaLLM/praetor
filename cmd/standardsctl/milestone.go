package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"strings"
	"time"

	"github.com/cordanaLLM/praetor/internal/milestone"
)

func runMilestone(args []string) error {
	if len(args) == 0 {
		printMilestoneUsage()
		return nil
	}

	sub := args[0]
	subArgs := args[1:]
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	switch sub {
	case "list":
		return runMilestoneList(ctx, subArgs)
	case "create":
		return runMilestoneCreate(ctx, subArgs)
	case "close":
		return runMilestoneClose(ctx, subArgs)
	case "sync":
		return runMilestoneSync(ctx, subArgs)
	case "status":
		return runMilestoneStatus(ctx, subArgs)
	case "-h", "--help", "help":
		printMilestoneUsage()
		return nil
	default:
		return fmt.Errorf("unknown milestone subcommand: %s", sub)
	}
}

func printMilestoneUsage() {
	fmt.Println("Usage: praetorctl milestone <subcommand> [arguments]")
	fmt.Println("\nSubcommands:")
	fmt.Println("  list [--dir=.] [--state=all|open|closed]   List tracked milestones")
	fmt.Println("  create --title=\"...\" [--due=YYYY-MM-DD]    Create a new milestone and render in BACKLOG.md")
	fmt.Println("  close <number|title> [--dir=.] [--publish] [--force] Close a milestone without open issues; --publish also closes it on GitHub")
	fmt.Println("  sync [--owner=...] [--repo=...] [--dir=.]  Synchronize milestones with GitHub")
	fmt.Println("  status [--dir=.]                           Display progress summary across all milestones")
}

func runMilestoneList(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("milestone list", flag.ContinueOnError)
	dir := fs.String("dir", ".", "Repository root directory")
	stateFilter := fs.String("state", "all", "Filter by state: all, open, closed")
	if _, err := parseInterspersed(fs, args); err != nil {
		return err
	}

	items, err := milestone.ListMilestones(ctx, *dir, *stateFilter)
	if err != nil {
		return err
	}

	fmt.Printf("=== Tracked Milestones (%d total) ===\n", len(items))
	for _, m := range items {
		dueStr := "No due date"
		if m.DueOn != nil {
			dueStr = m.DueOn.Format("2006-01-02")
		}
		badge := "[OPEN]"
		if m.State == milestone.StateClosed {
			badge = "[CLOSED]"
		}
		fmt.Printf("  #%-2d %-8s | %-30s | Due: %-10s | Progress: %3.0f%%\n",
			m.Number, badge, m.Title, dueStr, m.Progress)
	}
	return nil
}

// milestoneForge holds the forge flags shared by every milestone subcommand that talks to
// GitHub, declared once so create, close and sync cannot drift apart.
type milestoneForge struct {
	owner, repo, token, endpoint *string
	settings                     *operatorSettingsFlags
}

// addMilestoneForgeFlags registers the shared forge flags on fs.
func addMilestoneForgeFlags(fs *flag.FlagSet) milestoneForge {
	return milestoneForge{
		owner:    fs.String("owner", "", "GitHub repository owner "+ownerDefaultHelp),
		repo:     fs.String("repo", "", "GitHub repository name (default: repository.name, else origin remote name)"),
		token:    fs.String("token", "", "GitHub access token"),
		endpoint: fs.String("endpoint", "https://api.github.com", "GitHub API endpoint"),
		settings: registerOperatorSettingsFlags(fs),
	}
}

// target resolves the GitHub repository the milestones of dir are published to: --owner and
// --repo, else dir's identity (resolveForgeRepository), with forge.default_owner as the last
// owner step. It runs only when a subcommand reaches GitHub, so local-only runs need none.
func (m milestoneForge) target(ctx context.Context, dir string) (string, string, error) {
	forgeSettings, err := loadForgeSettings(ctx, m.settings)
	if err != nil {
		return "", "", err
	}
	return resolveForgeRepository(ctx, dir, *m.owner, *m.repo, forgeSettings.DefaultOwner)
}

func runMilestoneCreate(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("milestone create", flag.ContinueOnError)
	title := fs.String("title", "", "Milestone title (required)")
	desc := fs.String("desc", "", "Milestone description")
	dueStr := fs.String("due", "", "Due date in YYYY-MM-DD format")
	dir := fs.String("dir", ".", "Repository root directory")
	publish := fs.Bool("publish", false, "Publish immediately to GitHub remote")
	remote := addMilestoneForgeFlags(fs)

	if _, err := parseInterspersed(fs, args); err != nil {
		return err
	}
	if strings.TrimSpace(*title) == "" {
		return fmt.Errorf("--title is required")
	}

	var parsedDue *time.Time
	if strings.TrimSpace(*dueStr) != "" {
		t, err := time.Parse("2006-01-02", strings.TrimSpace(*dueStr))
		if err != nil {
			return fmt.Errorf("invalid due date format (use YYYY-MM-DD): %w", err)
		}
		parsedDue = &t
	}

	m, err := milestone.CreateMilestone(ctx, *dir, *title, *desc, parsedDue)
	if err != nil {
		return fmt.Errorf("create milestone failed: %w", err)
	}
	fmt.Printf("[PASS] Created milestone #%d: %s (State: %s)\n", m.Number, m.Title, m.State)

	if *publish {
		owner, repo, err := remote.target(ctx, *dir)
		if err != nil {
			return fmt.Errorf("milestone created locally, but its GitHub repository is unknown: %w", err)
		}
		if err := milestone.PublishMilestone(ctx, *dir, owner, repo, *remote.token, *remote.endpoint, m); err != nil {
			return fmt.Errorf("milestone published locally, but GitHub publish failed: %w", err)
		}
		fmt.Printf("[PASS] Published milestone #%d as remote milestone #%d: https://github.com/%s/%s/milestone/%d\n",
			m.Number, m.RemoteNumber, owner, repo, m.RemoteNumber)
	}
	return nil
}

func runMilestoneClose(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("milestone close", flag.ContinueOnError)
	dirFlag := fs.String("dir", ".", "Repository root directory")
	publish := fs.Bool("publish", false, "Also close the bound milestone on GitHub and read it back")
	force := fs.Bool("force", false, "Close although the forge or the cached store reports open issues")
	remote := addMilestoneForgeFlags(fs)
	positional, err := parseInterspersed(fs, args)
	if err != nil {
		return err
	}
	if len(positional) < 1 || len(positional) > 2 {
		return fmt.Errorf("usage: praetorctl milestone close <number|title> [--dir=.] [--publish] [--force]")
	}
	selector := positional[0]
	// Preserve the legacy second positional directory while parsing --dir correctly.
	dir := positionalAt(positional, 1, *dirFlag)

	opts := milestone.CloseOptions{Force: *force}
	var owner, repo string
	if *publish {
		if owner, repo, opts.Remote, err = forgeMilestoneView(ctx, remote, dir, selector); err != nil {
			return err
		}
	}
	m, err := milestone.CloseMilestoneWith(ctx, dir, selector, opts)
	if errors.Is(err, milestone.ErrOpenIssues) {
		return fmt.Errorf("%w; pass --force to close it anyway", err)
	}
	if err != nil {
		return err
	}
	fmt.Printf("[PASS] Milestone #%d closed: %s (Progress: %.0f%%, %d open, %d closed issues)\n",
		m.Number, m.Title, m.Progress, m.OpenIssues, m.ClosedIssues)
	if !*publish {
		reportLocalOnlyClose(m)
		return nil
	}
	return publishMilestoneClose(ctx, remote, dir, owner, repo, m)
}

// reportLocalOnlyClose names the forge milestone a close without --publish left open.
func reportLocalOnlyClose(m *milestone.Milestone) {
	if m.PendingRemoteClose && m.RemoteNumber > 0 {
		fmt.Printf("[INFO] Closed locally only; remote milestone #%d stays open until `milestone close %d --publish`\n",
			m.RemoteNumber, m.Number)
	}
}

// publishMilestoneClose carries a local close to the bound forge milestone.
func publishMilestoneClose(ctx context.Context, remote milestoneForge, dir, owner, repo string, m *milestone.Milestone) error {
	if err := milestone.PublishClose(ctx, dir, owner, repo, *remote.token, *remote.endpoint, m); err != nil {
		return fmt.Errorf("milestone closed locally, but the GitHub close failed (sync reports it as pending): %w", err)
	}
	fmt.Printf("[PASS] Closed remote milestone #%d on https://github.com/%s/%s\n", m.RemoteNumber, owner, repo)
	return nil
}

// forgeMilestoneView resolves the GitHub repository of `milestone close --publish` and,
// for a milestone bound to the forge, reads the milestone's current issue counts, which
// the close then checks instead of the cached ones. A local-only milestone has no forge
// view: its close fails at the publish, as before.
func forgeMilestoneView(ctx context.Context, remote milestoneForge, dir, selector string) (string, string, *milestone.RemoteMilestone, error) {
	owner, repo, err := remote.target(ctx, dir)
	if err != nil {
		return "", "", nil, fmt.Errorf("milestone close --publish: its GitHub repository is unknown: %w", err)
	}
	m, err := milestone.SelectMilestone(ctx, dir, selector)
	if err != nil {
		return "", "", nil, err
	}
	if m.RemoteNumber <= 0 {
		return owner, repo, nil, nil
	}
	rm, err := milestone.FetchRemoteMilestone(ctx, owner, repo, *remote.token, *remote.endpoint, m.RemoteNumber)
	if err != nil {
		return "", "", nil, fmt.Errorf("milestone close --publish: read remote milestone #%d before the close: %w", m.RemoteNumber, err)
	}
	return owner, repo, &rm, nil
}

func runMilestoneSync(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("milestone sync", flag.ContinueOnError)
	dir := fs.String("dir", ".", "Repository root directory")
	remote := addMilestoneForgeFlags(fs)
	if _, err := parseInterspersed(fs, args); err != nil {
		return err
	}

	owner, repo, err := remote.target(ctx, *dir)
	if err != nil {
		return fmt.Errorf("milestone sync: %w", err)
	}
	result, err := milestone.SyncWithGitHub(ctx, *dir, owner, repo, *remote.token, *remote.endpoint)
	if err != nil {
		return fmt.Errorf("milestone sync failed: %w", err)
	}

	fmt.Printf("[PASS] Synchronized %d milestones from https://github.com/%s/%s\n",
		len(result.Milestones), owner, repo)
	printMilestoneSyncDrift(result)
	return nil
}

// printMilestoneSyncDrift reports every divergence the sync kept or repaired rather than
// overwrote.
func printMilestoneSyncDrift(result *milestone.SyncResult) {
	for _, number := range result.PendingCloses {
		fmt.Printf("[WARN] Milestone #%d is closed locally but open on GitHub; kept closed. Publish with `milestone close %d --publish`\n",
			number, number)
	}
	for _, stale := range result.StaleBindings {
		fmt.Printf("[WARN] Milestone #%d duplicated remote milestone #%d already bound to #%d; unbound and kept as a local-only milestone\n",
			stale.Local, stale.Remote, stale.Kept)
	}
}

func runMilestoneStatus(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("milestone status", flag.ContinueOnError)
	dirFlag := fs.String("dir", ".", "Repository root directory")
	positional, err := parseInterspersed(fs, args)
	if err != nil {
		return err
	}
	if len(positional) > 1 {
		return fmt.Errorf("milestone status accepts at most one directory, got %q", positional)
	}
	dir := positionalAt(positional, 0, *dirFlag)

	items, err := milestone.ListMilestones(ctx, dir, "all")
	if err != nil {
		return err
	}

	var openCount, closedCount int
	var totalProgress float64
	for _, m := range items {
		if m.State == milestone.StateClosed {
			closedCount++
			totalProgress += 100.0
		} else {
			openCount++
			totalProgress += m.Progress
		}
	}

	overallPct := 0.0
	if len(items) > 0 {
		overallPct = totalProgress / float64(len(items))
	}

	fmt.Printf("=== Milestones Summary (%s) ===\n", dir)
	fmt.Printf("  Total:       %d\n", len(items))
	fmt.Printf("  Open:        %d\n", openCount)
	fmt.Printf("  Closed:      %d\n", closedCount)
	fmt.Printf("  Average:     %.1f%%\n", overallPct)
	return nil
}
