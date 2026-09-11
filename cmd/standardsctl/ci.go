package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/cordanaLLM/praetor/internal/cifilter"
)

func runCI(args []string) error {
	if len(args) < 1 {
		printCIUsage()
		return nil
	}

	sub := args[0]
	subArgs := args[1:]
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	switch sub {
	case "-h", "--help", "help":
		printCIUsage()
		return nil
	case "filter":
		return runCIFilter(ctx, subArgs)
	default:
		return fmt.Errorf("unknown ci subcommand: %s", sub)
	}
}

func printCIUsage() {
	fmt.Println("Usage: standardsctl ci <subcommand> [arguments]")
	fmt.Println("\nSubcommands:")
	fmt.Println("  filter [--dir=.] [--base=ref] [--head=ref] [--json] [--env] [--force]  Analyze diff and filter CI gates")
}

func runCIFilter(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("ci filter", flag.ContinueOnError)
	dir := fs.String("dir", ".", "Repository root directory")
	baseRef := fs.String("base", "origin/main", "Base git reference or branch")
	headRef := fs.String("head", "HEAD", "Head git reference or commit")
	asJSON := fs.Bool("json", false, "Output decision as JSON")
	asEnv := fs.Bool("env", false, "Output decision formatted for GitHub Actions $GITHUB_OUTPUT")
	force := fs.Bool("force", false, "Force execution of all CI test and security gates")

	if err := fs.Parse(args); err != nil {
		return err
	}

	opts := cifilter.FilterOptions{
		RepoDir:  *dir,
		BaseRef:  *baseRef,
		HeadRef:  *headRef,
		ForceAll: *force,
	}

	dec, err := cifilter.AnalyzeChanges(ctx, opts)
	if err != nil {
		return fmt.Errorf("ci filter failed: %w", err)
	}

	if *asJSON {
		data, jErr := dec.ToJSON()
		if jErr != nil {
			return jErr
		}
		fmt.Println(string(data))
		return nil
	}

	if *asEnv {
		outStr := dec.FormatGitHubOutput()
		fmt.Print(outStr)
		// If running inside GitHub Actions, append to GITHUB_OUTPUT directly if set
		if ghOutput := os.Getenv("GITHUB_OUTPUT"); ghOutput != "" {
			f, fErr := os.OpenFile(ghOutput, os.O_APPEND|os.O_WRONLY, 0600)
			if fErr != nil {
				return fmt.Errorf("failed opening GITHUB_OUTPUT: %w", fErr)
			}
			defer func() {
				if cErr := f.Close(); cErr != nil {
					fmt.Fprintf(os.Stderr, "warning: error closing GITHUB_OUTPUT: %v\n", cErr)
				}
			}()
			if _, wErr := f.WriteString(outStr); wErr != nil {
				return fmt.Errorf("failed writing to GITHUB_OUTPUT: %w", wErr)
			}
		}
		return nil
	}

	printCIDecisionSummary(dec)
	return nil
}

func printCIDecisionSummary(dec *cifilter.FilterDecision) {
	fmt.Printf("=== Praetor CI Diff-Aware Change Decision ===\n")
	fmt.Printf("Reason:            %s\n", dec.Reason)
	fmt.Printf("Total Files Diff:  %d\n", dec.ChangeSet.TotalFiles)
	fmt.Printf("Run Tests:         %t\n", dec.RunTests)
	fmt.Printf("Run Linters:       %t\n", dec.RunLinters)
	fmt.Printf("Run Security:      %t\n", dec.RunSecurity)
	fmt.Printf("Run Audit:         %t\n", dec.RunAudit)
	fmt.Printf("Run Context Sync:  %t\n", dec.RunContextSync)
	fmt.Printf("Docs Only:         %t\n", dec.RunDocsOnly)
	fmt.Printf("Skip Heavy Gates:  %t\n", dec.SkipHeavyGates)
}
