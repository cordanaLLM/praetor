package main

import (
	"context"
	"flag"
	"fmt"
	"path/filepath"
	"time"

	"github.com/cordanaLLM/praetor/internal/baseline"
	"github.com/cordanaLLM/praetor/internal/hiss"
)

// baselineScanTimeout bounds the repository walk performed by --record (HISS-02).
const baselineScanTimeout = 5 * time.Minute

func runBaseline(args []string) error {
	fs := flag.NewFlagSet("baseline", flag.ContinueOnError)
	baselinePath := fs.String("file", ".standards-baseline.json", "Path to baseline file; its directory is the scanned root")
	record := fs.Bool("record", false, "Record current infractions into baseline file")
	allowIncrease := fs.Bool("allow-increase", false, "Permit --record to raise the infraction count (HISS-13 exception); requires --reason")
	reason := fs.String("reason", "", "Rationale stored in the baseline when --allow-increase raises the count")

	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		return fmt.Errorf("baseline accepts no positional arguments, got %q", fs.Args())
	}

	b, err := baseline.LoadBaseline(*baselinePath)
	if err != nil {
		return fmt.Errorf("failed to load baseline: %w", err)
	}

	if *record {
		opts := baseline.RecordOptions{AllowIncrease: *allowIncrease, Rationale: *reason}
		return recordBaseline(*baselinePath, b, opts)
	}

	printBaseline(*baselinePath, b)
	return nil
}

// recordBaseline rescans the repository around the baseline and replaces the snapshot,
// refusing to raise the count unless the operator allowed it with a rationale.
func recordBaseline(path string, previous *baseline.Baseline, opts baseline.RecordOptions) error {
	ctx, cancel := context.WithTimeout(context.Background(), baselineScanTimeout)
	defer cancel()

	scanRep, err := hiss.Scan(ctx, filepath.Dir(path), hiss.ScanOptions{})
	if err != nil {
		return fmt.Errorf("failed to scan for baseline infractions: %w", err)
	}

	next, err := baseline.Record(previous, fingerprintViolations(scanRep.Violations), opts)
	if err != nil {
		return fmt.Errorf("refusing to record %s: %w (pass --allow-increase --reason=<why> to record a deliberate increase)", path, err)
	}
	if err := baseline.SaveBaseline(path, next); err != nil {
		return fmt.Errorf("failed to save baseline: %w", err)
	}

	fmt.Printf("Baseline successfully updated: %s (Total: %d infractions, previously %d)\n", path, next.TotalInfractions, previous.Count())
	if next.IncreaseRationale != "" {
		fmt.Printf("[WARN] Debt increased deliberately; recorded rationale: %s\n", next.IncreaseRationale)
	}
	return nil
}

// printBaseline lists the recorded infractions.
func printBaseline(path string, b *baseline.Baseline) {
	repo := b.Repository
	if repo == "" {
		repo = "repository"
	}
	fmt.Printf("=== %s Technical Debt Baseline ===\n", repo)
	fmt.Printf("File: %s | Total Infractions: %d\n", path, b.TotalInfractions)
	if b.IncreaseRationale != "" {
		fmt.Printf("Recorded increase rationale: %s\n", b.IncreaseRationale)
	}
	for i, inf := range b.Infractions {
		fmt.Printf("  #%d [%s] %s:%d (%s) - %s\n", i+1, inf.RuleID, inf.FilePath, inf.LineNumber, inf.Symbol, inf.Message)
	}

	if b.TotalInfractions == 0 {
		fmt.Println("Zero technical debt recorded. Repository is 100% compliant.")
	}
}
