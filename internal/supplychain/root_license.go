// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package supplychain

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/cordanaLLM/praetor/internal/config"
	"github.com/cordanaLLM/praetor/internal/contextopt"
	"github.com/cordanaLLM/praetor/internal/util"
)

// A repository that keeps its licence texts in LICENSES/ states one licence at its root: the
// LICENSE file forges and package indexes read, holding the text of the licence the repository
// declares, byte for byte apart from line endings. Every other root file named like a licence,
// which the REUSE specification exempts from labelling and forges may read as the licence, is
// either an upstream notice the repository keeps on purpose, declared as such in the manifest's
// exceptions list, or a second, conflicting licence statement. CheckRootLicense holds the root
// to that.

const (
	// RootLicenseFile is the one root licence file.
	RootLicenseFile = "LICENSE"
	// maxRootEntries bounds the root directory entries CheckRootLicense reads (HISS-02).
	maxRootEntries = 1 << 16
)

// licenceNamedFile matches a file name the REUSE specification treats as a licence file
// (COPYING, LICENSE and LICENCE, each optionally followed by "-" or "." and more), compared
// without case, as forges detect licences.
var licenceNamedFile = regexp.MustCompile(`(?i)^(copying|licen[cs]e)([-.].*)?$`)

// RootLicenseOptions are the inputs of one CheckRootLicense run.
type RootLicenseOptions struct {
	// Root is the repository root.
	Root string
	// Exceptions is the manifest's exceptions list; the check reads the entries of
	// config.ExceptionRuleRootLicenseNotice.
	Exceptions []config.Exception
	// Today is the day expiry is judged against.
	Today time.Time
}

// RootLicenseReport is what CheckRootLicense found.
type RootLicenseReport struct {
	// Skipped says why the check did not run; empty when it ran.
	Skipped string
	// License is the SPDX identifier the repository declares.
	License string
	// Kept are the licence-named root files a live exception keeps.
	Kept []string
	// Findings are the problems, one line each.
	Findings []string
}

// CheckRootLicense checks the root of a repository that keeps LICENSES/ (LicensesDir): LICENSE
// holds the text of LICENSES/<id>.txt for the licence the repository declares (declaredLicense),
// compared as util.CheckoutTextEqual compares a checkout, and every other root file named like a
// licence is kept by a live config.ExceptionRuleRootLicenseNotice entry. An expired entry keeps
// nothing, and an entry that keeps no such file is stale. A root without LICENSES/, or one whose
// declared licence is not one identifier, is skipped with the reason; one that cannot be read is
// an error.
func CheckRootLicense(ctx context.Context, opts RootLicenseOptions) (RootLicenseReport, error) {
	if ctx == nil {
		return RootLicenseReport{}, errors.New("root licence check requires a context")
	}
	info, err := os.Lstat(filepath.Join(opts.Root, LicensesDir))
	if errors.Is(err, os.ErrNotExist) || (err == nil && !info.IsDir()) {
		return RootLicenseReport{Skipped: "the repository keeps no " + LicensesDir + "/ directory"}, nil
	}
	if err != nil {
		return RootLicenseReport{}, fmt.Errorf("inspect %s: %w", LicensesDir, err)
	}
	license, skipped, err := declaredLicense(ctx, opts.Root)
	if err != nil || skipped != "" {
		return RootLicenseReport{Skipped: skipped}, err
	}
	report := RootLicenseReport{License: license}
	finding, err := compareRootLicense(ctx, opts.Root, license)
	if err != nil {
		return RootLicenseReport{}, err
	}
	if finding != "" {
		report.Findings = append(report.Findings, finding)
	}
	if err := judgeLicenceNamedFiles(ctx, opts, &report); err != nil {
		return RootLicenseReport{}, err
	}
	return report, nil
}

// declaredLicense returns the licence the repository at root declares: the one identifier of the
// last REUSE.toml annotation whose path covers the whole tree, or, without such an annotation,
// the one text LICENSES/ holds. A whole-tree annotation naming an expression, and a LICENSES/
// holding several texts and no such annotation, declare no single licence: skipped says so.
func declaredLicense(ctx context.Context, root string) (license, skipped string, err error) {
	data, exists, err := contextopt.ObserveSnapshotIn(ctx, root, ReuseFile)
	if err != nil {
		return "", "", fmt.Errorf("read %s: %w", ReuseFile, err)
	}
	if exists {
		tables, err := ReuseAnnotationTables(string(data))
		if err != nil {
			return "", "", err
		}
		if expression, found := wholeTreeLicense(tables); found {
			return singleLicense(expression, "the whole-tree "+ReuseFile+" annotation names")
		}
	}
	texts, err := licenseTextIDs(ctx, root)
	if err != nil {
		return "", "", err
	}
	if len(texts) != 1 {
		return "", fmt.Sprintf("%s/ holds %d licence texts and no %s annotation labels the whole tree, so no one licence is declared",
			LicensesDir, len(texts), ReuseFile), nil
	}
	return texts[0], "", nil
}

// wholeTreeLicense returns the licence expression of the last table whose globs together match
// every path, and whether there is one: the table REUSE resolves for a file no later table names.
// Its globs may be "**" or a union no one of them covers alone, such as "*", ".*", "*/**" and
// ".*/**".
func wholeTreeLicense(tables []ReuseAnnotation) (string, bool) {
	for index := len(tables) - 1; index >= 0; index-- {
		if reuseGlobIncludes(tables[index].Paths, "**") {
			return strings.Join(tables[index].Licenses, " AND "), true
		}
	}
	return "", false
}

// singleLicense returns expression as the declared licence when it is one SPDX identifier, and
// otherwise why no single licence is declared, opening with source.
func singleLicense(expression, source string) (license, skipped string, err error) {
	if terms := licenseTerms(expression); len(terms) == 1 && len(strings.Fields(expression)) == 1 {
		return terms[0], "", nil
	}
	return "", fmt.Sprintf("%s %q, not one licence; the one-root-licence rule holds a single declared licence", source, expression), nil
}

// licenseTextIDs returns the identifier of every <id>.txt LICENSES/ holds, in name order.
func licenseTextIDs(ctx context.Context, root string) ([]string, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(filepath.Join(root, LicensesDir))
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", LicensesDir, err)
	}
	var ids []string
	for index := 0; index < len(entries) && index < maxRootEntries; index++ {
		if id, isText := strings.CutSuffix(entries[index].Name(), ".txt"); isText && !entries[index].IsDir() {
			ids = append(ids, id)
		}
	}
	return ids, nil
}

// compareRootLicense returns the finding about the root LICENSE: absent, or holding another text
// than LICENSES/<license>.txt; "" when it holds that text.
func compareRootLicense(ctx context.Context, root, license string) (string, error) {
	text := LicensesDir + "/" + license + ".txt"
	want, exists, err := contextopt.ObserveSnapshotIn(ctx, root, text)
	if err != nil {
		return "", fmt.Errorf("read %s: %w", text, err)
	}
	if !exists {
		return fmt.Sprintf("%s is missing: the repository declares %s, so %s/ must hold its text", text, license, LicensesDir), nil
	}
	got, exists, err := contextopt.ObserveSnapshotIn(ctx, root, RootLicenseFile)
	if err != nil {
		return "", fmt.Errorf("read %s: %w", RootLicenseFile, err)
	}
	if !exists {
		return fmt.Sprintf("%s is missing: copy %s to %s, the one root licence of a repository that declares %s",
			RootLicenseFile, text, RootLicenseFile, license), nil
	}
	if equal, strict := util.CheckoutTextEqual(got, want); !equal {
		return fmt.Sprintf("%s differs from %s%s: the root licence must be the text of the declared licence %s",
			RootLicenseFile, text, util.ByteExactNote(strict), license), nil
	}
	return "", nil
}

// judgeLicenceNamedFiles adds to report every root file named like a licence other than LICENSE:
// kept when a live exception names it, a finding otherwise, and a finding for every exception
// entry of the rule that keeps no such file.
func judgeLicenceNamedFiles(ctx context.Context, opts RootLicenseOptions, report *RootLicenseReport) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	entries, err := os.ReadDir(opts.Root)
	if err != nil {
		return fmt.Errorf("read the repository root: %w", err)
	}
	if len(entries) > maxRootEntries {
		return fmt.Errorf("the repository root holds %d entries, more than the %d the root licence check reads", len(entries), maxRootEntries)
	}
	kept := config.ExceptionsFor(opts.Exceptions, config.ExceptionRuleRootLicenseNotice)
	used := make([]bool, len(kept))
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || name == RootLicenseFile || !licenceNamedFile.MatchString(name) {
			continue
		}
		if finding := judgeLicenceNamedFile(name, kept, used, opts.Today); finding != "" {
			report.Findings = append(report.Findings, finding)
		} else {
			report.Kept = append(report.Kept, name)
		}
	}
	for index := range kept {
		if !used[index] {
			report.Findings = append(report.Findings, fmt.Sprintf("exceptions entry %s (%s): keeps no root file named like a licence; remove the entry",
				kept[index].Target(), config.ExceptionRuleRootLicenseNotice))
		}
	}
	return nil
}

// judgeLicenceNamedFile returns "" when a live entry of kept names the root file name, and
// otherwise the finding. Every entry naming it is marked used, expired or not (config.ExceptionFor),
// so an expired entry is reported once, through its file.
func judgeLicenceNamedFile(name string, kept []config.Exception, used []bool, today time.Time) string {
	live, expired := config.ExceptionFor(kept, name, today, used)
	if live != nil {
		return ""
	}
	if expired != nil {
		return fmt.Sprintf("%s: a second root licence file; its %s exception expired on %s", name, config.ExceptionRuleRootLicenseNotice, expired.Expires)
	}
	return fmt.Sprintf("%s: a second root licence file beside %s; merge it into %s, move an upstream notice under a name "+
		"that is no licence name, or keep it with an exceptions entry of rule %s", name, RootLicenseFile, RootLicenseFile,
		config.ExceptionRuleRootLicenseNotice)
}
