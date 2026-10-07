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

// The REUSE.toml reads: praetorctl audit's vendored license warning (cmd/standardsctl,
// audit_reuse.go) and the label a copied upstream file must carry (CheckUpstreamCredits). Both
// read the file's shape, not TOML: the module carries no TOML library (util.TOMLTableName).
// Only the values of the path and SPDX-License-Identifier keys count; comments and the other
// keys never match a path.

const (
	// ReuseFile is the REUSE configuration a repository may declare its licensing in.
	ReuseFile = "REUSE.toml"
	// MaxReuseLines bounds one REUSE.toml read (HISS-02).
	MaxReuseLines = 4096
	// reuseAnnotationsTable is the array-of-tables name util.TOMLTableName gives [[annotations]].
	reuseAnnotationsTable = "[annotations]"
	// reusePathKey holds the globs an annotation table covers, a string or an array of strings.
	reusePathKey = "path"
	// reuseLicenseKey holds the SPDX license expressions of a table, a string or an array.
	reuseLicenseKey = "SPDX-License-Identifier"
)

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
}

// ReuseAnnotationTables returns every [[annotations]] table of a REUSE.toml with the values of
// its path and SPDX-License-Identifier keys, each a single-line string or an array of them that
// may span lines. A text past MaxReuseLines is an error, and so is one the read cannot follow:
// a line that is no table header, key or comment, a multi-line string, an array of anything but
// such strings or one left open, and a path or license value that is not a string or an array.
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
	if scan.listOpen {
		return nil, fmt.Errorf("%s: the %s array is not closed", ReuseFile, scan.listKey)
	}
	return scan.tables, nil
}

// read takes one trimmed line: the next part of an open array, a comment, a table header, or a
// key.
func (s *reuseScan) read(line string) error {
	switch {
	case s.listOpen:
		// No element spans lines, so each line of an open array reads as an array of its own.
		return s.readList("[" + line)
	case line == "" || strings.HasPrefix(line, "#"):
		return nil
	case strings.HasPrefix(line, "["):
		s.inTable = util.TOMLTableName(line) == reuseAnnotationsTable
		if s.inTable {
			s.tables = append(s.tables, ReuseAnnotation{})
		}
		return nil
	}
	return s.assign(line)
}

// assign reads the key line assigns and records its strings when it is a key ReuseAnnotation
// holds.
func (s *reuseScan) assign(line string) error {
	key, value, ok := util.TOMLKeyValue(line)
	if !ok {
		return fmt.Errorf("%q is not a table header, a key or a comment", line)
	}
	s.listKey, s.listItems = strings.Trim(key, `"'`), nil
	if strings.HasPrefix(value, `"""`) || strings.HasPrefix(value, "'''") {
		return fmt.Errorf("%s holds a multi-line string, which this read does not follow", s.listKey)
	}
	if strings.HasPrefix(value, "[") {
		s.listOpen = true
		return s.readList(value)
	}
	text, isString := util.TOMLStringValue(value)
	if isString {
		s.record([]string{text})
		return nil
	}
	if s.inTable && (s.listKey == reusePathKey || s.listKey == reuseLicenseKey) {
		return fmt.Errorf("%s = %s is not a string without escape sequences or an array of them", s.listKey, value)
	}
	return nil
}

// readList reads text, one line of the array listKey opened, and records the array once it
// closes.
func (s *reuseScan) readList(text string) error {
	items, closed, ok := util.TOMLStringArray(text + "\n")
	if !ok {
		return fmt.Errorf("the %s array holds something other than strings without escape sequences, each on one line", s.listKey)
	}
	s.listItems = append(s.listItems, items...)
	if closed {
		s.listOpen = false
		s.record(s.listItems)
	}
	return nil
}

// record keeps the strings of listKey on the current table when the key is one it holds.
func (s *reuseScan) record(values []string) {
	if !s.inTable || len(s.tables) == 0 {
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
// file, so the tables are read from the last: one whose globs match every file of subject
// decides, and one matching only some of them, such as "**/*.md" for a directory, decides when
// it does not name license, since those files lose the label. An override placed before a
// whole-tree table is thus relabelled by it, and reuse lint still passes.
func ReuseLabels(tables []ReuseAnnotation, subject, license string) bool {
	for index := len(tables) - 1; index >= 0; index-- {
		covers, overlaps := tables[index].relation(subject)
		labels := slices.ContainsFunc(tables[index].Licenses, func(expression string) bool {
			return slices.Contains(licenseTerms(expression), license)
		})
		if covers || (overlaps && !labels) {
			return labels
		}
	}
	return false
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

// maxReuseShadowChecks bounds the glob pairs one ReuseShadowedPaths compares (HISS-02).
const maxReuseShadowChecks = 1 << 16

// ReuseShadowedPaths returns every path glob of tables whose files all resolve to a later table:
// the record REUSE resolves for each of them is a later table, never this one. The specification
// says so for any precedence ("exclusively the last matching table in the file is used"), and
// the reuse tool reads the tables from the last (reuse/global_licensing.py,
// ReuseTOML.find_annotations_item): precedence = "override" or "aggregate" decides only between
// the table and licensing information inside the file or in other REUSE.toml files. A default
// table placed after an override is the usual cause, whether it names the whole tree with "**" or
// with several globs such as "*", ".*", "*/**" and ".*/**". Later globs matching only some of the
// paths, such as a narrower override after a whole-tree default, leave the rest in effect and are
// no shadow. A file holding more than maxReuseShadowChecks pairs of a path and a later path is
// refused before the first comparison, never answered in part.
func ReuseShadowedPaths(tables []ReuseAnnotation) ([]ReuseShadow, error) {
	pairs, later := 0, 0
	for index := len(tables) - 1; index >= 0; index-- {
		pairs += len(tables[index].Paths) * later
		later += len(tables[index].Paths)
		if pairs > maxReuseShadowChecks {
			return nil, fmt.Errorf("%s holds more path pairs than the %d its annotation order check compares", ReuseFile, maxReuseShadowChecks)
		}
	}
	var shadows []ReuseShadow
	for index := range tables {
		for _, glob := range tables[index].Paths {
			if by := reuseShadowOf(tables[index+1:], glob); by > 0 {
				shadows = append(shadows, newReuseShadow(tables, index, glob, index+by))
			}
		}
	}
	return shadows, nil
}

// newReuseShadow is the shadow of glob, a path of tables[table], completed by tables[by].
func newReuseShadow(tables []ReuseAnnotation, table int, glob string, by int) ReuseShadow {
	shadow := ReuseShadow{Table: table + 1, Path: glob, By: by + 1}
	if reuseGlobIncludes(tables[by].Paths, glob) {
		shadow.ByPaths = tables[by].Paths
	}
	return shadow
}

// maxReuseShadowSearch bounds the halvings of one reuseShadowOf search (HISS-02): enough for any
// slice length.
const maxReuseShadowSearch = 64

// reuseShadowOf returns the position in later, from 1, of the table that completes the shadow of
// glob: the first k for which the globs of later[:k] together match every path glob does, or 0
// when all of later does not. Coverage only grows with k, so the search halves the range.
func reuseShadowOf(later []ReuseAnnotation, glob string) int {
	if !reuseGlobIncludes(reuseTablePaths(later), glob) {
		return 0
	}
	low, high := 1, len(later)
	for pass := 0; low < high && pass < maxReuseShadowSearch; pass++ {
		middle := low + (high-low)/2
		if reuseGlobIncludes(reuseTablePaths(later[:middle]), glob) {
			high = middle
		} else {
			low = middle + 1
		}
	}
	return high
}

// reuseTablePaths returns the globs of tables, in file order.
func reuseTablePaths(tables []ReuseAnnotation) []string {
	var paths []string
	for _, table := range tables {
		paths = append(paths, table.Paths...)
	}
	return paths
}

// relation reports whether one of the table's globs matches every file subject names (covers)
// and whether one matches at least one of them (overlaps).
func (a ReuseAnnotation) relation(subject string) (covers, overlaps bool) {
	for _, glob := range a.Paths {
		globCovers, globOverlaps := reuseGlobRelation(glob, subject)
		covers = covers || globCovers
		overlaps = overlaps || globOverlaps
	}
	return covers, overlaps
}
