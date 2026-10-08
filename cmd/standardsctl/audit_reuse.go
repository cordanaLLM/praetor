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
	return errors.Join(auditReuseRecords(ctx, manifest, rootDir, today), auditRootLicense(ctx, manifest, rootDir, today))
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
// because later annotations match every file it names (supplychain.ReuseShadowedPaths): the
// override reuse lint then accepts is not the record REUSE resolves for its files. A repository
// without REUSE.toml skips the check, saying so; one whose REUSE.toml cannot be read or followed
// fails it, since a check that did not run is no pass. A file whose globs need more comparison
// steps than the bound is not checked: it fails, naming the bound, unless the manifest excuses it
// (reuseOrderBound), and an entry excusing a file checked in full is stale and fails too.
func auditReuseRecords(ctx context.Context, manifest *config.Manifest, rootDir string, today time.Time) error {
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
	entries := config.ExceptionsFor(manifest.Exceptions, config.ExceptionRuleReuseAnnotationOrder)
	shadows, err := supplychain.ReuseShadowedPaths(tables)
	if errors.Is(err, supplychain.ErrReuseGlobBound) {
		return reuseOrderBound(entries, err, today)
	}
	if err != nil {
		return fmt.Errorf("[FAIL] %s annotation order not checked: %w", supplychain.ReuseFile, err)
	}
	return reuseOrderVerdict(shadows, entries, len(tables))
}

// reuseOrderVerdict prints the verdict of an annotation order check that ran in full over a
// REUSE.toml of tables annotations: every shadow and every reuse-annotation-order entry, which
// excuses nothing here, before the failure, or the pass.
func reuseOrderVerdict(shadows []supplychain.ReuseShadow, entries []config.Exception, tables int) error {
	for _, shadow := range shadows {
		fmt.Printf("  - %s\n", shadow)
	}
	for _, entry := range entries {
		fmt.Printf("  - exceptions entry %s (%s): the annotation order was checked in full, so the entry excuses nothing; remove it\n",
			entry.Target(), config.ExceptionRuleReuseAnnotationOrder)
	}
	switch {
	case len(shadows) > 0:
		return fmt.Errorf("[FAIL] %s annotation order: %d path(s) resolve to a later annotation, not their own", supplychain.ReuseFile, len(shadows))
	case len(entries) > 0:
		return fmt.Errorf("[FAIL] %s annotation order: its %s exceptions entries excuse nothing", supplychain.ReuseFile, config.ExceptionRuleReuseAnnotationOrder)
	}
	fmt.Printf("[PASS] %s annotation order: no path of its %d annotations is matched whole by the annotations after it.\n",
		supplychain.ReuseFile, tables)
	return nil
}

// reuseOrderBound returns the verdict of an annotation order check that stopped at its step bound
// (supplychain.ErrReuseGlobBound, bound): not checked either way. A live exceptions entry of rule
// reuse-annotation-order naming REUSE.toml excuses it, and the check prints the entry's reason and
// expiry; an expired one excuses nothing, and without one the check fails, naming the bound and
// the entry that would excuse it.
func reuseOrderBound(entries []config.Exception, bound error, today time.Time) error {
	used := make([]bool, len(entries))
	live, expired := config.ExceptionFor(entries, supplychain.ReuseFile, today, used)
	switch {
	case live != nil:
		entry := *live
		fmt.Printf("[SKIP] %s annotation order not checked: %v; excused by exceptions entry %s (%s) until %s: %s.\n",
			supplychain.ReuseFile, bound, entry.Target(), config.ExceptionRuleReuseAnnotationOrder, entry.Expires, entry.Reason)
		return nil
	case expired != nil:
		return fmt.Errorf("[FAIL] %s annotation order not checked: %w; its %s exception expired on %s",
			supplychain.ReuseFile, bound, config.ExceptionRuleReuseAnnotationOrder, expired.Expires)
	}
	return fmt.Errorf("[FAIL] %s annotation order not checked: %w; merge its path globs into fewer tables, or keep the file "+
		"unchecked with an exceptions entry of rule %s naming %s (docs/guides/licensing-gates.md)",
		supplychain.ReuseFile, bound, config.ExceptionRuleReuseAnnotationOrder, supplychain.ReuseFile)
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
		if warning := vendoredLabelWarning(tables, family); warning != "" {
			warnings = append(warnings, warning)
		}
	}
	return warnings
}

// vendoredLabelWarning returns the warning about one family whose vendored tree tables do not
// label with the tree's license, or about a label the check could not decide, and "" when tables
// label it.
func vendoredLabelWarning(tables []supplychain.ReuseAnnotation, family managedasset.Family) string {
	labelled, err := supplychain.ReuseLabels(tables, family.VendoredGlob(), family.VendoredLicense)
	switch {
	case err != nil:
		return fmt.Sprintf("the vendored license annotation of %s was not checked: %v", family.Directory, err)
	case labelled:
		return ""
	}
	return fmt.Sprintf(
		"%s has no annotation labelling %s %s; add an override annotation for that path after every table that also covers it, such as a whole-tree ** table (%s/README.md, Credit)",
		supplychain.ReuseFile, family.VendoredGlob(), family.VendoredLicense, family.Directory)
}
