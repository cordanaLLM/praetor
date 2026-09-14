package main

import (
	"flag"
	"fmt"

	"github.com/cordanaLLM/praetor/internal/changelog"
)

func runChangelog(args []string) error {
	if len(args) < 1 {
		printChangelogUsage()
		return nil
	}

	sub := args[0]
	subArgs := args[1:]

	switch sub {
	case "-h", "--help", "help":
		printChangelogUsage()
		return nil
	case "new":
		return runChangelogNew(subArgs)
	case "render":
		return runChangelogRender(subArgs)
	case "verify":
		return runChangelogVerify(subArgs)
	default:
		return fmt.Errorf("unknown changelog subcommand: %s", sub)
	}
}

func printChangelogUsage() {
	fmt.Println("Usage: standardsctl changelog <subcommand> [arguments]")
	fmt.Println("\nSubcommands:")
	fmt.Println("  new --type=<type> --title=<text> [--issue=N] [--breaking]  Create a changelog fragment")
	fmt.Println("  render [--version=X.Y.Z] [--date=YYYY-MM-DD]               Render fragments into CHANGELOG.md")
	fmt.Println("  verify                                                     Verify changelog integrity and fragments")
}

func runChangelogNew(args []string) error {
	fs := flag.NewFlagSet("changelog new", flag.ContinueOnError)
	cType := fs.String("type", "changed", "Fragment type: added, changed, deprecated, removed, fixed, security")
	title := fs.String("title", "", "Summary of the change")
	issue := fs.String("issue", "", "Optional issue / PR number")
	breaking := fs.Bool("breaking", false, "Mark change as breaking")

	if err := fs.Parse(args); err != nil {
		return err
	}

	if *title == "" {
		return fmt.Errorf("title is required: standardsctl changelog new --type=<type> --title=<text>")
	}

	frag := changelog.Fragment{
		Type:     changelog.FragmentType(*cType),
		Title:    *title,
		Issue:    *issue,
		Breaking: *breaking,
	}

	path, err := changelog.CreateFragment(".", frag)
	if err != nil {
		return err
	}

	fmt.Printf("Created changelog fragment: %s\n", path)
	return nil
}

func runChangelogRender(args []string) error {
	fs := flag.NewFlagSet("changelog render", flag.ContinueOnError)
	version := fs.String("version", "Unreleased", "Release version to render")
	date := fs.String("date", "", "Release date (YYYY-MM-DD, defaults to today)")

	if err := fs.Parse(args); err != nil {
		return err
	}

	if err := changelog.RenderRelease(".", *version, *date); err != nil {
		return err
	}

	fmt.Printf("Rendered release [%s] into CHANGELOG.md\n", *version)
	return nil
}

func runChangelogVerify(args []string) error {
	fragments, files, err := changelog.LoadFragments(".")
	if err != nil {
		return fmt.Errorf("load fragments: %w", err)
	}

	if len(fragments) == 0 {
		fmt.Println("[PASS] No pending unrendered changelog fragments.")
		return nil
	}

	fmt.Printf("[INFO] %d pending changelog fragments discovered:\n", len(fragments))
	for i, f := range fragments {
		fmt.Printf("  #%d [%s] %s (%s)\n", i+1, f.Type, f.Title, files[i])
	}
	return nil
}
