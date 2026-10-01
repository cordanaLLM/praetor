// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/cordanaLLM/praetor/internal/harvester"
)

// driftDigestWidth is how many characters of a variant digest the text report shows
// ("sha256:" and twelve hex digits); the JSON report carries the whole digest.
const driftDigestWidth = len("sha256:") + 12

// runHarvestDrift runs the read-only fleet drift survey (harvester.SurveyDrift) over the
// repositories the workstation inventory finds under the dev root. Drift is reported, never
// failed on: enforcement is a later unit (docs/plans/fleet-drift-and-schema-conformance.md).
// An incomplete survey or inventory returns nonzero with the report still printed, as
// harvest workstation does, so a partial comparison is never read as a complete one.
func runHarvestDrift(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("harvest drift", flag.ContinueOnError)
	dirFlag := fs.String("dir", "", "Path to development directory "+devRootUsageDefault)
	jsonOutput := fs.Bool("json", false, "Emit the complete read-only drift report as JSON")
	var paths repeatedStringFlag
	fs.Var(&paths, "path", "Repository-relative path prefix to compare (repeatable; default: "+
		strings.Join(harvester.DefaultDriftPaths(), ", ")+")")
	if _, err := parseInterspersed(fs, args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return errors.New("harvest drift accepts no positional arguments")
	}
	if _, err := harvester.NormalizeDriftPaths(paths); err != nil {
		return fmt.Errorf("harvest drift: %w", err)
	}
	devDir, err := resolveDevRootDir(*dirFlag, "--dir")
	if err != nil {
		return fmt.Errorf("harvest drift: %w", err)
	}
	inventory, scanErr := harvester.ScanLocalWorkstation(ctx, devDir)
	if inventory == nil {
		return fmt.Errorf("harvest drift: scan workstation: %w", scanErr)
	}
	report, surveyErr := harvester.SurveyDrift(ctx, inventory, harvester.DriftOptions{Root: devDir, Paths: paths})
	if report == nil {
		return fmt.Errorf("harvest drift: %w", errors.Join(scanErr, surveyErr))
	}
	if *jsonOutput {
		if err := json.NewEncoder(os.Stdout).Encode(report); err != nil {
			return fmt.Errorf("encode drift report: %w", err)
		}
	} else {
		printDriftReport(report)
	}
	return errors.Join(scanErr, surveyErr, driftSurveyError(report))
}

// driftSurveyError is the nonzero result for a survey or inventory that did not cover
// everything it was asked to; drift itself is never an error.
func driftSurveyError(report *harvester.DriftReport) error {
	if !report.Complete {
		return errors.New("drift survey is incomplete; inspect the report errors")
	}
	if !report.InventoryComplete {
		return errors.New("workstation inventory is incomplete; run harvest workstation --json for its errors")
	}
	return nil
}

// printDriftReport renders the text survey: the paths compared, the repositories by state,
// each drifted path with its variants, each identical path with its repositories, and the
// completeness line with every recorded error.
func printDriftReport(report *harvester.DriftReport) {
	fmt.Println("=== Fleet Drift Survey ===")
	fmt.Printf("Paths: %s\n", strings.Join(report.Paths, ", "))
	states := map[string]int{}
	for _, repository := range report.Repositories {
		states[repository.State]++
	}
	fmt.Printf("Repositories: %d (surveyed %d, unborn %d, unreadable %d)\n", len(report.Repositories),
		states[harvester.DriftRepositorySurveyed], states[harvester.DriftRepositoryUnborn], states[harvester.DriftRepositoryUnreadable])
	fmt.Printf("Drifted copies (%d):\n", len(report.Drifted))
	for _, file := range report.Drifted {
		fmt.Printf("  - %s (%d variants)\n", file.Path, len(file.Variants))
		for i, variant := range file.Variants {
			delta := ""
			if i > 0 {
				delta = fmt.Sprintf(" -%d +%d", variant.LinesRemoved, variant.LinesAdded)
			}
			fmt.Printf("      %s lines=%d%s in %s\n", shortDriftDigest(variant.Digest), variant.Lines, delta,
				strings.Join(variant.Repositories, ", "))
		}
	}
	fmt.Printf("Identical copies (%d):\n", len(report.Identical))
	for _, file := range report.Identical {
		fmt.Printf("  - %s in %s\n", file.Path, strings.Join(file.Variants[0].Repositories, ", "))
	}
	fmt.Printf("Survey complete: %t (truncated: %t); repository inventory complete: %t\n",
		report.Complete, report.Truncated, report.InventoryComplete)
	for _, message := range report.Errors {
		fmt.Printf("  error: %s\n", message)
	}
}

// shortDriftDigest shortens a digest to driftDigestWidth characters for the text report.
func shortDriftDigest(digest string) string {
	if len(digest) <= driftDigestWidth {
		return digest
	}
	return digest[:driftDigestWidth]
}
