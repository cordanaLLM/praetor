// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/cordanaLLM/praetor/internal/generated"
)

// generatedCommandTimeout bounds one generated-artefact command (HISS-02). A check or a
// rendering runs every render command in turn, each within its own declared timeout.
const generatedCommandTimeout = 2 * time.Hour

// runCIGenerated dispatches the generated-artefact subcommands (ADR-0017).
func runCIGenerated(args []string) error {
	if len(args) == 0 {
		printCIGeneratedUsage()
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), generatedCommandTimeout)
	defer cancel()
	switch args[0] {
	case "list":
		return runCIGeneratedList(ctx, args[1:])
	case "check":
		return runCIGeneratedCheck(ctx, args[1:])
	case "render":
		return runCIGeneratedRender(ctx, args[1:])
	case "-h", "--help", "help":
		printCIGeneratedUsage()
		return nil
	default:
		printCIGeneratedUsage()
		return fmt.Errorf("unknown ci generated subcommand: %s", args[0])
	}
}

func printCIGeneratedUsage() {
	fmt.Println("Usage: standardsctl ci generated <subcommand> [arguments]")
	fmt.Println("\nSubcommands:")
	fmt.Println("  list [--dir=.] [--json]             List the declared generated artefacts, built-in and declared")
	fmt.Println("  check [--dir=.] [--base=origin/main] [--head=HEAD] [--branch=<name>] [--title=<text>] [--json]")
	fmt.Println("                                      Pull-request mode: refuse an edit of a declared artefact unless the")
	fmt.Println("                                      branch and title carry the regeneration marker; render every artefact")
	fmt.Println("  render [--dir=.] [--check] [--json] Default-branch mode: render every artefact, write the changed ones,")
	fmt.Println("                                      or with --check exit non-zero when one differs")
}

// runCIGeneratedList prints the declared artefacts of the checkout.
func runCIGeneratedList(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("ci generated list", flag.ContinueOnError)
	dir := fs.String("dir", ".", "Repository root directory")
	asJSON := fs.Bool("json", false, "Print the declaration as JSON")
	if err := parseGeneratedFlags(fs, args); err != nil {
		return err
	}
	set, err := generated.List(ctx, *dir)
	if err != nil {
		return fmt.Errorf("ci generated list: %w", err)
	}
	if *asJSON {
		return printGeneratedJSON(set)
	}
	printGeneratedSet(set)
	return nil
}

// runCIGeneratedCheck judges the change base..head as a pull request.
func runCIGeneratedCheck(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("ci generated check", flag.ContinueOnError)
	opts := generated.CheckOptions{}
	fs.StringVar(&opts.Root, "dir", ".", "Repository root directory")
	fs.StringVar(&opts.Base, "base", "origin/main", "Base git reference the change merges into")
	fs.StringVar(&opts.Head, "head", "HEAD", "Head git reference or commit of the change")
	fs.StringVar(&opts.Branch, "branch", "", "Branch of the change, which carries the regeneration marker's prefix")
	fs.StringVar(&opts.Title, "title", "", "Title of the change, which carries the regeneration marker's conventional type")
	asJSON := fs.Bool("json", false, "Print the report as JSON")
	if err := parseGeneratedFlags(fs, args); err != nil {
		return err
	}
	report, err := generated.Check(ctx, opts)
	if err != nil {
		return fmt.Errorf("ci generated check: %w", err)
	}
	return emitGeneratedReport(report, *asJSON)
}

// runCIGeneratedRender renders the checkout's HEAD.
func runCIGeneratedRender(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("ci generated render", flag.ContinueOnError)
	opts := generated.RenderOptions{}
	fs.StringVar(&opts.Root, "dir", ".", "Repository root directory")
	fs.BoolVar(&opts.Check, "check", false, "Write nothing; exit non-zero when an artefact differs from its rendering")
	asJSON := fs.Bool("json", false, "Print the report as JSON")
	if err := parseGeneratedFlags(fs, args); err != nil {
		return err
	}
	report, err := generated.Render(ctx, opts)
	if err != nil {
		return fmt.Errorf("ci generated render: %w", err)
	}
	return emitGeneratedReport(report, *asJSON)
}

// parseGeneratedFlags parses a subcommand's flags and refuses positional arguments.
func parseGeneratedFlags(fs *flag.FlagSet, args []string) error {
	if _, err := parseInterspersed(fs, args); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		return fmt.Errorf("%s accepts no positional arguments, got %q", fs.Name(), fs.Args())
	}
	return nil
}

// printGeneratedJSON prints value as indented JSON.
func printGeneratedJSON(value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return fmt.Errorf("encode the generated-artefact report: %w", err)
	}
	fmt.Println(string(data))
	return nil
}

// printGeneratedSet prints the declaration, one artefact per block.
func printGeneratedSet(set *generated.Set) {
	fmt.Printf("=== Generated Artefacts: %d declared ===\n", len(set.Artefacts))
	fmt.Printf("  Regeneration marker: %s\n", set.Marker)
	for index := 0; index < len(set.Artefacts); index++ {
		artefact := set.Artefacts[index]
		if !artefact.Active {
			fmt.Printf("  [inactive] %s (%s): %s\n", artefact.Name, artefact.Origin, artefact.Reason)
			continue
		}
		fmt.Printf("  [active] %s (%s)\n", artefact.Name, artefact.Origin)
		fmt.Printf("      paths:   %s\n", strings.Join(artefact.Paths, ", "))
		if artefact.Block != nil {
			fmt.Printf("      block:   %s .. %s\n", artefact.Block.Start, artefact.Block.End)
		}
		fmt.Printf("      command: %s (timeout %s)\n", strings.Join(artefact.Command, " "), artefact.Timeout)
		fmt.Printf("      sources: %s\n", strings.Join(artefact.Sources, ", "))
		fmt.Printf("      files:   %d\n", len(artefact.Files))
	}
	for index := 0; index < len(set.Declined); index++ {
		fmt.Printf("  [declined] %s\n", set.Declined[index])
	}
}

// emitGeneratedReport prints a check or rendering report and fails when it did not pass.
func emitGeneratedReport(report *generated.Report, asJSON bool) error {
	if asJSON {
		if err := printGeneratedJSON(report); err != nil {
			return err
		}
	} else {
		printGeneratedReport(report)
	}
	if report.Passed {
		return nil
	}
	return fmt.Errorf("[FAIL] %d generated-artefact problem(s)", len(report.Problems))
}

// printGeneratedReport prints the report's summary, one line per artefact, its problems and
// the verdict.
func printGeneratedReport(report *generated.Report) {
	if report.Mode == generated.ModePullRequest {
		fmt.Printf("=== Generated Artefacts: pull-request check %s..%s ===\n", report.MergeBase, report.Head)
		fmt.Printf("  Changed paths: %d | Regeneration change: %t (%s)\n", report.ChangedPaths, report.Regeneration, report.Marker)
	} else {
		fmt.Printf("=== Generated Artefacts: render %s ===\n", report.Head)
	}
	for index := 0; index < len(report.Artefacts); index++ {
		fmt.Printf("  %s\n", describeGeneratedResult(report.Artefacts[index]))
	}
	for index := 0; index < len(report.Written); index++ {
		fmt.Printf("  wrote %s\n", report.Written[index])
	}
	for index := 0; index < len(report.Problems); index++ {
		fmt.Fprintf(os.Stderr, "  %s\n", report.Problems[index])
	}
	if report.Passed {
		fmt.Println("[PASS] every declared generated artefact passed.")
	}
}

// describeGeneratedResult is one artefact's report line.
func describeGeneratedResult(result generated.Result) string {
	parts := []string{result.Name + ": rendered " + result.Rendered}
	if !result.Active {
		parts[0] += " (" + result.Reason + ")"
	}
	for _, field := range []struct {
		label string
		paths []string
	}{{"edited", result.Edited}, {"sources changed", result.SourcesChanged}, {"differs from its rendering", result.Changed}} {
		if len(field.paths) > 0 {
			parts = append(parts, fmt.Sprintf("%s %s", field.label, strings.Join(field.paths, ", ")))
		}
	}
	return strings.Join(parts, "; ")
}
