// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package main

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/cordanaLLM/praetor/internal/config"
	"github.com/cordanaLLM/praetor/internal/contextopt"
	"github.com/cordanaLLM/praetor/internal/managedasset"
	"github.com/cordanaLLM/praetor/internal/supplychain"
)

// auditLicensing runs the licensing gates (docs/guides/licensing-gates.md): the REUSE.toml
// annotation order (auditReuseRecords) and the one root licence (auditRootLicense). Both run,
// and both verdicts are printed, before the first failure is returned.
func auditLicensing(ctx context.Context, manifest *config.Manifest, rootDir string, today time.Time) error {
	return errors.Join(auditReuseRecords(ctx, rootDir), auditRootLicense(ctx, manifest, rootDir, today))
}

// auditRootLicense runs supplychain.CheckRootLicense over rootDir with the manifest's exceptions
// and prints its verdict: a skip with the reason, a pass naming the licence and the kept upstream
// notices, or every finding before the failure.
func auditRootLicense(ctx context.Context, manifest *config.Manifest, rootDir string, today time.Time) error {
	report, err := supplychain.CheckRootLicense(ctx, supplychain.RootLicenseOptions{Root: rootDir, Exceptions: manifest.Exceptions, Today: today})
	if err != nil {
		return fmt.Errorf("[FAIL] root licence not checked: %w", err)
	}
	if report.Skipped != "" {
		fmt.Printf("[SKIP] root licence not checked: %s.\n", report.Skipped)
		return nil
	}
	if len(report.Findings) > 0 {
		for _, finding := range report.Findings {
			fmt.Printf("  - %s\n", finding)
		}
		return fmt.Errorf("[FAIL] root licence: %d problem(s) with the one root licence of a repository declaring %s",
			len(report.Findings), report.License)
	}
	kept := "no other root file named like a licence"
	if len(report.Kept) > 0 {
		kept = "kept upstream notices " + strings.Join(report.Kept, ", ")
	}
	fmt.Printf("[PASS] root licence: %s holds the text of %s/%s.txt; %s.\n",
		supplychain.RootLicenseFile, supplychain.LicensesDir, report.License, kept)
	return nil
}

// A managed family can vendor files that keep their upstream license: the figure engine carries
// interfig's render source under MIT (docs/adr/0016-figures-for-adopters.md, section 11). A
// repository that declares its licensing in REUSE.toml, typically with one table labelling the
// whole tree under its own license, relabels those files unless an annotation names their
// license. Audit warns rather than fails: REUSE is the repository's own choice, and the files
// keep their upstream LICENSE either way.

// auditReuseRecords fails when an annotation of the repository's REUSE.toml never takes effect
// because a later annotation matches every file it names (supplychain.ReuseShadowedPaths): the
// override reuse lint then accepts is not the record REUSE resolves for its files. A repository
// without REUSE.toml skips the check, saying so; one whose REUSE.toml cannot be read or followed
// fails it, since a check that did not run is no pass.
func auditReuseRecords(ctx context.Context, rootDir string) error {
	data, exists, err := contextopt.ObserveSnapshot(ctx, filepath.Join(rootDir, supplychain.ReuseFile))
	if err != nil {
		return fmt.Errorf("[FAIL] %s annotation order not checked: %w", supplychain.ReuseFile, err)
	}
	if !exists {
		fmt.Printf("[SKIP] %s annotation order not checked: the repository has no %s.\n", supplychain.ReuseFile, supplychain.ReuseFile)
		return nil
	}
	tables, err := supplychain.ReuseAnnotationTables(string(data))
	if err != nil {
		return fmt.Errorf("[FAIL] %s annotation order not checked: %w", supplychain.ReuseFile, err)
	}
	shadows, err := supplychain.ReuseShadowedPaths(tables)
	if err != nil {
		return fmt.Errorf("[FAIL] %s annotation order not checked: %w", supplychain.ReuseFile, err)
	}
	if len(shadows) > 0 {
		for _, shadow := range shadows {
			fmt.Printf("  - %s\n", shadow)
		}
		return fmt.Errorf("[FAIL] %s annotation order: %d path(s) resolve to a later annotation, not their own", supplychain.ReuseFile, len(shadows))
	}
	fmt.Printf("[PASS] %s annotation order: no path of its %d annotations is matched whole by a later annotation.\n",
		supplychain.ReuseFile, len(tables))
	return nil
}

// auditVendoredLicenses prints one warning for each of families whose vendored tree the
// repository's REUSE.toml does not label with the tree's license.
func auditVendoredLicenses(ctx context.Context, rootDir string, families []managedasset.Family) {
	for _, warning := range vendoredLicenseWarnings(ctx, rootDir, families) {
		fmt.Printf("[WARN] %s\n", warning)
	}
}

// vendoredLicenseWarnings returns the warnings auditVendoredLicenses prints. A repository without
// REUSE.toml declares its licensing some other way and gets none; a REUSE.toml that cannot be read
// or is past supplychain.MaxReuseLines gets one saying so.
func vendoredLicenseWarnings(ctx context.Context, rootDir string, families []managedasset.Family) []string {
	vendoring := make([]managedasset.Family, 0, len(families))
	for index := 0; index < len(families) && index < managedasset.MaxFamilies; index++ {
		if families[index].VendoredGlob() != "" {
			vendoring = append(vendoring, families[index])
		}
	}
	if len(vendoring) == 0 {
		return nil
	}
	data, exists, err := contextopt.ObserveSnapshot(ctx, filepath.Join(rootDir, supplychain.ReuseFile))
	if err != nil {
		return []string{fmt.Sprintf("%s cannot be read, so the vendored license annotations were not checked: %v", supplychain.ReuseFile, err)}
	}
	if !exists {
		return nil
	}
	tables, err := supplychain.ReuseAnnotationTables(string(data))
	if err != nil {
		return []string{err.Error()}
	}
	var warnings []string
	for _, family := range vendoring {
		if !supplychain.ReuseLabels(tables, family.VendoredGlob(), family.VendoredLicense) {
			warnings = append(warnings, fmt.Sprintf(
				"%s has no annotation labelling %s %s; add an override annotation for that path after every table that also covers it, such as a whole-tree ** table (%s/README.md, Credit)",
				supplychain.ReuseFile, family.VendoredGlob(), family.VendoredLicense, family.Directory))
		}
	}
	return warnings
}
