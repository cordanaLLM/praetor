package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"strings"
	"time"

	"github.com/cordanaLLM/praetor/internal/gc"
)

type repeatedStringFlag []string

func (f *repeatedStringFlag) String() string { return strings.Join(*f, ",") }

func (f *repeatedStringFlag) Set(value string) error {
	if strings.TrimSpace(value) == "" {
		return fmt.Errorf("released path cannot be empty")
	}
	*f = append(*f, value)
	return nil
}

func runGC(args []string) error {
	fs := flag.NewFlagSet("gc", flag.ContinueOnError)
	// Deleting worktrees is irreversible, so the simulation is the default: the caller has
	// to ask for the deletion explicitly with --dry-run=false.
	dryRun := fs.Bool("dry-run", true, "Simulate garbage collection; pass --dry-run=false to delete")
	rootDir := fs.String("path", ".", "Root repository directory")
	worktreesDir := fs.String("worktrees-dir", "", "Worktree pool within the repository root")
	maxAge := fs.Duration("max-age", 24*time.Hour, "Minimum retention age for released worktrees")
	artifactMaxAge := fs.Duration("artifact-max-age", 24*time.Hour, "Minimum retention age for released artifacts")
	cleanCache := fs.Bool("clean-go-test-cache", false, "Explicitly clear the shared Go test cache on apply")
	jsonOutput := fs.Bool("json", false, "Print the complete report as JSON")
	var released repeatedStringFlag
	fs.Var(&released, "released-path", "Explicitly assert that a path is quiescent and released (repeatable)")

	if err := fs.Parse(reorderArgs(args, boolFlagNames(fs))); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("gc takes no positional arguments, got %q", fs.Arg(0))
	}
	if *maxAge <= 0 {
		return fmt.Errorf("max-age must be positive")
	}
	if *artifactMaxAge <= 0 {
		return fmt.Errorf("artifact-max-age must be positive")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	opts := gc.Options{
		RootDir:          *rootDir,
		WorktreesDir:     *worktreesDir,
		MaxWorktreeAge:   *maxAge,
		MaxArtifactAge:   *artifactMaxAge,
		ReleasedPaths:    append([]string(nil), released...),
		DryRun:           *dryRun,
		SkipGitPrune:     true,
		CleanGoTestCache: *cleanCache,
	}

	report, err := gc.Collect(ctx, opts)
	printGCReport(report, *jsonOutput, err)
	if err != nil {
		return fmt.Errorf("garbage collection failed: %w", err)
	}
	return nil
}

func printGCReport(report *gc.GCReport, asJSON bool, collectErr error) {
	if asJSON {
		data, marshalErr := json.Marshal(report)
		if marshalErr != nil {
			fmt.Printf("{\"error\":%q}\n", marshalErr.Error())
			return
		}
		fmt.Println(string(data))
		return
	}
	if report == nil {
		if collectErr != nil {
			fmt.Printf("Garbage collection failed: %v\n", collectErr)
		}
		return
	}
	fmt.Println("=== Workstation Garbage Collection ===")
	if report.DryRun {
		fmt.Println("[DRY-RUN MODE: No files modified. Pass --dry-run=false to delete.]")
	}
	fmt.Printf("Complete:                  %t\n", report.Complete)
	fmt.Printf("Planned Worktrees:        %d\n", len(report.PlannedWorktrees))
	fmt.Printf("Planned Artifacts:        %d\n", len(report.PlannedArtifacts))
	fmt.Printf("Planned Bytes Reclaimed:  %.2f MB\n", float64(report.PlannedReclaimedBytes)/(1024*1024))
	fmt.Printf("Planned Cache Cleanup:    %t\n", report.PlannedCacheCleanup)
	printGCPaths("  worktree", report.PlannedWorktrees)
	printGCPaths("  artifact", report.PlannedArtifacts)
	fmt.Printf("Pruned Worktrees:         %d\n", len(report.PrunedWorktrees))
	fmt.Printf("Purged Ephemeral Files:   %d\n", len(report.PurgedEphemeralFiles))
	fmt.Printf("Cleaned Cache Artifacts:  %d\n", len(report.CleanedCacheArtifacts))
	fmt.Printf("Actual Bytes Reclaimed:   %.2f MB\n", float64(report.ReclaimedBytes)/(1024*1024))
	if len(report.SkippedWorktrees)+len(report.SkippedArtifacts) > 0 {
		fmt.Printf("Protected Worktrees:       %d\n", len(report.SkippedWorktrees))
		fmt.Printf("Skipped Artifacts:         %d\n", len(report.SkippedArtifacts))
		printGCPaths("  protected worktree", report.SkippedWorktrees)
		printGCPaths("  protected artifact", report.SkippedArtifacts)
	}
	if len(report.Errors) > 0 {
		fmt.Printf("Errors (%d):\n", len(report.Errors))
		for _, item := range report.Errors {
			fmt.Printf("  - %s\n", item)
		}
	}
}

func printGCPaths(label string, paths []string) {
	for _, path := range paths {
		fmt.Printf("%s: %s\n", label, path)
	}
}
