package main

import (
	"context"
	"flag"
	"fmt"
	"time"

	"github.com/cordanaLLM/praetor/internal/dedupe"
)

func runDedupe(args []string) error {
	if len(args) == 0 {
		printDedupeUsage()
		return nil
	}

	sub := args[0]
	subArgs := args[1:]

	switch sub {
	case "scan":
		return runDedupeScan(subArgs)
	case "cadence":
		return runDedupeCadence(subArgs)
	case "-h", "--help", "help":
		printDedupeUsage()
		return nil
	default:
		return fmt.Errorf("unknown dedupe subcommand: %s", sub)
	}
}

func printDedupeUsage() {
	fmt.Println("Usage: praetorctl dedupe <subcommand> [args]")
	fmt.Println("\nSubcommands:")
	fmt.Println("  scan [dir]                         Scan repository for AST clones and utility sprawl")
	fmt.Println("  cadence [--threshold=20] [--added-lines=1000] [--added-files=10] [--record]")
	fmt.Println("                                     Trigger a sweep every N commits or on Go source growth")
}

func runDedupeScan(args []string) error {
	dir := "."
	if len(args) > 0 {
		dir = args[0]
	}

	report, err := dedupe.ScanRepo(dir)
	if err != nil {
		return fmt.Errorf("dedupe scan failed: %w", err)
	}

	fmt.Printf("=== Codebase Deduplication & Unification Audit: %s ===\n", dir)
	if !report.Applicable {
		// Neither a pass nor a failure. The detector reads Go sources and found none, so it
		// has no verdict to give; printing one would certify a tree it never opened.
		fmt.Println("  Not applicable: no Go sources found; this detector reads Go only.")
		fmt.Println("  Clone detection for other languages is not implemented.")
		return nil
	}
	fmt.Printf("  Files Scanned:     %d\n", report.TotalFilesScanned)
	fmt.Printf("  Functions Scanned: %d\n", report.TotalFuncsScanned)
	fmt.Printf("  Cleanliness Score: %.1f%%\n", report.CleanlinessScore)
	fmt.Printf("  Passed:            %v\n", report.Passed)

	if len(report.Duplicates) > 0 {
		fmt.Printf("\nDuplicate Function Blocks (%d):\n", len(report.Duplicates))
		for i, d := range report.Duplicates {
			fmt.Printf("  [%d] %d lines (hash: %s):\n", i+1, d.LOC, d.Hash)
			for _, loc := range d.Locations {
				fmt.Printf("      - %s:%d in %s()\n", loc.Path, loc.Line, loc.FuncName)
			}
		}
	}

	if len(report.SprawlItems) > 0 {
		fmt.Printf("\nUtility Sprawl Infractions (%d):\n", len(report.SprawlItems))
		for _, sp := range report.SprawlItems {
			fmt.Printf("  - %s:%d: uses %s (should use %s)\n", sp.File, sp.Line, sp.Pattern, sp.Replacement)
		}
	}

	if !report.Passed {
		// The score is not the verdict: any finding fails the scan, so a single sprawl item
		// used to be reported as "failed with score 95.0%", which reads like a threshold the
		// repository missed rather than the one call site it has to fix.
		return fmt.Errorf("dedupe audit failed: %d duplicate function group(s), %d utility sprawl finding(s); cleanliness %.1f%%",
			len(report.Duplicates), len(report.SprawlItems), report.CleanlinessScore)
	}
	return nil
}

func runDedupeCadence(args []string) error {
	fs := flag.NewFlagSet("dedupe cadence", flag.ContinueOnError)
	threshold := fs.Int("threshold", dedupe.DefaultCadenceCommits, "Commit threshold between deduplication sweeps")
	addedLines := fs.Int("added-lines", dedupe.DefaultCadenceAddedLines, "Go source lines added since the last sweep that make a new one due")
	addedFiles := fs.Int("added-files", dedupe.DefaultCadenceAddedFiles, "Go source files added since the last sweep that make a new one due")
	record := fs.Bool("record", false, "Record cadence timestamp/commit upon execution")
	if err := fs.Parse(args); err != nil {
		return err
	}

	dir := "."
	if len(fs.Args()) > 0 {
		dir = fs.Args()[0]
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	limits := dedupe.CadenceLimits{Commits: *threshold, AddedLines: *addedLines, AddedFiles: *addedFiles}
	status, err := dedupe.CheckCadence(ctx, dir, limits)
	if err != nil {
		return fmt.Errorf("cadence check failed: %w", err)
	}

	fmt.Printf("Cadence check: %d commits, %d Go source lines and %d Go source files added since last sweep (thresholds: %d / %d / %d, trigger: %v)\n",
		status.CommitsSince, status.AddedLines, status.AddedFiles, *threshold, *addedLines, *addedFiles, status.Due)

	if status.Due {
		fmt.Printf("Triggering periodic codebase deduplication audit: %s\n", status.Reason)
		if err := runDedupeScan([]string{dir}); err != nil {
			return err
		}
		if *record {
			if err := dedupe.RecordCadence(ctx, dir, *threshold); err != nil {
				return fmt.Errorf("failed to record cadence: %w", err)
			}
			fmt.Println("Recorded cadence milestone in .workingdir/cadence.json")
		}
	}
	return nil
}
