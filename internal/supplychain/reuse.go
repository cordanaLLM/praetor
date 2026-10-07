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

// The REUSE.toml reads: praetorctl audit's licensing gates and vendored license warning
// (cmd/standardsctl, audit_reuse.go) and the label a copied upstream file must carry
// (CheckUpstreamCredits). They read the file's shape, not TOML: the module carries no TOML
// library (util.TOMLTableName). The read mirrors what the reuse tool parses
// (reuse/global_licensing.py, ReuseTOML.from_dict and AnnotationsItem.from_dict), so it follows
// an allow-list and refuses everything else, naming the line: the version key, [[annotations]]
// table headers, and in each table the four keys REUSE reads, one per line. Only the values of
// path and SPDX-License-Identifier count; the values of version, precedence and
// SPDX-FileCopyrightText are stepped over whatever they hold (util.TOMLValueScan). Any other
// top-level key, such as annotations written as an inline array of tables, any dotted key, any
// other table header and any other key of a table fail closed, since REUSE may read them as
// annotations this read never sees.

const (
	// ReuseFile is the REUSE configuration a repository may declare its licensing in.
	ReuseFile = "REUSE.toml"
	// MaxReuseLines bounds one REUSE.toml read (HISS-02).
	MaxReuseLines = 4096
	// reuseAnnotationsTable is the array-of-tables name util.TOMLTableName gives [[annotations]].
	reuseAnnotationsTable = "[annotations]"
	// reuseVersionKey is the one top-level key of a REUSE.toml.
	reuseVersionKey = "version"
	// reusePathKey holds the globs an annotation table covers, a string or an array of strings.
	reusePathKey = "path"
	// reuseLicenseKey holds the SPDX license expressions of a table, a string or an array.
	reuseLicenseKey = "SPDX-License-Identifier"
)

// reuseSteppedKeys are the keys of an [[annotations]] table REUSE reads and the gates do not:
// their values are stepped over.
var reuseSteppedKeys = []string{"precedence", "SPDX-FileCopyrightText"}

// ReuseAnnotation is one [[annotations]] table of a REUSE.toml.
type ReuseAnnotation struct {
	// Paths are the globs of its path key.
	Paths []string
	// Licenses are the SPDX license expressions of its SPDX-License-Identifier key.
	Licenses []string
}

// reuseScan is the state of one ReuseAnnotationTables read.
type reuseScan struct {
	tables []ReuseAnnotation
	// inTable is whether the lines read belong to an [[annotations]] table.
	inTable bool
	// listOpen is whether the value of listKey is an array not yet closed.
	listOpen  bool
	listKey   string
	listItems []string
	// skipping is whether the value of listKey, a key the read does not keep, continues on the
	// next line, and skip follows that value.
	skipping bool
	skip     util.TOMLValueScan
}

// ReuseAnnotationTables returns every [[annotations]] table of a REUSE.toml with the values of
// its path and SPDX-License-Identifier keys, each a single-line string, its escape sequences
// decoded, or an array of them that may span lines. The values of version, precedence and
// SPDX-FileCopyrightText, such as an array of strings with escape sequences or a multi-line
// string, are stepped over unread. A text past MaxReuseLines is an error, and so is one the read
// does not follow: a line that is no table header, key or comment, a key or table header outside
// the allow-list above, a value whose end it cannot find, and a path or license value that is a
// multi-line string, an array of anything but single-line strings or one left open, or neither a
// string nor an array, so the gates reading those keys never judge a file in part.
func ReuseAnnotationTables(text string) ([]ReuseAnnotation, error) {
	lines := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	if len(lines) > MaxReuseLines {
		return nil, fmt.Errorf("%s holds %d lines, more than the %d its annotation read takes", ReuseFile, len(lines), MaxReuseLines)
	}
	scan := reuseScan{}
	for index := 0; index < len(lines) && index < MaxReuseLines; index++ {
		if err := scan.read(strings.TrimSpace(lines[index])); err != nil {
			return nil, fmt.Errorf("%s:%d: %w", ReuseFile, index+1, err)
		}
	}
	switch {
	case scan.listOpen:
		return nil, fmt.Errorf("%s: the %s array is not closed", ReuseFile, scan.listKey)
	case scan.skipping:
		return nil, fmt.Errorf("%s: the value of %s is not closed", ReuseFile, scan.listKey)
	}
	return scan.tables, nil
}

// read takes one trimmed line: the next part of an open array, a comment, a table header, or a
// key.
func (s *reuseScan) read(line string) error {
	switch {
	case s.skipping:
		return s.skipValue(line)
	case s.listOpen:
		// No element spans lines, so each line of an open array reads as an array of its own.
		return s.readList("[" + line)
	case line == "" || strings.HasPrefix(line, "#"):
		return nil
	case strings.HasPrefix(line, "["):
		return s.header(line)
	}
	return s.assign(line)
}

// header opens the [[annotations]] table line starts, and refuses any other table header.
func (s *reuseScan) header(line string) error {
	if !strings.HasPrefix(line, "[[") || util.TOMLTableName(line) != reuseAnnotationsTable {
		return fmt.Errorf("the table header %s is not one this read follows: %s holds the %s key and [[annotations]] tables, "+
			"each opened by a [[annotations]] line of its own", line, ReuseFile, reuseVersionKey)
	}
	s.inTable = true
	s.tables = append(s.tables, ReuseAnnotation{})
	return nil
}

// assign reads the key line assigns: it records the strings of path and SPDX-License-Identifier,
// steps over the value of another key the allow-list holds, and refuses any other key.
func (s *reuseScan) assign(line string) error {
	key, value, ok := util.TOMLKeyValue(line)
	if !ok {
		return fmt.Errorf("%q is not a table header, a key or a comment", line)
	}
	s.listKey, s.listItems = strings.Trim(key, `"'`), nil
	switch {
	case !s.inTable && s.listKey == reuseVersionKey, s.inTable && slices.Contains(reuseSteppedKeys, s.listKey):
		s.skip = util.TOMLValueScan{}
		return s.skipValue(value)
	case !s.inTable:
		return reuseTopLevelKeyError(s.listKey)
	case s.listKey != reusePathKey && s.listKey != reuseLicenseKey:
		return fmt.Errorf("the key %s of an [[annotations]] table is not one this read follows: a table holds %s, %s and %s, "+
			"each assigned on a line of its own, with no dotted key", s.listKey, reusePathKey, reuseLicenseKey, strings.Join(reuseSteppedKeys, ", "))
	}
	return s.readValue(value)
}

// reuseTopLevelKeyError refuses key, a key outside every table other than version: annotations
// written as an inline array of tables or with dotted keys, which REUSE reads and this read does
// not follow, or a key REUSE.toml does not hold.
func reuseTopLevelKeyError(key string) error {
	if key == "annotations" || strings.HasPrefix(key, "annotations.") {
		return fmt.Errorf("the top-level key %s is not one this read follows: write each annotation as a table of its own, "+
			"opened by a [[annotations]] line and holding one key = value per line, not as an inline array of tables or dotted keys", key)
	}
	return fmt.Errorf("the top-level key %s is not one this read follows: %s holds the %s key and [[annotations]] tables", key, ReuseFile, reuseVersionKey)
}

// readValue records the value of the path or license key, which is a single-line string or an
// array of them.
func (s *reuseScan) readValue(value string) error {
	if strings.HasPrefix(value, `"""`) || strings.HasPrefix(value, "'''") {
		return fmt.Errorf("%s holds a multi-line string, which this read does not follow", s.listKey)
	}
	if strings.HasPrefix(value, "[") {
		s.listOpen = true
		return s.readList(value)
	}
	text, isString := util.TOMLStringValue(value)
	if !isString {
		return fmt.Errorf("%s = %s is not a single-line string or an array of them; a basic string may hold only the escape "+
			"sequences TOML defines, and a literal string ('...') keeps every backslash as written", s.listKey, value)
	}
	s.record([]string{text})
	return nil
}

// skipValue reads text, one line of the value of a key the read does not keep, and notes whether
// the value continues on the next line.
func (s *reuseScan) skipValue(text string) error {
	done, ok := s.skip.Feed(text)
	if !ok {
		return fmt.Errorf("the value of %s is not one this read can find the end of", s.listKey)
	}
	s.skipping = !done
	return nil
}

// readList reads text, one line of the array listKey opened, and records the array once it
// closes.
func (s *reuseScan) readList(text string) error {
	items, closed, ok := util.TOMLStringArray(text + "\n")
	if !ok {
		return fmt.Errorf("the %s array holds something other than single-line strings, each on one line; a basic string may hold "+
			"only the escape sequences TOML defines, and a literal string ('...') keeps every backslash as written", s.listKey)
	}
	s.listItems = append(s.listItems, items...)
	if closed {
		s.listOpen = false
		s.record(s.listItems)
	}
	return nil
}

// record keeps the strings of listKey, path or SPDX-License-Identifier, on the current table.
func (s *reuseScan) record(values []string) {
	if len(s.tables) == 0 {
		return
	}
	table := &s.tables[len(s.tables)-1]
	switch s.listKey {
	case reusePathKey:
		table.Paths = append(table.Paths, values...)
	case reuseLicenseKey:
		table.Licenses = append(table.Licenses, values...)
	}
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
