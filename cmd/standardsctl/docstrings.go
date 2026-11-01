// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/cordanaLLM/praetor/internal/docstrings"
)

const docstringsCommandTimeout = 30 * time.Second

// runDocstrings dispatches subcommands of `standardsctl docstrings`.
func runDocstrings(args []string) error {
	if len(args) < 1 {
		printDocstringsUsage()
		return nil
	}
	sub := args[0]
	subArgs := args[1:]
	switch sub {
	case "-h", "--help", "help":
		printDocstringsUsage()
		return nil
	case "check":
		return runDocstringsCheck(subArgs)
	default:
		fmt.Fprintf(os.Stderr, "[ERROR] unknown docstrings subcommand: %s\n", sub)
		return exitStatusError{code: 2}
	}
}

// printDocstringsUsage prints help text for the docstrings command.
func printDocstringsUsage() {
	fmt.Println("Usage: standardsctl docstrings <subcommand> [arguments]")
	fmt.Println("\nSubcommands:")
	fmt.Println("  check [--path=.] [--base=origin/main] [--head=<ref>] [--threshold=80]  Check diff-scoped docstring coverage")
}

// runDocstringsCheck executes `standardsctl docstrings check`.
func runDocstringsCheck(args []string) error {
	fs := flag.NewFlagSet("docstrings check", flag.ContinueOnError)
	repoPath := fs.String("path", ".", "Repository root directory")
	baseRef := fs.String("base", "origin/main", "Base git reference compared against")
	headRef := fs.String("head", "", "Optional head git reference (defaults to working tree against merge base)")
	threshold := fs.Int("threshold", docstrings.DefaultThreshold, "Minimum docstring coverage percentage (overrides .coderabbit.yaml)")

	rest, err := parseInterspersed(fs, args)
	if err != nil {
		fmt.Fprintf(os.Stderr, "[ERROR] %v\n", err)
		return exitStatusError{code: 2}
	}
	if len(rest) > 0 {
		fmt.Fprintf(os.Stderr, "[ERROR] docstrings check accepts no positional arguments, got %q\n", rest)
		return exitStatusError{code: 2}
	}

	var override *int
	fs.Visit(func(f *flag.Flag) {
		if f.Name == "threshold" {
			val := *threshold
			override = &val
		}
	})

	ctx, cancel := context.WithTimeout(context.Background(), docstringsCommandTimeout)
	defer cancel()

	rep, err := docstrings.Check(ctx, docstrings.Options{
		RepoDir:           *repoPath,
		BaseRef:           *baseRef,
		HeadRef:           *headRef,
		ThresholdOverride: override,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "[ERROR] %v\n", err)
		return exitStatusError{code: 2}
	}

	printDocstringsReport(rep)
	if !rep.Passed {
		return exitStatusError{code: 1}
	}
	return nil
}

// printDocstringsReport writes the coverage summary and each uncovered function to stdout.
func printDocstringsReport(rep *docstrings.Report) {
	fmt.Printf("Docstring coverage: %.2f%% (%d/%d covered, counted: %d, covered: %d, threshold: %d%%)\n",
		rep.Coverage, rep.Covered, rep.Counted, rep.Counted, rep.Covered, rep.Threshold)
	for i := 0; i < len(rep.Uncovered); i++ {
		u := rep.Uncovered[i]
		fmt.Printf("%s:%d %s\n", u.Path, u.Line, u.Name)
	}
}
