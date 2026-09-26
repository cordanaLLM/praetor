package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"strings"

	"github.com/cordanaLLM/praetor/internal/funding"
)

// runDocsFunding renders the funding surfaces (.github/FUNDING.yml, the README badge and
// support blocks, the MkDocs announcement and social blocks) from the operator's funding
// document. Without the document it renders nothing, so no unconfigured account is linked.
func runDocsFunding(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("docs funding", flag.ContinueOnError)
	configPath := fs.String("config", funding.ConfigFile, "Funding document, relative to the repository path")
	check := fs.Bool("check", false, "Report drift without writing; exit non-zero when a surface differs")
	positional, err := parseInterspersed(fs, args)
	if err != nil {
		return err
	}
	if len(positional) > 1 {
		return fmt.Errorf("docs funding accepts at most one repository path, got %q", positional)
	}
	repoPath := positionalAt(positional, 0, ".")

	cfg, err := funding.Load(repoPath, *configPath)
	switch {
	case errors.Is(err, funding.ErrNotConfigured):
		fmt.Printf("Funding: not configured (%s absent); rendering no funding links\n", *configPath)
	case err != nil:
		return err
	}
	result, err := funding.Apply(ctx, repoPath, cfg, !*check)
	if err != nil {
		return err
	}
	printFundingResult(result, *check)
	if *check && len(result.Drifted) > 0 {
		return fmt.Errorf("funding surfaces differ from %s: %s", *configPath, strings.Join(result.Drifted, ", "))
	}
	return nil
}

func printFundingResult(result funding.Result, check bool) {
	verb := "rendered"
	if check {
		verb = "drifted"
	}
	for _, surface := range result.Drifted {
		fmt.Printf("  %s: %s\n", verb, surface)
	}
	for _, surface := range result.Skipped {
		fmt.Printf("  skipped (no file or markers): %s\n", surface)
	}
	if len(result.Drifted) == 0 {
		fmt.Println("  every funding surface matches the configuration")
	}
}
