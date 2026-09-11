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
	fmt.Println("  cadence [--threshold=20] [--record] Check commit cadence and trigger sweep every N changes")
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
		return fmt.Errorf("dedupe audit failed with score %.1f%%", report.CleanlinessScore)
	}
	return nil
}

func runDedupeCadence(args []string) error {
	fs := flag.NewFlagSet("dedupe cadence", flag.ContinueOnError)
	threshold := fs.Int("threshold", 20, "Commit threshold between deduplication sweeps")
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

	shouldRun, delta, err := dedupe.CheckCadence(ctx, dir, *threshold)
	if err != nil {
		return fmt.Errorf("cadence check failed: %w", err)
	}

	fmt.Printf("Cadence check: %d commits since last sweep (threshold: %d, trigger: %v)\n",
		delta, *threshold, shouldRun)

	if shouldRun {
		fmt.Println("Triggering periodic codebase deduplication audit...")
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
