// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package supplychain

import (
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/cordanaLLM/praetor/internal/util"
)

// The REUSE.toml reads: praetorctl audit's vendored license warning (cmd/standardsctl,
// audit_reuse.go) and the label a copied upstream file must carry (CheckUpstreamCredits). Both
// read the file's shape, not TOML: the module carries no TOML library (util.TOMLTableName).

const (
	// ReuseFile is the REUSE configuration a repository may declare its licensing in.
	ReuseFile = "REUSE.toml"
	// MaxReuseLines bounds one REUSE.toml read (HISS-02).
	MaxReuseLines = 4096
	// reuseAnnotationsTable is the array-of-tables name util.TOMLTableName gives [[annotations]].
	reuseAnnotationsTable = "[annotations]"
)

// reuseLicenseLine matches a single-line SPDX-License-Identifier key and captures its value.
var reuseLicenseLine = regexp.MustCompile(`^\s*SPDX-License-Identifier\s*=\s*["']([^"']*)["']`)

// ReuseAnnotationTables returns the body of every [[annotations]] table of a REUSE.toml, each
// the lines from its header to the next table header. A text past MaxReuseLines is an error.
func ReuseAnnotationTables(text string) ([]string, error) {
	lines := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	if len(lines) > MaxReuseLines {
		return nil, fmt.Errorf("%s holds %d lines, more than the %d its annotation read takes", ReuseFile, len(lines), MaxReuseLines)
	}
	var tables []string
	current := -1
	for index := 0; index < len(lines) && index < MaxReuseLines; index++ {
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

// ReuseLabels reports whether the annotation table that labels glob names license among the
// terms of its SPDX-License-Identifier. REUSE 3.3 applies only the last table matching a file,
// so that table is the last one quoting glob exactly or a glob that covers it, "**" or an
// ancestor directory's "/**" (reuseCoveringGlobs): an override placed before a whole-tree table
// is relabelled by it, and reuse lint still passes. glob may name one file.
func ReuseLabels(tables []string, glob, license string) bool {
	matching := append(reuseCoveringGlobs(glob), glob)
	for index := len(tables) - 1; index >= 0; index-- {
		if slices.ContainsFunc(matching, func(cover string) bool { return reuseQuotes(tables[index], cover) }) {
			return reuseTableLicenses(tables[index], license)
		}
	}
	return false
}

// reuseCoveringGlobs returns the globs that cover every file glob does: "**" and "<dir>/**" for
// each ancestor directory of glob's literal prefix, nearest last.
func reuseCoveringGlobs(glob string) []string {
	segments := strings.Split(strings.TrimSuffix(glob, "/**"), "/")
	covering := []string{"**"}
	for index := 1; index < len(segments) && index < MaxReuseLines; index++ {
		covering = append(covering, strings.Join(segments[:index], "/")+"/**")
	}
	return covering
}

// reuseQuotes reports whether table names glob in double or single quotes.
func reuseQuotes(table, glob string) bool {
	return strings.Contains(table, strconv.Quote(glob)) || strings.Contains(table, "'"+glob+"'")
}

// reuseTableLicenses reports whether table's SPDX-License-Identifier names license among its
// terms (licenseTerms).
func reuseTableLicenses(table, license string) bool {
	for _, line := range strings.Split(table, "\n") {
		match := reuseLicenseLine.FindStringSubmatch(line)
		if match != nil && slices.Contains(licenseTerms(match[1]), license) {
			return true
		}
	}
	return false
}
