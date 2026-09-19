package state

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

type bugRow struct {
	bug        BugEntry
	form       bugRowForm
	start, end int
}

type bugDocument struct {
	text                     string
	rows                     []bugRow
	insert                   int
	newline                  string
	header, table, separator bool
	fence                    markdownFence
	seen                     map[string]bool
	// meta is the sidecar index v2 rows read from. Writers update it in place;
	// it is persisted with the document.
	meta ledgerMetaIndex
}

// parseBugDocument validates a whole ledger. index is the sidecar content, or
// nil when there is none; a v2 row without an index entry is an error.
func parseBugDocument(text string, index ledgerMetaIndex) (*bugDocument, error) {
	if len(text) > maxLedgerBytes || !utf8.ValidString(text) || strings.ContainsRune(text, 0) {
		return nil, fmt.Errorf("bug ledger must be UTF-8 without NUL, at most %d bytes", maxLedgerBytes)
	}
	doc := &bugDocument{text: text, newline: "\n", seen: make(map[string]bool), meta: index}
	if strings.Contains(text, "\r\n") {
		doc.newline = "\r\n"
	}
	start := 0
	for lineNo, line := range strings.SplitAfter(text, "\n") {
		if lineNo >= maxScannedLines {
			return nil, fmt.Errorf("bug ledger line bound exceeded")
		}
		body := strings.TrimSuffix(strings.TrimSuffix(line, "\n"), "\r")
		if err := doc.readLine(body, start, start+len(body)); err != nil {
			return nil, fmt.Errorf("BUGS.md line %d: %w", lineNo+1, err)
		}
		start += len(line)
	}
	if !doc.header || doc.separator {
		return nil, fmt.Errorf("BUGS.md requires a complete six-column ledger header")
	}
	return doc, nil
}

// skipFence advances the shared fence tracker and additionally closes any open
// table, because a fence always terminates the bug table that preceded it.
func (doc *bugDocument) skipFence(line string) bool {
	opening := !doc.fence.open()
	if !doc.fence.inside(line) {
		return false
	}
	if opening {
		doc.table = false
	}
	return true
}

func (doc *bugDocument) readLine(body string, start, end int) error {
	line := strings.TrimSpace(body)
	if doc.skipFence(line) {
		return nil
	}
	if isBugHeader(line) {
		if doc.header {
			return fmt.Errorf("multiple bug tables are ambiguous")
		}
		doc.header, doc.table, doc.separator = true, true, true
		return nil
	}
	if doc.separator {
		if !isBugSeparator(line) {
			return fmt.Errorf("invalid bug table separator")
		}
		doc.separator, doc.insert = false, end
		return nil
	}
	if !doc.table {
		if claimsLedgerRow(line, bugIDPrefix) {
			return fmt.Errorf("bug row outside ledger table")
		}
		return nil
	}
	if !strings.HasPrefix(line, "|") {
		if claimsLedgerRow(line, bugIDPrefix) {
			return fmt.Errorf("malformed bug row outside table syntax")
		}
		doc.table = false
		return nil
	}
	return doc.addRow(body, start, end)
}

func (doc *bugDocument) addRow(body string, start, end int) error {
	bug, form, err := decodeBugRow(body, doc.meta)
	if err != nil {
		return err
	}
	if doc.seen[bug.ID] {
		return fmt.Errorf("duplicate bug ID %s", bug.ID)
	}
	if len(doc.rows) >= maxLedgerEntries {
		return fmt.Errorf("bug count exceeds %d", maxLedgerEntries)
	}
	doc.seen[bug.ID] = true
	doc.rows = append(doc.rows, bugRow{bug, form, start, end})
	doc.insert = end
	return nil
}

func isBugHeader(line string) bool {
	parts := strings.Split(line, "|")
	if len(parts) != 8 {
		return false
	}
	want := []string{"", "ID", "Title", "Severity", "Status", "Location", "Resolution", ""}
	for i := range parts {
		if strings.TrimSpace(parts[i]) != want[i] {
			return false
		}
	}
	return true
}

func isBugSeparator(line string) bool {
	parts := strings.Split(line, "|")
	if len(parts) != 8 || strings.TrimSpace(parts[0])+strings.TrimSpace(parts[7]) != "" {
		return false
	}
	for _, part := range parts[1:7] {
		cell := strings.TrimSpace(part)
		if strings.Count(cell, "-") < 3 || strings.Trim(cell, ":-") != "" {
			return false
		}
	}
	return true
}

func (doc *bugDocument) insertRow(row string) string {
	// Insert before the existing end-of-line; its exact bytes and all trailing
	// prose remain untouched, including a missing final newline.
	return doc.text[:doc.insert] + doc.newline + row + doc.text[doc.insert:]
}
