package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/cordanaLLM/praetor/internal/paperclip"
)

func runPaperclip(args []string) error {
	if len(args) < 1 {
		printPaperclipUsage()
		return nil
	}

	sub := args[0]
	subArgs := args[1:]
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	switch sub {
	case "-h", "--help", "help":
		printPaperclipUsage()
		return nil
	case "harness":
		return runPaperclipHarness(ctx, subArgs)
	case "disposition":
		return runPaperclipDisposition(ctx, subArgs)
	case "verify":
		return runPaperclipVerify(ctx, subArgs)
	default:
		return fmt.Errorf("unknown paperclip subcommand: %s", sub)
	}
}

func printPaperclipUsage() {
	fmt.Println("Usage: standardsctl paperclip <subcommand> [arguments]")
	fmt.Println("\nSubcommands:")
	fmt.Println("  harness [--path=.]                               Synthesize Paperclip agent harness and AGit rules")
	fmt.Println("  disposition --issue=<id> --status=<status> ...   Emit Rule 0 structured terminal disposition record")
	fmt.Println("  verify [--path=.] [--disposition=path]           Verify run satisfies Rule 0 and contract invariants")
}

func runPaperclipHarness(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("paperclip harness", flag.ContinueOnError)
	path := fs.String("path", ".", "Target repository path")
	if err := fs.Parse(args); err != nil {
		return err
	}

	h, err := paperclip.SynthesizeHarness(ctx, *path)
	if err != nil {
		return fmt.Errorf("synthesize harness: %w", err)
	}

	if err := paperclip.WriteHarness(h, *path); err != nil {
		return fmt.Errorf("write harness: %w", err)
	}

	fmt.Printf("[OK] Synthesized Paperclip harness in %s/.paperclip/:\n", *path)
	fmt.Println("  - .paperclip/harness.json")
	fmt.Println("  - .paperclip/rules.md")
	return nil
}

func runPaperclipDisposition(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("paperclip disposition", flag.ContinueOnError)
	issueID := fs.String("issue", "", "Issue identifier worked during the run")
	status := fs.String("status", "in_review", "Terminal disposition status (in_review or blocked)")
	note := fs.String("note", "", "Summary note explaining the terminal disposition")
	proof := fs.String("proof", "", "Evidence proof string (PR link, test run output)")
	owner := fs.String("recovery-owner", "", "Recovery owner team/user if blocked")
	actor := fs.String("actor", "praetor/agent", "Actor identity creating the disposition")
	outputPath := fs.String("output", "", "Optional path to save disposition JSON (defaults to stdout)")

	if err := fs.Parse(args); err != nil {
		return err
	}

	disp, err := paperclip.CreateDisposition(*issueID, *status, *note, *proof, *owner, *actor, nil)
	if err != nil {
		return fmt.Errorf("create disposition: %w", err)
	}

	if err := disp.Validate(ctx); err != nil {
		return fmt.Errorf("validate disposition: %w", err)
	}

	jsonBytes, err := disp.FormatJSON()
	if err != nil {
		return fmt.Errorf("format disposition: %w", err)
	}

	if *outputPath != "" {
		if err := os.WriteFile(*outputPath, jsonBytes, 0644); err != nil {
			return fmt.Errorf("write disposition to %s: %w", *outputPath, err)
		}
		fmt.Printf("[OK] Saved Paperclip Rule 0 terminal disposition to %s\n", *outputPath)
	} else {
		fmt.Print(string(jsonBytes))
	}
	return nil
}

func runPaperclipVerify(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("paperclip verify", flag.ContinueOnError)
	path := fs.String("path", ".", "Target repository path")
	dispPath := fs.String("disposition", "", "Path to disposition JSON file to verify")

	if err := fs.Parse(args); err != nil {
		return err
	}

	if *dispPath == "" {
		*dispPath = filepath.Join(*path, ".paperclip", "disposition.json")
	}

	disp, err := paperclip.ReadDisposition(*dispPath)
	if err != nil {
		return fmt.Errorf("read disposition: %w", err)
	}

	if err := paperclip.VerifyRun(ctx, *path, disp); err != nil {
		return fmt.Errorf("[FAIL] Paperclip verification failed: %w", err)
	}

	fmt.Printf("[PASS] Paperclip run satisfied Rule 0 and contract invariants for %s (Status: %s)\n", disp.IssueID, disp.Status)
	return nil
}
