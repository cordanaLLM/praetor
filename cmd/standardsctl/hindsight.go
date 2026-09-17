// SPDX-License-Identifier: EUPL-1.2
// SPDX-FileCopyrightText: 2026 Lusoris <lusoris@proton.me>

package main

import (
	"context"
	"flag"
	"fmt"
	"time"

	"github.com/cordanaLLM/praetor/internal/hindsight"
)

const (
	// hindsightCommandTimeout bounds the whole hindsight command (HISS-02).
	hindsightCommandTimeout = 15 * time.Minute
	// maxSyncedFacts is the scalar upper bound (HISS-02) on the facts a single sync
	// ingests. The client rate-limits to 30 requests per minute, so an unbounded fact
	// list would otherwise pin the command for hours.
	maxSyncedFacts = 5000
)

func runHindsight(args []string) error {
	if len(args) == 0 {
		printHindsightUsage()
		return nil
	}

	sub := args[0]
	subArgs := args[1:]
	// HISS-02: every hindsight subcommand performs filesystem or network I/O, so the
	// whole command runs under an explicit deadline like its sibling commands.
	ctx, cancel := context.WithTimeout(context.Background(), hindsightCommandTimeout)
	defer cancel()

	switch sub {
	case "distill":
		return runHindsightDistill(ctx, subArgs)
	case "recall":
		return runHindsightRecall(ctx, subArgs)
	case "audit":
		return runHindsightAudit(ctx, subArgs)
	case "sync":
		return runHindsightSync(ctx, subArgs)
	case "-h", "--help", "help":
		printHindsightUsage()
		return nil
	default:
		printHindsightUsage()
		return fmt.Errorf("unknown hindsight subcommand: %s", sub)
	}
}

func printHindsightUsage() {
	fmt.Println("Usage: standardsctl hindsight <subcommand> [arguments]")
	fmt.Println("\nSubcommands:")
	fmt.Println("  distill [path]             Harvest atomic facts into local memory cache (.workingdir/memory/)")
	fmt.Println("  recall <query> [path]      Instant zero-token local memory recall")
	fmt.Println("  audit [path]               Inspect local memory health and categorized fact count")
	fmt.Println("  sync [path] [--bank=...]   Ingest facts into Hindsight server bank")
}

func runHindsightDistill(ctx context.Context, args []string) error {
	repoPath := "."
	if len(args) > 0 {
		repoPath = args[0]
	}

	report, err := hindsight.DistillWorkspace(ctx, repoPath)
	if err != nil {
		return err
	}

	if err := hindsight.SaveLocalCacheContext(ctx, repoPath, report.Facts); err != nil {
		return err
	}

	fmt.Printf("=== Distilled Workspace Facts: %s ===\n", repoPath)
	fmt.Printf("  Total Facts: %d\n", report.TotalFacts)
	for _, cat := range hindsight.SortedCategories(report.Categories) {
		fmt.Printf("    - %-20s: %d\n", cat, report.Categories[cat])
	}
	fmt.Println("Facts cached into .workingdir/memory/distilled.json.")
	return nil
}

func runHindsightRecall(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: standardsctl hindsight recall <query> [path]")
	}
	query := args[0]
	repoPath := "."
	if len(args) > 1 {
		repoPath = args[1]
	}

	matches, err := hindsight.RecallLocalFactsContext(ctx, repoPath, query, "")
	if err != nil {
		return err
	}
	if len(matches) == 0 {
		fmt.Printf("No local facts match '%s'.\n", query)
		return nil
	}

	fmt.Printf("=== Local Memory Recall: '%s' (%d matches) ===\n\n", query, len(matches))
	for _, m := range matches {
		fmt.Printf("[%s] %s\n  Statement: %s\n  Evidence:  %s\n\n", m.Category, m.Subject, m.Statement, m.Evidence)
	}
	return nil
}

func runHindsightAudit(ctx context.Context, args []string) error {
	repoPath := "."
	if len(args) > 0 {
		repoPath = args[0]
	}

	facts, err := hindsight.LoadLocalCacheContext(ctx, repoPath)
	if err != nil {
		return err
	}

	fmt.Printf("=== Local Hindsight Memory Audit: %s ===\n", repoPath)
	fmt.Printf("  Cached Facts: %d\n", len(facts))
	catCounts := make(map[hindsight.FactCategory]int)
	for _, f := range facts {
		catCounts[f.Category]++
	}
	for _, cat := range hindsight.SortedCategories(catCounts) {
		fmt.Printf("    - %-20s: %d\n", cat, catCounts[cat])
	}
	return nil
}

func runHindsightSync(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("hindsight sync", flag.ContinueOnError)
	bank := fs.String("bank", "default", "Target Hindsight memory bank")
	offline := fs.Bool("offline", false, "Run in offline mode (dry-run without HTTP)")

	positional, err := parseInterspersed(fs, args)
	if err != nil {
		return err
	}
	repoPath := positionalAt(positional, 0, ".")

	report, err := hindsight.DistillWorkspace(ctx, repoPath)
	if err != nil {
		return err
	}

	if *offline {
		// IngestFact returns nil without issuing a request in offline mode, so reporting
		// a successful sync here would claim a bank was populated that was never touched.
		fmt.Printf("=== Hindsight Bank '%s' (offline dry-run) ===\n", *bank)
		fmt.Printf("[DRY-RUN] Would ingest %d atomic facts; no request was made.\n", len(report.Facts))
		return nil
	}

	cfg := hindsight.DefaultClientConfig()
	client := hindsight.NewClient(cfg)
	defer client.Close()

	fmt.Printf("=== Syncing Facts to Hindsight Bank '%s' ===\n", *bank)
	if len(report.Facts) > maxSyncedFacts {
		return fmt.Errorf("refusing to sync %d facts: the per-invocation limit is %d",
			len(report.Facts), maxSyncedFacts)
	}
	ingested := 0
	for i := 0; i < len(report.Facts) && i < maxSyncedFacts; i++ {
		if err := client.IngestFact(ctx, *bank, report.Facts[i]); err != nil {
			return fmt.Errorf("ingested %d of %d facts, then failed on %s: %w",
				ingested, len(report.Facts), report.Facts[i].ID, err)
		}
		ingested++
	}
	fmt.Printf("Synced %d of %d atomic facts successfully.\n", ingested, len(report.Facts))
	return nil
}
