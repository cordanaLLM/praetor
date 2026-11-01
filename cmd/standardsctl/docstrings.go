// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package main

import (
	"context"
	"flag"
	"fmt"
	"io"
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
	fmt.Println("  check [--path=.] [--base=<ref>] [--head=<ref>] [--pre-push] [--threshold=80]  Check diff-scoped docstring coverage (modes: error, warning, off)")
}

// runDocstringsCheck executes `standardsctl docstrings check`.
func runDocstringsCheck(args []string) error {
	fs := flag.NewFlagSet("docstrings check", flag.ContinueOnError)
	repoPath := fs.String("path", ".", "Repository root directory")
	baseRef := fs.String("base", "", "Base git reference compared against (defaults to origin/<default-branch>)")
	headRef := fs.String("head", "", "Optional head git reference (defaults to working tree against merge base)")
	prePush := fs.Bool("pre-push", false, "Read pushed ref updates from git pre-push stdin")
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

	if *prePush {
		if *baseRef != "" || *headRef != "" {
			fmt.Fprintln(os.Stderr, "[ERROR] --pre-push cannot be combined with --base or --head")
			return exitStatusError{code: 2}
		}
		return runDocstringsPrePush(ctx, *repoPath, override, os.Stdin)
	}
	return runDocstringsRange(ctx, *repoPath, *baseRef, *headRef, override)
}

// runDocstringsRange executes a single diff-scoped docstring coverage check.
func runDocstringsRange(ctx context.Context, repoPath, baseRef, headRef string, override *int) error {
	rep, err := docstrings.Check(ctx, docstrings.Options{
		RepoDir:           repoPath,
		BaseRef:           baseRef,
		HeadRef:           headRef,
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

// runDocstringsPrePush reads git pre-push updates from r and checks each pushed range.
func runDocstringsPrePush(ctx context.Context, repoPath string, override *int, r io.Reader) error {
	updates, err := docstrings.ParsePrePushStdin(ctx, r)
	if err != nil {
		fmt.Fprintf(os.Stderr, "[ERROR] %v\n", err)
		return exitStatusError{code: 2}
	}
	if len(updates) == 0 {
		// lefthook hands stdin to one use_stdin job only; an empty input is stated, never a silent pass.
		fmt.Println("Docstring check: skipped, no pushed ref update on stdin (a delete-only push, or another hook job read the input)")
		return nil
	}
	seen := make(map[string]bool, len(updates))
	failed := false
	for i := 0; i < len(updates); i++ {
		u := updates[i]
		base, skipReason := docstrings.ResolvePrePushBase(ctx, repoPath, "origin", u.LocalOID, u.RemoteOID)
		if base == "" {
			fmt.Printf("Docstring check: skipped, no pushed-range base (%s); CI checks pull request range\n", skipReason)
			continue
		}
		key := base + ".." + u.LocalOID
		if seen[key] {
			continue
		}
		seen[key] = true
		rep, err := docstrings.Check(ctx, docstrings.Options{
			RepoDir:           repoPath,
			BaseRef:           base,
			HeadRef:           u.LocalOID,
			ThresholdOverride: override,
		})
		if err != nil {
			fmt.Fprintf(os.Stderr, "[ERROR] %v\n", err)
			return exitStatusError{code: 2}
		}
		printDocstringsReport(rep)
		if !rep.Passed {
			failed = true
		}
	}
	if failed {
		return exitStatusError{code: 1}
	}
	return nil
}

// printDocstringsReport writes the coverage summary and each uncovered function to stdout.
func printDocstringsReport(rep *docstrings.Report) {
	suffix := ""
	if rep.Mode != "" && rep.Mode != "error" {
		suffix = fmt.Sprintf(" (mode: %s)", rep.Mode)
	}
	fmt.Printf("Docstring coverage: %.2f%% (%d/%d covered, counted: %d, covered: %d, threshold: %d%%)%s\n",
		rep.Coverage, rep.Covered, rep.Counted, rep.Counted, rep.Covered, rep.Threshold, suffix)
	for i := 0; i < len(rep.Uncovered); i++ {
		u := rep.Uncovered[i]
		fmt.Printf("%s:%d %s\n", u.Path, u.Line, u.Name)
	}
}
