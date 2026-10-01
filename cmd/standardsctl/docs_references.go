// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"time"

	"github.com/cordanaLLM/praetor/internal/cifilter"
	"github.com/cordanaLLM/praetor/internal/config"
	"github.com/cordanaLLM/praetor/internal/contextopt"
	"github.com/cordanaLLM/praetor/internal/docsref"
)

// docsReferencesTimeout bounds one documentation reference check (HISS-02).
const docsReferencesTimeout = 2 * time.Minute

// docsReferencesPackage is where this binary's source lives inside a Praetor checkout.
const docsReferencesPackage = "cmd/standardsctl"

// maxReportedFindings bounds the findings printed by one run (HISS-02).
const maxReportedFindings = 512

// docsReferencesRequest is one parsed `docs references` call.
type docsReferencesRequest struct {
	path     string
	base     string
	head     string
	bodyFile string
}

// runDocsReferences checks the checkout's documentation. In a Praetor source checkout it
// checks that README.md and docs/ name only commands, flags and repository paths that exist
// (BUG-992), reading this binary's own dispatch table for the command list and the checkout's
// source for each command's subcommands and flags. In any checkout that declares docs_surfaces
// in .standards.yaml it checks that every declared glob selects a file and, with --base, that a
// change to a surface edits its documentation or carries a waiver (#608).
func runDocsReferences(ctx context.Context, args []string) error {
	req, err := parseDocsReferences(args)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, docsReferencesTimeout)
	defer cancel()
	surfaces, err := declaredDocsSurfaces(req.path)
	if err != nil {
		return err
	}
	praetor := isPraetorCheckout(req.path)
	if !praetor && len(surfaces) == 0 {
		return fmt.Errorf("docs references checks a Praetor source checkout or a repository that declares docs_surfaces: "+
			"%s has no %s directory and no docs_surfaces in %s (docs/guides/documentation-drift.md)",
			req.path, docsReferencesPackage, config.ManifestFileName)
	}
	if req.base != "" && len(surfaces) == 0 {
		return fmt.Errorf("docs references --base checks the declared docs_surfaces, and %s declares none", config.ManifestFileName)
	}
	var failures []error
	if praetor {
		failures = append(failures, checkCLIReferences(ctx, req.path))
	} else {
		fmt.Printf("=== Documentation References: %s ===\n", req.path)
		fmt.Printf("  skipped command, flag and path references: not a Praetor source checkout (no %s directory)\n", docsReferencesPackage)
	}
	if len(surfaces) > 0 {
		failures = append(failures, checkDocsSurfaces(ctx, req, surfaces))
	}
	return errors.Join(failures...)
}

// parseDocsReferences parses the flags; --head and --pr-body-file belong to --base.
func parseDocsReferences(args []string) (docsReferencesRequest, error) {
	flags := flag.NewFlagSet("docs references", flag.ContinueOnError)
	var req docsReferencesRequest
	flags.StringVar(&req.path, "path", ".", "Top of the checkout whose documentation is checked")
	flags.StringVar(&req.base, "base", "", "Base revision: check that each declared surface changed in base..head edits its documentation")
	flags.StringVar(&req.head, "head", "", "Head revision of the change --base checks (default HEAD)")
	flags.StringVar(&req.bodyFile, "pr-body-file", "", "Pull request body whose 'no docs needed: <reason>' line waives the --base findings")
	if _, err := parseInterspersed(flags, args); err != nil {
		return req, err
	}
	if flags.NArg() > 0 {
		return req, fmt.Errorf("docs references accepts no positional arguments, got %q", flags.Args())
	}
	if req.base == "" && (req.head != "" || req.bodyFile != "") {
		return req, errors.New("docs references: --head and --pr-body-file need --base")
	}
	if req.head == "" {
		req.head = "HEAD"
	}
	return req, nil
}

// isPraetorCheckout reports whether repoPath holds this binary's source package.
func isPraetorCheckout(repoPath string) bool {
	info, err := os.Stat(filepath.Join(repoPath, filepath.FromSlash(docsReferencesPackage)))
	return err == nil && info.IsDir()
}

// declaredDocsSurfaces returns the docs_surfaces of the checkout's manifest; a checkout
// without a manifest declares none.
func declaredDocsSurfaces(repoPath string) ([]config.DocsSurface, error) {
	manifest, err := config.LoadManifest(filepath.Join(repoPath, config.ManifestFileName))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return manifest.DocsSurfaces, nil
}

// checkCLIReferences runs the command, flag and path reference check of a Praetor checkout.
func checkCLIReferences(ctx context.Context, repoPath string) error {
	commands, err := commandHandlerNames()
	if err != nil {
		return err
	}
	report, err := docsref.Run(ctx, docsref.Options{Root: repoPath, Package: docsReferencesPackage, Commands: commands})
	if err != nil {
		return err
	}
	return printDocsReferences(repoPath, report)
}

// printDocsReferences prints the counts, every skipped document and suppressed block with
// its reason, every flag on an Accepted decision record, and each finding; it fails when any
// finding exists. A flag never fails it.
func printDocsReferences(repoPath string, report *docsref.Report) error {
	fmt.Printf("=== Documentation References: %s ===\n", repoPath)
	fmt.Printf("  Documents checked:  %d\n", report.Documents)
	fmt.Printf("  Decision records:   %d (Accepted; repository paths only)\n", report.Records)
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
	printRecordFlags(report.Flagged)
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

// printRecordFlags prints what an Accepted decision record names that does not resolve. The
// body is immutable (docs/adr/README.md, rule 4), so the remedy is a superseding record and
// the flags never fail the check.
func printRecordFlags(flagged []docsref.Finding) {
	if len(flagged) == 0 {
		return
	}
	fmt.Printf("  [FLAG] %d reference(s) in Accepted decision records do not resolve:\n", len(flagged))
	for index := 0; index < len(flagged) && index < maxReportedFindings; index++ {
		fmt.Printf("    %s\n", flagged[index])
	}
	fmt.Println("  An Accepted record's body is not edited: a record that supersedes it states what changed.\n" +
		"  These flags do not fail the check.")
}

// checkDocsSurfaces checks that every declared glob selects a file and, with --base, the
// change base..head against the surfaces.
func checkDocsSurfaces(ctx context.Context, req docsReferencesRequest, surfaces []config.DocsSurface) error {
	problems, err := docsref.CheckSurfaces(ctx, req.path, surfaces)
	if err != nil {
		return err
	}
	fmt.Printf("=== Documentation Surfaces: %d declared in %s ===\n", len(surfaces), config.ManifestFileName)
	var failures []error
	for index := 0; index < len(problems) && index < maxReportedFindings; index++ {
		fmt.Fprintf(os.Stderr, "  %s\n", problems[index])
	}
	if len(problems) > 0 {
		failures = append(failures, fmt.Errorf("[FAIL] %d docs_surfaces glob(s) in %s select nothing", len(problems), config.ManifestFileName))
	} else {
		fmt.Println("[PASS] every declared surface and document exists.")
	}
	if req.base == "" {
		fmt.Println("  change not checked: pass --base=<rev> to check that a change to a surface edits its documentation")
		return errors.Join(failures...)
	}
	return errors.Join(append(failures, checkDocsDrift(ctx, req, surfaces))...)
}

// checkDocsDrift checks the change base..head against the declared surfaces.
func checkDocsDrift(ctx context.Context, req docsReferencesRequest, surfaces []config.DocsSurface) error {
	messages, err := commitRange(ctx, req.path, req.base, req.head)
	if err != nil {
		return err
	}
	changed, err := cifilter.GetChangedFiles(ctx, req.path, req.base, req.head)
	if err != nil {
		return err
	}
	waivers, err := driftWaivers(ctx, req.bodyFile, messages)
	if err != nil {
		return err
	}
	report, err := docsref.Drift(changed, surfaces, waivers)
	if err != nil {
		return err
	}
	return printDocsDrift(req, report)
}

// driftWaivers collects the Docs-Waiver: trailers of the range's commits and the waiver of
// the pull request body, when one is given.
func driftWaivers(ctx context.Context, bodyFile string, messages []string) ([]docsref.Waiver, error) {
	var waivers []docsref.Waiver
	for index := 0; index < len(messages) && index <= maxAnalyzedCommits; index++ {
		sha, message, _ := strings.Cut(messages[index], commitFieldSep)
		waivers = append(waivers, docsref.CommitWaivers(shortSHA(sha), message)...)
	}
	if bodyFile == "" {
		return waivers, nil
	}
	body, err := contextopt.ReadSnapshot(ctx, bodyFile)
	if err != nil {
		return nil, fmt.Errorf("read the pull request body: %w", err)
	}
	return append(waivers, docsref.BodyWaivers(string(body))...), nil
}

// printDocsDrift prints the counts, every documented surface, waiver and waived finding, and
// each finding; it fails when any finding exists.
func printDocsDrift(req docsReferencesRequest, report *docsref.DriftReport) error {
	fmt.Printf("=== Documentation Drift: %s..%s ===\n", req.base, req.head)
	fmt.Printf("  Paths changed:      %d\n", report.Paths)
	fmt.Printf("  Surfaces changed:   %d of %d\n", len(report.Documented)+len(report.Waived)+len(report.Findings), report.Surfaces)
	for _, documented := range report.Documented {
		fmt.Printf("  documented %s\n", documented)
	}
	for _, waiver := range report.Waivers {
		fmt.Printf("  waiver %s\n", waiver)
	}
	for _, waived := range report.Waived {
		fmt.Printf("  waived %s\n", waived)
	}
	if len(report.Findings) == 0 {
		fmt.Println("[PASS] every changed surface carries its documentation edit or a waiver.")
		return nil
	}
	for index := 0; index < len(report.Findings) && index < maxReportedFindings; index++ {
		fmt.Fprintf(os.Stderr, "  %s\n", report.Findings[index])
	}
	fmt.Fprintln(os.Stderr, "\nEdit the mapped documentation in the same change, or state why none is needed with a\n"+
		"'Docs-Waiver: <reason>' commit trailer or a 'no docs needed: <reason>' line in the pull request\n"+
		"body. A decision record under docs/adr/ does not count. Map an unmapped surface by listing its\n"+
		"documents under docs in its docs_surfaces entry of "+config.ManifestFileName+".")
	return fmt.Errorf("[FAIL] %d changed surface(s) without their documentation", len(report.Findings))
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
