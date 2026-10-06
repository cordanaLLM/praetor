// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package supplychain

import (
	"fmt"
	"strings"
)

// The header rows of the credits page tables: the Adapted work table names the praetor artifact
// and what changed; every other table names the use and the kind and relation.
var (
	adaptedTableHeader = []string{
		"| Project | Praetor artifact | Relation | What changed | License |",
		"| :-- | :-- | :-- | :-- | :-- |",
	}
	creditTableHeader = []string{
		"| Project | Use | Kind and relation | License |",
		"| :-- | :-- | :-- | :-- |",
	}
)

// RenderCreditsPage returns page, the credits page (AcknowledgementsFile), with the table under
// each section heading of creditSections replaced by the table rendered from credits: one row per
// entry of that section, in list order. Every other line, the hand-written prose included, is
// returned unchanged, with LF line endings. A section heading that is missing or holds no table,
// a second table under one heading, and a section no entry is listed in are errors.
func RenderCreditsPage(page string, credits Credits) (string, error) {
	lines, err := splitNoticeLines(page)
	if err != nil {
		return "", fmt.Errorf("parse %s: %w", AcknowledgementsFile, err)
	}
	headings := make([]string, 0, len(creditSections))
	for _, section := range creditSections {
		headings = append(headings, section.heading)
	}
	tables, err := locateTables(AcknowledgementsFile, lines, headings)
	if err != nil {
		return "", err
	}
	spans := make([]tableSpan, 0, len(creditSections))
	for _, section := range creditSections {
		rows, err := renderCreditTable(section, credits.Entries)
		if err != nil {
			return "", err
		}
		table := tables[section.heading]
		spans = append(spans, tableSpan{start: table.headerIndex, end: table.end, rows: rows})
	}
	return spliceTables(lines, spans), nil
}

// renderCreditTable returns the header and the rows of one section's table.
func renderCreditTable(section creditSection, entries []CreditEntry) ([]string, error) {
	header := creditTableHeader
	if section.id == sectionAdapted {
		header = adaptedTableHeader
	}
	rows := append([]string(nil), header...)
	for _, entry := range entries {
		if entry.Section == section.id {
			rows = append(rows, creditRow(entry))
		}
	}
	if len(rows) == len(header) {
		return nil, fmt.Errorf("%s lists no entry in section %s; the %q table of %s would be empty",
			CreditsFile, section.id, section.heading, AcknowledgementsFile)
	}
	return rows, nil
}

// creditRow renders one entry as a table row: the linked project and its detail, then the
// artifact, relation and what changed (Adapted work) or the use and the kind and relation, then
// the license and its notice.
func creditRow(entry CreditEntry) string {
	project := "[" + entry.Name + "](" + entry.URL + ")"
	if entry.Detail != "" {
		project += " " + entry.Detail
	}
	license := entry.License
	if entry.Notice != "" {
		license += ", " + entry.Notice
	}
	cells := []string{project, entry.Use, entry.Kind + ", " + entry.Relation, license}
	if entry.Section == sectionAdapted {
		cells = []string{project, entry.Artifact, entry.Relation, entry.Use, license}
	}
	return "| " + strings.Join(cells, " | ") + " |"
}
