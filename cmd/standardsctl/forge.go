package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/cordanaLLM/praetor/internal/forge"
)

func runForge(args []string) error {
	if len(args) < 1 {
		fmt.Println("Usage: standardsctl forge <subcommand> [arguments]")
		fmt.Println("\nSubcommands:")
		fmt.Println("  sync-wiki [--output=docs/wiki]  Generate git-backed wiki documentation suite")
		fmt.Println("  validate-pr <pr-body-file>      Verify HISS checklist and receipts in PR description")
		return nil
	}

	sub := args[0]
	subArgs := args[1:]
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	switch sub {
	case "sync-wiki":
		fs := flag.NewFlagSet("forge sync-wiki", flag.ContinueOnError)
		outputDir := fs.String("output", "docs/wiki", "Output directory for generated wiki")
		if err := fs.Parse(subArgs); err != nil {
			return err
		}

		manifest, err := forge.GenerateWiki(ctx, ".", *outputDir)
		if err != nil {
			return fmt.Errorf("failed generating wiki: %w", err)
		}

		fmt.Printf("[OK] Generated %d wiki pages in %s:\n", len(manifest.Pages), manifest.OutputDir)
		for _, p := range manifest.Pages {
			fmt.Printf("  - %s: %s\n", p.Name, p.Title)
		}
		return nil

	case "validate-pr":
		return runForgeValidatePR(subArgs)

	default:
		return fmt.Errorf("unknown forge subcommand: %s", sub)
	}
}

func runForgeValidatePR(args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("usage: standardsctl forge validate-pr <pr-body-file>")
	}
	data, err := os.ReadFile(args[0])
	if err != nil {
		return fmt.Errorf("failed to read PR body file: %w", err)
	}
	res, err := forge.ValidatePRChecklist(string(data))
	if err != nil && res == nil {
		return fmt.Errorf("PR checklist validation failed: %w", err)
	}
	fmt.Println("=== PR Checklist Validation ===")
	fmt.Printf("  HISS-16 Invariant Check: %v\n", res.HasHISS16Check)
	fmt.Printf("  3D Tests (Pos/Neg/Bound): %v\n", res.Has3DTestsCheck)
	fmt.Printf("  Ed25519 Exit-0 Receipt:  %v\n", res.HasReceipt)
	if !res.Valid {
		return fmt.Errorf("PR validation failed: %s", strings.Join(res.Errors, ", "))
	}
	fmt.Println("[PASS] PR checklist fulfills all mandatory governance invariants.")
	return nil
}
