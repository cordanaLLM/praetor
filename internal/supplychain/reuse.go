// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package supplychain

import (
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/cordanaLLM/praetor/internal/util"
)

// REUSE.toml data read by licensing gates and upstream credit checks.
const (
	ReuseFile     = "REUSE.toml"
	MaxReuseLines = 4096
)

type ReuseAnnotation struct {
	Paths    []string
	Licenses []string
}

type reuseDocument struct {
	Version     any `toml:"version"`
	Annotations any `toml:"annotations"`
}

type reuseAnnotationInput struct {
	Path                  any `toml:"path"`
	Precedence            any `toml:"precedence"`
	SPDXLicenseIdentifier any `toml:"SPDX-License-Identifier"`
	SPDXFileCopyrightText any `toml:"SPDX-FileCopyrightText"`
}

// parseReuseAnnotation parses a single REUSE annotation table item.
func parseReuseAnnotation(item any, index int) (ReuseAnnotation, error) {
	annotation, ok := item.(map[string]any)
	if !ok {
		return ReuseAnnotation{}, fmt.Errorf("%s annotation %d must be a table", ReuseFile, index+1)
	}
	if err := rejectDottedReuseKeys(annotation, index); err != nil {
		return ReuseAnnotation{}, err
	}
	input := reuseAnnotationInput{
		Path: annotation["path"], Precedence: annotation["precedence"],
		SPDXLicenseIdentifier: annotation["SPDX-License-Identifier"],
		SPDXFileCopyrightText: annotation["SPDX-FileCopyrightText"],
	}
	paths, err := reuseStrings(input.Path, index, "path")
	if err != nil {
		return ReuseAnnotation{}, err
	}
	licenses, err := reuseStrings(input.SPDXLicenseIdentifier, index, "SPDX-License-Identifier")
	if err != nil {
		return ReuseAnnotation{}, err
	}
	return ReuseAnnotation{Paths: paths, Licenses: licenses}, nil
}

// ReuseAnnotationTables parses the full REUSE document and returns each annotation's paths and
// license identifiers. TOML syntax errors name REUSE.toml; oversized documents are refused.
func ReuseAnnotationTables(text string) ([]ReuseAnnotation, error) {
	if lines := strings.Count(text, "\n") + 1; lines > MaxReuseLines {
		return nil, fmt.Errorf("%s holds %d lines, more than the %d its annotation read takes", ReuseFile, lines, MaxReuseLines)
	}
	var document reuseDocument
	if err := util.DecodeTOML(ReuseFile, []byte(text), &document); err != nil {
		return nil, err
	}
	entries, ok := document.Annotations.([]any)
	if !ok {
		if document.Annotations == nil {
			return []ReuseAnnotation{}, nil
		}
		return nil, fmt.Errorf("%s annotations must be an array of tables", ReuseFile)
	}
	if len(entries) > MaxReuseLines {
		return nil, fmt.Errorf("%s has more than %d annotation tables", ReuseFile, MaxReuseLines)
	}
	tables := make([]ReuseAnnotation, 0, len(entries))
	for index, item := range entries {
		ann, err := parseReuseAnnotation(item, index)
		if err != nil {
			return nil, err
		}
		tables = append(tables, ann)
	}
	return tables, nil
}

// rejectDottedReuseKeys refuses nested forms of keys whose literal shape affects labels.
func rejectDottedReuseKeys(annotation map[string]any, index int) error {
	for _, key := range []string{"path", "precedence", "SPDX-FileCopyrightText", "SPDX-License-Identifier"} {
		if _, nested := annotation[key].(map[string]any); nested {
			return fmt.Errorf("%s annotation %d key %s uses unsupported dotted fields", ReuseFile, index+1, key)
		}
	}
	return nil
}

// reuseStrings converts a REUSE annotation value to its string list and names invalid values.
func reuseStrings(value any, table int, key string) ([]string, error) {
	if value == nil {
		return nil, nil
	}
	if item, ok := value.(string); ok {
		return []string{item}, nil
	}
	items, ok := value.([]any)
	if !ok {
		return nil, fmt.Errorf("%s annotation %d %s must be a string or an array of strings", ReuseFile, table+1, key)
	}
	if len(items) > MaxReuseLines {
		return nil, fmt.Errorf("%s annotation %d %s has more than %d values", ReuseFile, table+1, key, MaxReuseLines)
	}
	result := make([]string, 0, len(items))
	for _, item := range items {
		text, ok := item.(string)
		if !ok {
			return nil, fmt.Errorf("%s annotation %d %s array must contain only strings", ReuseFile, table+1, key)
		}
		result = append(result, text)
	}
	return result, nil
}

// ReuseLabels reports whether REUSE.toml labels every file subject names with license among the
// terms of a license expression. subject is one file's path, or a directory glob "<dir>/**"
// naming every file below dir. REUSE 3.3 applies only the last table whose path globs match a
// file, so the tables are read from the last: one whose globs together match every file of
// subject decides, and one matching only some of them, such as "**/*.md" for a directory, decides
// when it does not name license, since those files lose the label. An override placed before a
// whole-tree table is thus relabelled by it, and reuse lint still passes. Comparisons past
// maxReuseGlobSteps are ErrReuseGlobBound, never a guess.
func ReuseLabels(tables []ReuseAnnotation, subject, license string) (bool, error) {
	return reuseLabels(tables, subject, license, maxReuseGlobSteps)
}

// reuseLabels is ReuseLabels with a budget of steps.
func reuseLabels(tables []ReuseAnnotation, subject, license string, steps int) (bool, error) {
	glob := reuseSubjectGlob(subject)
	check := newReuseGlobCheck(append(reuseTablePaths(tables), glob), steps)
	for index := len(tables) - 1; index >= 0; index-- {
		covers, err := check.includes(tables[index].Paths, glob)
		if err != nil {
			return false, fmt.Errorf("%s label of %s not checked: %w", ReuseFile, subject, err)
		}
		labels := slices.ContainsFunc(tables[index].Licenses, func(expression string) bool {
			return slices.Contains(licenseTerms(expression), license)
		})
		if covers || (!labels && tables[index].overlaps(subject)) {
			return labels, nil
		}
	}
	return false, nil
}

// ReuseShadow is one path glob of a REUSE.toml annotation table that never takes effect: the
// globs of the tables after it match every path it does, and REUSE 3.3 applies to a file only the
// last table in the file whose path matches it.
type ReuseShadow struct {
	// Table and Path are the shadowed table, numbered from 1 in file order, and its glob.
	Table int
	Path  string
	// By is the later table that completes the shadow: the globs of the tables after Table up to
	// By together match every file of Path, and those before By do not.
	By int
	// ByPaths are the globs of By when they match every file of Path on their own, as a whole-tree
	// default does, and nil when only the tables up to By together do.
	ByPaths []string
}

// String names the shadow and why the annotation does not take effect.
func (s ReuseShadow) String() string {
	shadow := fmt.Sprintf("annotations %d to %d, after it, together match", s.Table+1, s.By)
	if len(s.ByPaths) > 0 {
		shadow = fmt.Sprintf("annotation %d %s, after it, matches", s.By, reusePathsText(s.ByPaths))
	}
	return fmt.Sprintf("%s annotation %d path %q never takes effect: %s every file it names, and REUSE applies only the last "+
		"matching annotation, whatever its precedence; move annotation %d after annotation %d",
		ReuseFile, s.Table, s.Path, shadow, s.Table, s.By)
}

// reusePathsText names the globs of one table: path "**", or paths "*", "*/**".
func reusePathsText(paths []string) string {
	quoted := make([]string, 0, len(paths))
	for _, glob := range paths {
		quoted = append(quoted, strconv.Quote(glob))
	}
	if len(quoted) == 1 {
		return "path " + quoted[0]
	}
	return "paths " + strings.Join(quoted, ", ")
}

// ReuseShadowedPaths returns every path glob of tables whose files all resolve to a later table:
// the record REUSE resolves for each of them is a later table, never this one. The specification
// says so for any precedence ("exclusively the last matching table in the file is used"), and
// the reuse tool reads the tables from the last (reuse/global_licensing.py,
// ReuseTOML.find_annotations_item): precedence = "override" or "aggregate" decides only between
// the table and licensing information inside the file or in other REUSE.toml files. A default
// table placed after an override is the usual cause, whether it names the whole tree with "**" or
// with several globs such as "*", ".*", "*/**" and ".*/**". Later globs matching only some of the
// paths, such as a narrower override after a whole-tree default, leave the rest in effect and are
// no shadow. A file whose comparisons need more than maxReuseGlobSteps automaton steps is
// ErrReuseGlobBound, never answered in part.
func ReuseShadowedPaths(tables []ReuseAnnotation) ([]ReuseShadow, error) {
	return reuseShadowedPaths(tables, maxReuseGlobSteps)
}

// reuseShadowedPaths is ReuseShadowedPaths with a budget of steps.
func reuseShadowedPaths(tables []ReuseAnnotation, steps int) ([]ReuseShadow, error) {
	check := newReuseGlobCheck(reuseTablePaths(tables), steps)
	var shadows []ReuseShadow
	for index := range tables {
		for _, glob := range tables[index].Paths {
			shadow, found, err := reuseShadowOf(check, tables, index, glob)
			if err != nil {
				return nil, fmt.Errorf("%s annotation %d path %q: %w", ReuseFile, index+1, glob, err)
			}
			if found {
				shadows = append(shadows, shadow)
			}
		}
	}
	return shadows, nil
}

// maxReuseShadowSearch bounds the halvings of one reuseShadowOf search (HISS-02): enough for any
// slice length.
const maxReuseShadowSearch = 64

// reuseShadowOf returns the shadow of glob, a path of tables[table], and whether the tables after
// it together match every path glob does. The table that completes it is the first k for which
// the globs of the k tables after table do; coverage only grows with k, so the search halves the
// range.
func reuseShadowOf(check *reuseGlobCheck, tables []ReuseAnnotation, table int, glob string) (ReuseShadow, bool, error) {
	later := tables[table+1:]
	if covered, err := check.includes(reuseTablePaths(later), glob); err != nil || !covered {
		return ReuseShadow{}, false, err
	}
	low, high := 1, len(later)
	for pass := 0; low < high && pass < maxReuseShadowSearch; pass++ {
		middle := low + (high-low)/2
		covered, err := check.includes(reuseTablePaths(later[:middle]), glob)
		if err != nil {
			return ReuseShadow{}, false, err
		}
		if covered {
			high = middle
		} else {
			low = middle + 1
		}
	}
	by := table + high
	shadow := ReuseShadow{Table: table + 1, Path: glob, By: by + 1}
	alone, err := check.includes(tables[by].Paths, glob)
	if alone {
		shadow.ByPaths = tables[by].Paths
	}
	return shadow, true, err
}

// reuseTablePaths returns the globs of tables, in file order.
func reuseTablePaths(tables []ReuseAnnotation) []string {
	var paths []string
	for _, table := range tables {
		paths = append(paths, table.Paths...)
	}
	return paths
}

// overlaps reports whether one of the table's globs matches at least one file subject names.
func (a ReuseAnnotation) overlaps(subject string) bool {
	return slices.ContainsFunc(a.Paths, func(glob string) bool { return reuseGlobOverlaps(glob, subject) })
}
