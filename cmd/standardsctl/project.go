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
	fmt.Println("  list [--owner=<owner>] [--dir=.]                List GitHub Projects v2 boards")
	fmt.Println("  add <project-number> <issue-url> [--owner=...]   Add an issue or PR to project board")
	fmt.Println("  status [--dir=.]                                Inspect cached project boards and item counts")
}

// projectFlags are the flags project list and project add share.
type projectFlags struct {
	owner, dir, token, endpoint *string
	settings                    *operatorSettingsFlags
}

// addProjectFlags registers the shared project flags on fs.
func addProjectFlags(fs *flag.FlagSet) projectFlags {
	return projectFlags{
		owner:    fs.String("owner", "", "GitHub organization or user owning the boards "+ownerDefaultHelp),
		dir:      fs.String("dir", ".", "Repository root directory"),
		token:    fs.String("token", "", "GitHub access token"),
		endpoint: fs.String("endpoint", "", "GitHub GraphQL endpoint"),
		settings: registerOperatorSettingsFlags(fs),
	}
}

// manager resolves the board owner (resolveForgeOwner over --dir, forge.default_owner last)
// and returns the project manager acting for it. An unknown owner is an error: the boards of
// an owner nobody named are never read or written.
func (p projectFlags) manager(ctx context.Context) (*forge.ProjectManager, error) {
	forgeSettings, err := loadForgeSettings(ctx, p.settings)
	if err != nil {
		return nil, err
	}
	owner, err := resolveForgeOwner(ctx, *p.dir, *p.owner, forgeSettings.DefaultOwner)
	if err != nil {
		return nil, err
	}
	return forge.NewProjectManager(ctx, owner, *p.token, *p.endpoint), nil
}

func runProjectList(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("project list", flag.ContinueOnError)
	flags := addProjectFlags(fs)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("project list accepts no positional arguments, got %q", fs.Args())
	}

	pm, err := flags.manager(ctx)
	if err != nil {
		return fmt.Errorf("project list: %w", err)
	}
	projects, err := pm.ListProjects(ctx, *flags.dir)
	if err != nil {
		return fmt.Errorf("list projects failed: %w", err)
	}

	fmt.Printf("=== GitHub Projects (v2) for %s (%d boards) ===\n", pm.Owner, len(projects))
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
	flags := addProjectFlags(fs)
	remArgs, err := parseInterspersed(fs, args)
	if err != nil {
		return err
	}

	if len(remArgs) != 2 {
		return fmt.Errorf("usage: praetorctl project add <project-number> <item-url> [--owner=...] [--dir=.]")
	}

	projectNum, err := strconv.Atoi(remArgs[0])
	if err != nil {
		return fmt.Errorf("invalid project number '%s': %w", remArgs[0], err)
	}
	itemURL := strings.TrimSpace(remArgs[1])

	pm, err := flags.manager(ctx)
	if err != nil {
		return fmt.Errorf("project add: %w", err)
	}
	item, err := pm.AddItem(ctx, *flags.dir, projectNum, itemURL)
	if err != nil {
		return fmt.Errorf("failed adding item to project #%d: %w", projectNum, err)
	}

	if item.LocalOnly {
		fmt.Printf("[WARN] Recorded item %s for Project #%d in the local cache only "+
			"(ID: %s, Status: %s); no forge credentials were available, so the board was not updated\n",
			itemURL, projectNum, item.ID, item.Status)
		return nil
	}
	fmt.Printf("[PASS] Added item %s to Project #%d (ID: %s, Status: %s)\n",
		itemURL, projectNum, item.ID, item.Status)
	return nil
}

func runProjectStatus(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("project status", flag.ContinueOnError)
	dirFlag := fs.String("dir", ".", "Repository root directory")
	positional, err := parseInterspersed(fs, args)
	if err != nil {
		return err
	}
	if len(positional) > 1 {
		return fmt.Errorf("project status accepts at most one directory, got %q", positional)
	}
	dir := positionalAt(positional, 0, *dirFlag)

	// 'status' inspects the cached boards: it resolves no credentials and never reaches
	// the network, so it can neither block on a keyring nor rewrite the cache. The cache is
	// not keyed by owner and a credential-less manager never uses one, so none is resolved or
	// named here.
	pm := forge.NewCachedProjectManager("")
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
