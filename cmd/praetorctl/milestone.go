package main

import (
	"context"
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
	fmt.Println("  close <number|title> [--dir=.]             Mark a milestone as closed")
	fmt.Println("  sync [--owner=...] [--repo=...] [--dir=.]  Synchronize milestones with GitHub")
	fmt.Println("  status [--dir=.]                           Display progress summary across all milestones")
}

func runMilestoneList(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("milestone list", flag.ContinueOnError)
	dir := fs.String("dir", ".", "Repository root directory")
	stateFilter := fs.String("state", "all", "Filter by state: all, open, closed")
	if err := fs.Parse(args); err != nil {
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

func runMilestoneCreate(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("milestone create", flag.ContinueOnError)
	title := fs.String("title", "", "Milestone title (required)")
	desc := fs.String("desc", "", "Milestone description")
	dueStr := fs.String("due", "", "Due date in YYYY-MM-DD format")
	dir := fs.String("dir", ".", "Repository root directory")
	publish := fs.Bool("publish", false, "Publish immediately to GitHub remote")
	owner := fs.String("owner", "cordanaLLM", "GitHub organization owner")
	repo := fs.String("repo", "praetor", "GitHub repository name")
	token := fs.String("token", "", "GitHub access token")
	endpoint := fs.String("endpoint", "https://api.github.com", "GitHub API endpoint")

	if err := fs.Parse(args); err != nil {
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
		if err := milestone.PublishMilestone(ctx, *dir, *owner, *repo, *token, *endpoint, m); err != nil {
			return fmt.Errorf("milestone published locally, but GitHub publish failed: %w", err)
		}
		fmt.Printf("[PASS] Published milestone #%d as remote milestone #%d: https://github.com/%s/%s/milestone/%d\n",
			m.Number, m.RemoteNumber, *owner, *repo, m.RemoteNumber)
	}
	return nil
}

func runMilestoneClose(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("milestone close", flag.ContinueOnError)
	dirFlag := fs.String("dir", ".", "Repository root directory")
	positional, err := parseInterspersed(fs, args)
	if err != nil {
		return err
	}
	if len(positional) < 1 || len(positional) > 2 {
		return fmt.Errorf("usage: praetorctl milestone close <number|title> [--dir=.]")
	}
	selector := positional[0]
	// Preserve the legacy second positional directory while parsing --dir correctly.
	dir := positionalAt(positional, 1, *dirFlag)

	m, err := milestone.CloseMilestone(ctx, dir, selector)
	if err != nil {
		return err
	}
	fmt.Printf("[PASS] Milestone #%d closed: %s (Progress: 100%%)\n", m.Number, m.Title)
	return nil
}

func runMilestoneSync(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("milestone sync", flag.ContinueOnError)
	owner := fs.String("owner", "cordanaLLM", "GitHub organization owner")
	repo := fs.String("repo", "praetor", "GitHub repository name")
	dir := fs.String("dir", ".", "Repository root directory")
	token := fs.String("token", "", "GitHub access token")
	endpoint := fs.String("endpoint", "https://api.github.com", "GitHub API endpoint")
	if err := fs.Parse(args); err != nil {
		return err
	}

	synced, err := milestone.SyncWithGitHub(ctx, *dir, *owner, *repo, *token, *endpoint)
	if err != nil {
		return fmt.Errorf("milestone sync failed: %w", err)
	}

	fmt.Printf("[PASS] Synchronized %d milestones from https://github.com/%s/%s\n", len(synced), *owner, *repo)
	return nil
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
