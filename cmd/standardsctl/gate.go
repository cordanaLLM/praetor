package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/cordanaLLM/standards/internal/gating"
)

func runGate(args []string) error {
	fs := flag.NewFlagSet("gate", flag.ContinueOnError)
	path := fs.String("path", ".", "Path to repository to verify against gating pipeline")
	dryRun := fs.Bool("dry-run", false, "Execute test stage in dry-run mode")
	asJSON := fs.Bool("json", false, "Output pipeline results as JSON")

	if err := fs.Parse(args); err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	fmt.Printf("=== Praetor Anti-Direct-Merge Gating Pipeline ===\n")
	fmt.Printf("Target Repository: %s (dry-run: %v)\n", *path, *dryRun)

	rep, err := gating.RunGatedPipeline(ctx, *path, *dryRun)
	if err != nil {
		return fmt.Errorf("gating pipeline execution failed: %w", err)
	}

	if *asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(rep)
	}

	printGatingReport(rep)
	if rep.Status == gating.StatusRejected {
		return fmt.Errorf("repository rejected by gating pipeline")
	}
	return nil
}

func printGatingReport(rep *gating.PipelineReport) {
	fmt.Printf("\nPipeline Result: %s (total: %v)\n", rep.Status, rep.TotalElapsed.Round(time.Millisecond))
	for idx, s := range rep.Stages {
		statusStr := "[PASS]"
		if !s.Passed {
			statusStr = "[FAIL]"
		}
		fmt.Printf("  %d. %s %-25s (%v)\n", idx+1, statusStr, s.Name, s.Duration.Round(time.Millisecond))
		if s.Message != "" {
			fmt.Printf("     Reason: %s\n", s.Message)
		}
	}
	if rep.ReceiptSignature != "" {
		fmt.Printf("\nExit-0 Receipt: .standards-receipt.json (Ed25519 signature: %s...)\n", rep.ReceiptSignature[:16])
	}
}
