package supplychain

// THIRD-PARTY-NOTICES.md names every third-party component the release archives and the
// container image carry, and the archives and the image carry the file. Its seven component
// tables are generated: RenderNotices writes one row for each component the sources ship
// (notices_sources.go), taking the name, the version and, for an npm package, the license
// from the source. The cells no source records -- the copyright line, the name cell as
// written and, for a Go module, the base image or the vendored interfig source, the license --
// come from the row the file already holds for that component. The prose and the verbatim upstream license texts outside
// the tables are hand-written and kept as they stand.
//
// A version bump therefore regenerates: `praetorctl sbom notices` rewrites the row.
// A component the file holds no row for, or a license outside the reviewed set, stops the
// render, because its copyright line and terms need a person to read the upstream files.

import (
	"fmt"
	"regexp"
	"slices"
	"strings"
	"unicode"
)

// NoticesFile is the repository-relative notices file the release ships.
const NoticesFile = "THIRD-PARTY-NOTICES.md"

// The "## " headings of NoticesFile whose tables the render writes. Renaming one stops the
// render with the missing section named, so a rename cannot silently disable it.
const (
	noticesGoToolchain  = "Go standard library and runtime"
	noticesGoModules    = "Go modules"
	noticesBaseImage    = "Container base image"
	noticesNPM          = "npm packages of the Markdown gate"
	noticesFigureEngine = "Vendored figure engine"
	noticesFigureNPM    = "npm packages of the figure player"
	// noticesDevContainerNPM lists the packages the embedded devcontainer CLI lock installs.
	noticesDevContainerNPM = "npm packages of the devcontainer CLI"
)

// noticeSections are the generated sections, in the order the render reports on them.
var noticeSections = [...]string{noticesGoToolchain, noticesGoModules, noticesBaseImage, noticesNPM, noticesFigureEngine, noticesFigureNPM, noticesDevContainerNPM}

// goToolchainLicense is the license of the Go standard library and runtime.
const goToolchainLicense = "BSD-3-Clause"

// licenseFromSource names the sections whose license cell the source states: the npm locks
// record each package's license, and the toolchain's is goToolchainLicense. go.mod, a
// Dockerfile and vendor.json record none, so a Go module, the base image or the vendored
// interfig source keeps the license its row states.
var licenseFromSource = map[string]bool{noticesGoToolchain: true, noticesNPM: true, noticesFigureNPM: true, noticesDevContainerNPM: true}

// noticeColumns is the cell count of a generated table row: name, version, license, copyright.
const noticeColumns = 4

// noticeRegenerateHint names the command that brings the file back in step.
const noticeRegenerateHint = "praetorctl sbom notices"

// knownNoticeLicenses are the SPDX license identifiers a notices row may name. A component
// under any other license, or under none, stops the render: its terms need a review before the
// release ships it or the Markdown gate installs it, and adding the identifier here records
// that review.
var knownNoticeLicenses = map[string]bool{
	"Apache-2.0": true, "BSD-2-Clause": true, "BSD-3-Clause": true, "ISC": true, "MIT": true, "PSF-2.0": true, "Python-2.0": true,
}

// licenseOperators are the SPDX expression keywords, which join identifiers and name none.
var licenseOperators = map[string]bool{"AND": true, "OR": true, "WITH": true}

// delimiterCell matches one cell of a Markdown table's delimiter row.
var delimiterCell = regexp.MustCompile(`^:?-+:?$`)

// noticeCells is one data row of a generated table: the parsed row plus the name and
// copyright cells as written, which the render carries to the row it writes.
type noticeCells struct {
	row       noticeRow
	nameCell  string
	copyright string
}

// markdownTable is the one table under a generated "## " heading: the index of its header line,
// the line span [first, end) of its data rows, and those rows. first is 0 until the delimiter
// row has been read. RenderNotices and RenderCreditsPage both locate their tables this way.
type markdownTable struct {
	heading     string
	headerIndex int
	first, end  int
	rows        []tableLine
}

// tableLine is one data row of a table and its 0-based line index.
type tableLine struct {
	index int
	text  string
}

// tableSpan is a line span [start, end) of a document and the rows that replace it.
type tableSpan struct {
	start, end int
	rows       []string
}

// RenderNotices returns notices with the data rows of each generated table replaced by one
// row for every component sources ship, sorted by name and version. Every line outside those
// rows is returned unchanged, with LF line endings. It fails when a generated section or its
// table is missing, when a shipped component has no row to carry its copyright line from, and
// when a component's license is empty or outside knownNoticeLicenses.
func RenderNotices(notices string, sources NoticeSources) (string, error) {
	lines, err := splitNoticeLines(notices)
	if err != nil {
		return "", fmt.Errorf("parse %s: %w", NoticesFile, err)
	}
	tables, err := locateTables(NoticesFile, lines, noticeSections[:])
	if err != nil {
		return "", err
	}
	shipped, err := shippedNoticeRows(sources)
	if err != nil {
		return "", err
	}
	spans := make([]tableSpan, 0, len(tables))
	for _, heading := range noticeSections {
		listed, err := parseNoticeTable(tables[heading])
		if err != nil {
			return "", err
		}
		rows, err := renderNoticeRows(heading, shipped[heading], listed)
		if err != nil {
			return "", err
		}
		spans = append(spans, tableSpan{start: tables[heading].first, end: tables[heading].end, rows: rows})
	}
	return spliceTables(lines, spans), nil
}

// parseNoticeTable reads the data rows of one generated notices table.
func parseNoticeTable(table *markdownTable) ([]noticeCells, error) {
	listed := make([]noticeCells, 0, len(table.rows))
	for _, row := range table.rows {
		cells, err := parseNoticeCells(row.text)
		if err != nil {
			return nil, fmt.Errorf("%s line %d: %w", NoticesFile, row.index+1, err)
		}
		listed = append(listed, cells)
	}
	return listed, nil
}

// CheckNotices fails when notices differ from what RenderNotices writes for sources, naming
// every row that would be removed (-) or added (+). Line endings are not compared, so a CRLF
// checkout of a current file passes.
func CheckNotices(notices string, sources NoticeSources) error {
	rendered, err := RenderNotices(notices, sources)
	if err != nil {
		return err
	}
	current := strings.ReplaceAll(notices, "\r\n", "\n")
	if rendered == current {
		return nil
	}
	return fmt.Errorf("%s is out of step with go.mod, the Dockerfile, the npm locks and the figure engine; run `%s` to regenerate it:\n%s",
		NoticesFile, noticeRegenerateHint, strings.Join(noticeChanges(current, rendered), "\n"))
}

// noticeChanges lists the lines only current holds as "- line" and the lines only rendered
// holds as "+ line", each in file order.
func noticeChanges(current, rendered string) []string {
	before, after := strings.Split(current, "\n"), strings.Split(rendered, "\n")
	var changes []string
	for _, line := range before {
		if !slices.Contains(after, line) {
			changes = append(changes, "- "+line)
		}
	}
	for _, line := range after {
		if !slices.Contains(before, line) {
			changes = append(changes, "+ "+line)
		}
	}
	return changes
}

// tableScanner walks a document's lines once and records the table under each generated
// heading. Lines inside a fenced code block are neither headings nor table rows.
type tableScanner struct {
	file      string
	sections  []string
	tables    map[string]*markdownTable
	heading   string
	fenced    bool
	tableLine int
	current   *markdownTable
}

// locateTables finds the one table under each heading of sections in the lines of file. A
// heading that is missing or holds no table, a second table under one heading and a table
// without a delimiter row are errors: the render would otherwise drop, duplicate or misplace that
// section's rows.
func locateTables(file string, lines, sections []string) (map[string]*markdownTable, error) {
	scanner := &tableScanner{file: file, sections: sections, tables: make(map[string]*markdownTable, len(sections))}
	for index, line := range lines {
		if err := scanner.step(index, line); err != nil {
			return nil, err
		}
	}
	for _, heading := range sections {
		table, found := scanner.tables[heading]
		if !found {
			return nil, fmt.Errorf("%s has no table under a %q heading; the render has nowhere to write that section's rows", file, "## "+heading)
		}
		if table.first == 0 {
			return nil, fmt.Errorf("%s line %d: the %q table has no delimiter row", file, table.headerIndex+1, heading)
		}
	}
	return scanner.tables, nil
}

// step reads one line.
func (s *tableScanner) step(index int, line string) error {
	trimmed := strings.TrimSpace(line)
	if strings.HasPrefix(trimmed, "```") {
		s.fenced = !s.fenced
		s.endTable()
		return nil
	}
	if s.fenced {
		return nil
	}
	if title, isHeading := strings.CutPrefix(line, "## "); isHeading {
		s.endTable()
		s.heading = strings.TrimSpace(title)
		return nil
	}
	if !strings.HasPrefix(trimmed, "|") {
		s.endTable()
		return nil
	}
	return s.tableRow(index, line)
}

// endTable closes the table being read, if any.
func (s *tableScanner) endTable() {
	s.tableLine, s.current = 0, nil
}

// tableRow reads one line of a table: the header opens it, the delimiter row fixes where the
// data rows start, and each data row of a generated table is recorded.
func (s *tableScanner) tableRow(index int, line string) error {
	s.tableLine++
	switch {
	case s.tableLine == 1:
		return s.openTable(index)
	case s.current == nil:
		return nil
	case s.tableLine == 2:
		if !isDelimiterRow(line) {
			return fmt.Errorf("%s line %d: the %q table has no delimiter row", s.file, index+1, s.heading)
		}
		s.current.first, s.current.end = index+1, index+1
		return nil
	}
	s.current.rows = append(s.current.rows, tableLine{index: index, text: line})
	s.current.end = index + 1
	return nil
}

// openTable starts reading a table whose header is at index when it sits under a generated
// heading, and refuses a second table there.
func (s *tableScanner) openTable(index int) error {
	if !slices.Contains(s.sections, s.heading) {
		return nil
	}
	if _, twice := s.tables[s.heading]; twice {
		return fmt.Errorf("%s line %d: the %q section holds a second table; the render writes exactly one", s.file, index+1, s.heading)
	}
	s.current = &markdownTable{heading: s.heading, headerIndex: index}
	s.tables[s.heading] = s.current
	return nil
}

// tableCells splits one Markdown table row into its cells, without the outer pipes.
func tableCells(line string) []string {
	return strings.Split(strings.Trim(strings.TrimSpace(line), "|"), "|")
}

// isDelimiterRow reports whether line is a Markdown table delimiter row such as "| :-- | --- |".
func isDelimiterRow(line string) bool {
	cells := tableCells(line)
	return !slices.ContainsFunc(cells, func(cell string) bool { return !delimiterCell.MatchString(strings.TrimSpace(cell)) })
}

// parseNoticeCells reads one data row of a generated table: exactly a name, a version, a
// license and a non-empty copyright cell. Backticks around the name and version are
// stripped from the parsed row and kept in the name cell as written.
func parseNoticeCells(line string) (noticeCells, error) {
	cells := tableCells(line)
	if len(cells) != noticeColumns {
		return noticeCells{}, fmt.Errorf("row %q has %d cells; a component row holds %d: name, version, license, copyright", line, len(cells), noticeColumns)
	}
	for i := range cells {
		cells[i] = strings.TrimSpace(cells[i])
	}
	if cells[3] == "" {
		return noticeCells{}, fmt.Errorf("row %q has an empty copyright cell; state the upstream copyright line, or that none is stated", line)
	}
	clean := func(cell string) string { return strings.Trim(cell, "`") }
	return noticeCells{
		row:      noticeRow{name: clean(cells[0]), version: clean(cells[1]), license: clean(cells[2])},
		nameCell: cells[0], copyright: cells[3],
	}, nil
}

// renderNoticeRows writes one table row for each shipped component of a section, carrying the
// cells no source records from the row listed for that component.
func renderNoticeRows(section string, shipped []noticeRow, listed []noticeCells) ([]string, error) {
	rows := make([]string, 0, len(shipped))
	for _, component := range shipped {
		carried, err := carriedNoticeCells(section, component, listed)
		if err != nil {
			return nil, err
		}
		if !licenseFromSource[section] {
			component.license = carried.row.license
		}
		if err := checkNoticeLicense(section, component); err != nil {
			return nil, err
		}
		rows = append(rows, "| "+carried.nameCell+" | "+component.version+" | "+component.license+" | "+carried.copyright+" |")
	}
	return rows, nil
}

// carriedNoticeCells returns the listed row a shipped component takes its copyright line from:
// the row for its exact version, or else the rows for its other versions when they agree on
// the copyright line (and on the license, where the source records none). A component with no
// row at all, or with disagreeing rows, needs a person to read its upstream license file.
func carriedNoticeCells(section string, component noticeRow, listed []noticeCells) (noticeCells, error) {
	var named []noticeCells
	for _, cells := range listed {
		if cells.row.name != component.name {
			continue
		}
		if cells.row.version == component.version {
			return cells, nil
		}
		named = append(named, cells)
	}
	if len(named) == 0 {
		return noticeCells{}, fmt.Errorf("%s: %s ships and %s holds no row for %s to take its copyright line from; "+
			"add its row by hand with the copyright line its upstream license file states, then run `%s`",
			section, component, NoticesFile, component.name, noticeRegenerateHint)
	}
	disagree := slices.ContainsFunc(named[1:], func(cells noticeCells) bool {
		return cells.copyright != named[0].copyright || (!licenseFromSource[section] && cells.row.license != named[0].row.license)
	})
	if disagree {
		return noticeCells{}, fmt.Errorf("%s: %s ships and the rows %s holds for its other versions disagree on the copyright line or license; "+
			"keep one row for %s with the terms of the version that ships, then run `%s`",
			section, component, NoticesFile, component.name, noticeRegenerateHint)
	}
	return named[0], nil
}

// checkNoticeLicense fails when a component states no license or names an SPDX identifier
// outside knownNoticeLicenses.
func checkNoticeLicense(section string, component noticeRow) error {
	if strings.TrimSpace(component.license) == "" {
		return fmt.Errorf("%s: %s records no license; the notices cannot state its terms", section, component)
	}
	if unknown := unknownLicenseTerms(component.license); len(unknown) > 0 {
		return fmt.Errorf("%s: %s names %s, outside the reviewed licenses; review its terms, then add the identifier to knownNoticeLicenses in internal/supplychain/notices.go",
			section, component, strings.Join(unknown, ", "))
	}
	return nil
}

// unknownLicenseTerms returns each identifier of an SPDX license expression that
// knownNoticeLicenses does not hold.
func unknownLicenseTerms(expression string) []string {
	var unknown []string
	for _, term := range licenseTerms(expression) {
		if !knownNoticeLicenses[term] {
			unknown = append(unknown, term)
		}
	}
	return unknown
}

// licenseTerms returns the identifiers of an SPDX license expression in order; AND, OR, WITH,
// parentheses and whitespace are grammar.
func licenseTerms(expression string) []string {
	fields := strings.FieldsFunc(expression, func(r rune) bool { return r == '(' || r == ')' || unicode.IsSpace(r) })
	return slices.DeleteFunc(fields, func(term string) bool { return licenseOperators[term] })
}

// spliceTables returns lines with each span replaced by its rows, joined with LF. Spans do not
// overlap.
func spliceTables(lines []string, spans []tableSpan) string {
	ordered := slices.Clone(spans)
	slices.SortFunc(ordered, func(a, b tableSpan) int { return a.start - b.start })
	out := make([]string, 0, len(lines))
	next := 0
	for _, span := range ordered {
		out = append(out, lines[next:span.start]...)
		out = append(out, span.rows...)
		next = span.end
	}
	return strings.Join(append(out, lines[next:]...), "\n")
}
