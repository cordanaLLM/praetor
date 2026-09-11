// SPDX-License-Identifier: EUPL-1.2
// SPDX-FileCopyrightText: 2026 Lusoris <lusoris@proton.me>

package main

import (
	"context"
	"flag"
	"fmt"

	"github.com/cordanaLLM/praetor/internal/hindsight"
)

func runHindsight(args []string) error {
	if len(args) == 0 {
		printHindsightUsage()
		return nil
	}

	sub := args[0]
	subArgs := args[1:]
	ctx := context.Background()

	switch sub {
	case "distill":
		return runHindsightDistill(ctx, subArgs)
	case "recall":
		return runHindsightRecall(subArgs)
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

	if err := hindsight.SaveLocalCache(repoPath, report.Facts); err != nil {
		return err
	}

	fmt.Printf("=== Distilled Workspace Facts: %s ===\n", repoPath)
	fmt.Printf("  Total Facts: %d\n", report.TotalFacts)
	for cat, count := range report.Categories {
		fmt.Printf("    - %-20s: %d\n", cat, count)
	}
	fmt.Println("Facts cached into .workingdir/memory/distilled.json.")
	return nil
}

func runHindsightRecall(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: standardsctl hindsight recall <query> [path]")
	}
	query := args[0]
	repoPath := "."
	if len(args) > 1 {
		repoPath = args[1]
	}

	matches := hindsight.RecallLocalFacts(repoPath, query, "")
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

	facts, err := hindsight.LoadLocalCache(repoPath)
	if err != nil {
		return err
	}

	fmt.Printf("=== Local Hindsight Memory Audit: %s ===\n", repoPath)
	fmt.Printf("  Cached Facts: %d\n", len(facts))
	catCounts := make(map[hindsight.FactCategory]int)
	for _, f := range facts {
		catCounts[f.Category]++
	}
	for cat, count := range catCounts {
		fmt.Printf("    - %-20s: %d\n", cat, count)
	}
	return nil
}

func runHindsightSync(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("hindsight sync", flag.ContinueOnError)
	bank := fs.String("bank", "default", "Target Hindsight memory bank")
	offline := fs.Bool("offline", false, "Run in offline mode (dry-run without HTTP)")

	if err := fs.Parse(args); err != nil {
		return err
	}

	repoPath := "."
	if fs.NArg() > 0 {
		repoPath = fs.Arg(0)
	}

	report, err := hindsight.DistillWorkspace(ctx, repoPath)
	if err != nil {
		return err
	}

	cfg := hindsight.DefaultClientConfig()
	cfg.OfflineOnly = *offline
	client := hindsight.NewClient(cfg)
	defer client.Close()

	fmt.Printf("=== Syncing Facts to Hindsight Bank '%s' ===\n", *bank)
	for _, fact := range report.Facts {
		if err := client.IngestFact(ctx, *bank, fact); err != nil {
			return fmt.Errorf("failed to ingest fact %s: %w", fact.ID, err)
		}
	}
	fmt.Printf("Synced %d atomic facts successfully.\n", report.TotalFacts)
	return nil
}
