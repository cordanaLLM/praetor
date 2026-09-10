package main

import (
	"context"
	"flag"
	"fmt"
	"time"

	"github.com/cordanaLLM/standards/internal/gc"
)

func runGC(args []string) error {
	fs := flag.NewFlagSet("gc", flag.ContinueOnError)
	dryRun := fs.Bool("dry-run", false, "Simulate garbage collection without deleting files")
	rootDir := fs.String("path", ".", "Root repository directory")
	maxAge := fs.Duration("max-age", 24*time.Hour, "Maximum age for ephemeral worktrees")

	if err := fs.Parse(args); err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	opts := gc.Options{
		RootDir:        *rootDir,
		MaxWorktreeAge: *maxAge,
		DryRun:         *dryRun,
	}

	report, err := gc.Collect(ctx, opts)
	if err != nil {
		return fmt.Errorf("garbage collection failed: %w", err)
	}

	fmt.Println("=== Workstation Garbage Collection ===")
	if *dryRun {
		fmt.Println("[DRY-RUN MODE: No files modified]")
	}
	fmt.Printf("Pruned Worktrees:          %d\n", len(report.PrunedWorktrees))
	fmt.Printf("Purged Ephemeral Files:    %d\n", len(report.PurgedEphemeralFiles))
	fmt.Printf("Cleaned Cache Artifacts:   %d\n", len(report.CleanedCacheArtifacts))
	fmt.Printf("Total Bytes Reclaimed:     %.2f MB\n", float64(report.ReclaimedBytes)/(1024*1024))

	return nil
}
