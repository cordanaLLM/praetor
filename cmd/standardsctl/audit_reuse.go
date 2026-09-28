// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package main

import (
	"context"
	"fmt"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/cordanaLLM/praetor/internal/contextopt"
	"github.com/cordanaLLM/praetor/internal/managedasset"
	"github.com/cordanaLLM/praetor/internal/util"
)

// A managed family can vendor files that keep their upstream license: the figure engine carries
// interfig's render source under MIT (docs/adr/0016-figures-for-adopters.md, section 11). A
// repository that declares its licensing in REUSE.toml, typically with one table labelling the
// whole tree under its own license, relabels those files unless an annotation names their
// license. Audit warns rather than fails: REUSE is the repository's own choice, and the files
// keep their upstream LICENSE either way.

const (
	// reuseFile is the REUSE configuration a repository may declare its licensing in.
	reuseFile = "REUSE.toml"
	// maxReuseLines bounds the REUSE.toml read (HISS-02).
	maxReuseLines = 4096
	// reuseAnnotationsTable is the array-of-tables name util.TOMLTableName gives [[annotations]].
	reuseAnnotationsTable = "[annotations]"
)

// reuseLicenseLine matches a single-line SPDX-License-Identifier key and captures its value.
var reuseLicenseLine = regexp.MustCompile(`^\s*SPDX-License-Identifier\s*=\s*["']([^"']*)["']`)

// auditVendoredLicenses prints one warning for each of families whose vendored tree the
// repository's REUSE.toml does not label with the tree's license.
func auditVendoredLicenses(ctx context.Context, rootDir string, families []managedasset.Family) {
	for _, warning := range vendoredLicenseWarnings(ctx, rootDir, families) {
		fmt.Printf("[WARN] %s\n", warning)
	}
}

// vendoredLicenseWarnings returns the warnings auditVendoredLicenses prints. A repository without
// REUSE.toml declares its licensing some other way and gets none; a REUSE.toml that cannot be read
// or is past maxReuseLines gets one saying so.
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
	data, exists, err := contextopt.ObserveSnapshot(ctx, filepath.Join(rootDir, reuseFile))
	if err != nil {
		return []string{fmt.Sprintf("%s cannot be read, so the vendored license annotations were not checked: %v", reuseFile, err)}
	}
	if !exists {
		return nil
	}
	tables, err := reuseAnnotationTables(string(data))
	if err != nil {
		return []string{err.Error()}
	}
	var warnings []string
	for _, family := range vendoring {
		if !reuseLabels(tables, family.VendoredGlob(), family.VendoredLicense) {
			warnings = append(warnings, fmt.Sprintf(
				"%s has no annotation labelling %s %s; add an override annotation for that path after any table that covers the whole tree (%s/README.md, Credit)",
				reuseFile, family.VendoredGlob(), family.VendoredLicense, family.Directory))
		}
	}
	return warnings
}

// reuseAnnotationTables returns the body of every [[annotations]] table of a REUSE.toml, each the
// lines from its header to the next table header. It reads the file's shape, not TOML.
func reuseAnnotationTables(text string) ([]string, error) {
	lines := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	if len(lines) > maxReuseLines {
		return nil, fmt.Errorf("%s holds %d lines, more than the %d the vendored license check reads", reuseFile, len(lines), maxReuseLines)
	}
	var tables []string
	current := -1
	for index := 0; index < len(lines) && index < maxReuseLines; index++ {
		if strings.HasPrefix(strings.TrimSpace(lines[index]), "[") {
			current = -1
			if util.TOMLTableName(lines[index]) == reuseAnnotationsTable {
				tables = append(tables, "")
				current = len(tables) - 1
			}
			continue
		}
		if current >= 0 {
			tables[current] += lines[index] + "\n"
		}
	}
	return tables, nil
}

// reuseLabels reports whether one annotation table names glob, quoted exactly, in its paths and
// license among the terms of its SPDX-License-Identifier.
func reuseLabels(tables []string, glob, license string) bool {
	for _, table := range tables {
		if !strings.Contains(table, strconv.Quote(glob)) && !strings.Contains(table, "'"+glob+"'") {
			continue
		}
		for _, line := range strings.Split(table, "\n") {
			match := reuseLicenseLine.FindStringSubmatch(line)
			if match != nil && slices.Contains(strings.FieldsFunc(match[1], isSPDXSeparator), license) {
				return true
			}
		}
	}
	return false
}

// isSPDXSeparator splits an SPDX license expression into its terms and operators.
func isSPDXSeparator(r rune) bool {
	return r == ' ' || r == '\t' || r == '(' || r == ')'
}
