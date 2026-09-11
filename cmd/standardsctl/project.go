package main

import (
	"context"
	"flag"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/cordanaLLM/praetor/internal/forge"
)

func runProject(args []string) error {
	if len(args) == 0 {
		printProjectUsage()
		return nil
	}

	sub := args[0]
	subArgs := args[1:]
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	switch sub {
	case "list":
		return runProjectList(ctx, subArgs)
	case "add":
		return runProjectAdd(ctx, subArgs)
	case "status":
		return runProjectStatus(ctx, subArgs)
	case "-h", "--help", "help":
		printProjectUsage()
		return nil
	default:
		return fmt.Errorf("unknown project subcommand: %s", sub)
	}
}

func printProjectUsage() {
	fmt.Println("Usage: praetorctl project <subcommand> [arguments]")
	fmt.Println("\nSubcommands:")
	fmt.Println("  list [--owner=cordanaLLM] [--dir=.]             List GitHub Projects v2 boards")
	fmt.Println("  add <project-number> <issue-url> [--owner=...]   Add an issue or PR to project board")
	fmt.Println("  status [--dir=.]                                Inspect cached project boards and item counts")
}

func runProjectList(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("project list", flag.ContinueOnError)
	owner := fs.String("owner", "cordanaLLM", "GitHub organization or user owner")
	dir := fs.String("dir", ".", "Repository root directory")
	token := fs.String("token", "", "GitHub access token")
	endpoint := fs.String("endpoint", "", "GitHub GraphQL endpoint")
	if err := fs.Parse(args); err != nil {
		return err
	}

	pm := forge.NewProjectManager(*owner, *token, *endpoint)
	projects, err := pm.ListProjects(ctx, *dir)
	if err != nil {
		return fmt.Errorf("list projects failed: %w", err)
	}

	fmt.Printf("=== GitHub Projects (v2) for %s (%d boards) ===\n", *owner, len(projects))
	for _, p := range projects {
		stateStr := "OPEN"
		if p.Closed {
			stateStr = "CLOSED"
		}
		fmt.Printf("  #%-2d [%s] %-30s | Items: %-3d | URL: %s\n",
			p.Number, stateStr, p.Title, p.TotalItems, p.URL)
	}
	return nil
}

func runProjectAdd(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("project add", flag.ContinueOnError)
	owner := fs.String("owner", "cordanaLLM", "GitHub organization or user owner")
	dir := fs.String("dir", ".", "Repository root directory")
	token := fs.String("token", "", "GitHub access token")
	endpoint := fs.String("endpoint", "", "GitHub GraphQL endpoint")
	if err := fs.Parse(args); err != nil {
		return err
	}

	remArgs := fs.Args()
	if len(remArgs) < 2 {
		return fmt.Errorf("usage: praetorctl project add <project-number> <item-url> [--owner=...] [--dir=.]")
	}

	projectNum, err := strconv.Atoi(remArgs[0])
	if err != nil {
		return fmt.Errorf("invalid project number '%s': %w", remArgs[0], err)
	}
	itemURL := strings.TrimSpace(remArgs[1])

	pm := forge.NewProjectManager(*owner, *token, *endpoint)
	item, err := pm.AddItem(ctx, *dir, projectNum, itemURL)
	if err != nil {
		return fmt.Errorf("failed adding item to project #%d: %w", projectNum, err)
	}

	fmt.Printf("[PASS] Added item %s to Project #%d (ID: %s, Status: %s)\n",
		itemURL, projectNum, item.ID, item.Status)
	return nil
}

func runProjectStatus(ctx context.Context, args []string) error {
	dir := "."
	if len(args) > 0 {
		dir = args[0]
	}

	pm := forge.NewProjectManager("cordanaLLM", "", "")
	projects, err := pm.ListProjects(ctx, dir)
	if err != nil {
		return err
	}

	totalItems := 0
	for _, p := range projects {
		totalItems += p.TotalItems
	}

	fmt.Printf("=== Project Boards Status (%s) ===\n", dir)
	fmt.Printf("  Boards Tracked: %d\n", len(projects))
	fmt.Printf("  Total Items:    %d\n", totalItems)
	return nil
}
