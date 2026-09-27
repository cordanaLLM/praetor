// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"time"

	"github.com/cordanaLLM/praetor/internal/docsref"
)

// docsReferencesTimeout bounds one documentation reference check (HISS-02).
const docsReferencesTimeout = 2 * time.Minute

// docsReferencesPackage is where this binary's source lives inside a Praetor checkout.
const docsReferencesPackage = "cmd/standardsctl"

// maxReportedFindings bounds the findings printed by one run (HISS-02).
const maxReportedFindings = 512

// runDocsReferences checks that the checkout's README.md and docs/ name only commands, flags
// and repository paths that exist (BUG-992). It reads this binary's own dispatch table for
// the command list and the checkout's source for each command's subcommands and flags, so it
// runs against a Praetor source checkout only.
func runDocsReferences(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("docs references", flag.ContinueOnError)
	repoPath := fs.String("path", ".", "Top of the Praetor source checkout whose documentation is checked")
	if _, err := parseInterspersed(fs, args); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		return fmt.Errorf("docs references accepts no positional arguments, got %q", fs.Args())
	}
	if info, err := os.Stat(filepath.Join(*repoPath, filepath.FromSlash(docsReferencesPackage))); err != nil || !info.IsDir() {
		return fmt.Errorf("docs references checks a Praetor source checkout: %s has no %s directory", *repoPath, docsReferencesPackage)
	}
	commands, err := commandHandlerNames()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, docsReferencesTimeout)
	defer cancel()
	report, err := docsref.Run(ctx, docsref.Options{Root: *repoPath, Package: docsReferencesPackage, Commands: commands})
	if err != nil {
		return err
	}
	return printDocsReferences(*repoPath, report)
}

// printDocsReferences prints the counts, every skipped document and suppressed block with
// its reason, and each finding; it fails when any finding exists.
func printDocsReferences(repoPath string, report *docsref.Report) error {
	fmt.Printf("=== Documentation References: %s ===\n", repoPath)
	fmt.Printf("  Documents checked:  %d\n", report.Documents)
	fmt.Printf("  Documents skipped:  %d\n", len(report.Skipped))
	fmt.Printf("  Suppressed blocks:  %d\n", len(report.Suppressions))
	fmt.Printf("  CLI invocations:    %d\n", report.Invocations)
	fmt.Printf("  Repository paths:   %d\n", report.Paths)
	for _, skipped := range report.Skipped {
		fmt.Printf("  skipped %s\n", skipped)
	}
	for _, suppressed := range report.Suppressions {
		fmt.Printf("  suppressed %s\n", suppressed)
	}
	if len(report.Findings) == 0 {
		fmt.Println("[PASS] every documented command, flag and repository path resolves.")
		return nil
	}
	for index := 0; index < len(report.Findings) && index < maxReportedFindings; index++ {
		fmt.Fprintf(os.Stderr, "  %s\n", report.Findings[index])
	}
	fmt.Fprintln(os.Stderr, "\nPoint each reference at a command, flag or path that exists. A path the public source\n"+
		"never has (another repository's file, an adopter's generated file, an illustrative example)\n"+
		"goes between <!-- praetor:docs-references:off <reason> --> and <!-- praetor:docs-references:on -->.")
	return fmt.Errorf("[FAIL] %d documentation reference(s) do not resolve", len(report.Findings))
}

// commandHandlerNames maps every top-level command name to the name of the function that
// handles it, read from the live dispatch table rather than from a second list.
func commandHandlerNames() (map[string]string, error) {
	table := commandTable()
	names := make(map[string]string, len(table))
	for command, handler := range table {
		function := runtime.FuncForPC(reflect.ValueOf(handler).Pointer())
		if function == nil {
			return nil, fmt.Errorf("docs references: cannot name the handler of command %s", command)
		}
		full := function.Name()
		names[command] = full[strings.LastIndex(full, ".")+1:]
	}
	return names, nil
}
