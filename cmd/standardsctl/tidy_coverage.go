// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package main

import (
	"context"
	"flag"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/cordanaLLM/praetor/internal/config"
	"github.com/cordanaLLM/praetor/internal/tidycoverage"
)

// clangTidyLinter is the linter name a profile's linters list carries to enable the clang-tidy
// translation-unit coverage gate in the audit; native-gpu-systems declares it.
const clangTidyLinter = "clang-tidy"

// auditTidyCoverage runs the clang-tidy translation-unit coverage gate (#778) when the resolved
// policy's linters name clang-tidy or the manifest declares clang_tidy lanes, and otherwise
// prints that it did not run and why.
func auditTidyCoverage(ctx context.Context, manifest *config.Manifest, rootDir string, policy *config.ResolvedPolicy) error {
	if manifest.ClangTidy == nil && !slices.Contains(policy.Linters, clangTidyLinter) {
		fmt.Println("[SKIP] clang-tidy translation-unit coverage not checked: the resolved policy's linters do not name " +
			"clang-tidy and .standards.yaml declares no clang_tidy lanes.")
		return nil
	}
	return checkTidyCoverage(ctx, manifest, rootDir, time.Now())
}

// runCITidyCoverage is `praetorctl ci tidy-coverage`: the gate on its own, for the CI job that
// has just written the lanes' compile databases. It runs whatever the profile declares.
func runCITidyCoverage(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("ci tidy-coverage", flag.ContinueOnError)
	dir := fs.String("dir", ".", "Repository root: the top of its git work tree, holding .standards.yaml")
	if _, err := parseInterspersed(fs, args); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		return fmt.Errorf("ci tidy-coverage accepts no positional arguments, got %q", fs.Args())
	}
	manifest, err := config.LoadManifest(filepath.Join(*dir, config.ManifestFileName))
	if err != nil {
		return err
	}
	return checkTidyCoverage(ctx, manifest, *dir, time.Now())
}

// checkTidyCoverage runs the gate over the repository at rootDir and prints its verdict: a
// skip with its reason, a pass with what each lane read, or every finding before the failure.
// A gate that cannot run, such as one whose lane input is missing, fails.
func checkTidyCoverage(ctx context.Context, manifest *config.Manifest, rootDir string, today time.Time) error {
	report, err := tidycoverage.Check(ctx, tidycoverage.Options{
		Root: rootDir, Policy: manifest.ClangTidy, Exceptions: manifest.Exceptions, Today: today,
	})
	if err != nil {
		return fmt.Errorf("[FAIL] clang-tidy translation-unit coverage could not run: %w", err)
	}
	if report.Skipped != "" {
		fmt.Printf("[SKIP] clang-tidy translation-unit coverage not checked: %s.\n", report.Skipped)
		return nil
	}
	if !report.Passed() {
		for _, finding := range report.Findings {
			fmt.Printf("  - %s\n", finding)
		}
		return fmt.Errorf("[FAIL] clang-tidy translation-unit coverage: %d problem(s) over %d tracked translation units%s",
			len(report.Findings), report.Units, tidyCoverageHint(manifest))
	}
	fmt.Printf("[PASS] clang-tidy translation-unit coverage: all %d tracked translation units read by a lane (%s) or excused by a live exception (%d).\n",
		report.Units, laneSummary(report.Lanes), len(report.Excepted))
	return nil
}

// tidyCoverageHint names the declaration a failing repository is missing, if any.
func tidyCoverageHint(manifest *config.Manifest) string {
	if manifest.ClangTidy == nil {
		return "; .standards.yaml declares no clang_tidy lanes, so no file counts as read"
	}
	return "; add each file to a lane, or name it in the exceptions list with rule " + tidycoverage.Rule
}

// laneSummary lists what each lane read, as "name: count".
func laneSummary(lanes []tidycoverage.Lane) string {
	if len(lanes) == 0 {
		return "no lane declared"
	}
	parts := make([]string, 0, len(lanes))
	for _, lane := range lanes {
		parts = append(parts, fmt.Sprintf("%s: %d", lane.Name, lane.Units))
	}
	return strings.Join(parts, ", ")
}
