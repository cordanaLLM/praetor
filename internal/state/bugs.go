package state

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// BugEntry captures a defect discovered during active development.
type BugEntry struct {
	ID         string    `json:"id"`
	Title      string    `json:"title"`
	Severity   string    `json:"severity"` // "p0", "p1", "p2", "p3"
	Location   string    `json:"location,omitempty"`
	Context    string    `json:"context,omitempty"`
	Status     string    `json:"status"` // "open", "investigating", "deferred", "resolved"
	Resolution string    `json:"resolution,omitempty"`
	CreatedAt  time.Time `json:"created_at"`
	ResolvedAt time.Time `json:"resolved_at,omitempty"`
}

// AddBug records a bug without rewriting unrelated ledger rows or prose.
func AddBug(rootPath string, bug BugEntry) (*BugEntry, error) {
	if bug.Severity == "" {
		bug.Severity = "p2"
	}
	if bug.Status == "" {
		bug.Status = "open"
	}
	if bug.CreatedAt.IsZero() {
		bug.CreatedAt = time.Now().UTC()
	}
	err := updateBugLedger(rootPath, func(doc *bugDocument) (string, error) {
		id, err := nextBugID(doc.rows)
		if err != nil {
			return "", err
		}
		bug.ID = id
		row, err := encodeBugRow(bug)
		if err != nil {
			return "", err
		}
		doc.meta[bug.ID] = metadataOf(bug)
		return doc.insertRow(row), nil
	})
	if err != nil {
		return nil, err
	}
	return &bug, nil
}

// ListBugs returns validated records. Missing ledgers are empty; malformed or
// unreadable existing ledgers return errors instead of partial results.
func ListBugs(rootPath string, filterStatus string) ([]BugEntry, error) {
	return ListBugsContext(context.Background(), rootPath, filterStatus)
}

// ListBugsContext preserves caller cancellation and caps file I/O at ten seconds.
func ListBugsContext(ctx context.Context, rootPath, filterStatus string) ([]BugEntry, error) {
	if ctx == nil {
		return nil, fmt.Errorf("bug ledger read requires context")
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	files, err := readBugFiles(ctx, rootPath)
	if err != nil {
		return nil, err
	}
	doc, err := parseBugFiles(files)
	if err != nil {
		return nil, err
	}
	all := doc.entries()
	if filterStatus == "" || filterStatus == "all" {
		return all, nil
	}
	filtered := make([]BugEntry, 0, len(all))
	for _, bug := range all {
		if strings.EqualFold(bug.Status, filterStatus) {
			filtered = append(filtered, bug)
		}
	}
	return filtered, nil
}

// ResolveBug updates only the selected record, preserving all other bytes.
func ResolveBug(rootPath, id, resolution string) error {
	return updateBugLedger(rootPath, func(doc *bugDocument) (string, error) {
		for _, row := range doc.rows {
			if !strings.EqualFold(row.bug.ID, id) {
				continue
			}
			bug := row.bug
			bug.Status, bug.Resolution, bug.ResolvedAt = "resolved", resolution, time.Now().UTC()
			encoded, err := encodeBugRow(bug)
			if err != nil {
				return "", err
			}
			doc.meta[bug.ID] = metadataOf(bug)
			return doc.text[:row.start] + encoded + doc.text[row.end:], nil
		}
		return "", fmt.Errorf("bug with ID %q not found", id)
	})
}

// ParseBugsMarkdown extracts a complete validated ledger.
// Deprecated: use ParseBugsMarkdownStrict to distinguish invalid from empty.
// Invalid documents yield nil; no partial record set is returned.
func ParseBugsMarkdown(md string) []BugEntry {
	bugs, err := ParseBugsMarkdownStrict(md)
	if err != nil {
		return nil
	}
	return bugs
}

// ParseBugsMarkdownStrict validates the entire ledger before returning records.
// It reads one self-contained document: a row whose metadata lives in the
// bugs.meta.json sidecar is an error here, because the sidecar is not given.
// ListBugsContext reads a ledger directory together with its sidecar.
func ParseBugsMarkdownStrict(md string) ([]BugEntry, error) {
	doc, err := parseBugDocument(md, nil)
	if err != nil {
		return nil, err
	}
	return doc.entries(), nil
}

func (doc *bugDocument) entries() []BugEntry {
	bugs := make([]BugEntry, 0, len(doc.rows))
	for _, row := range doc.rows {
		bugs = append(bugs, row.bug)
	}
	return bugs
}

// RenderBugsMarkdown retains its legacy signature. Invalid records produce an
// explicitly invalid document rather than an apparently empty valid ledger.
// Use RenderBugsMarkdownStrict when the caller needs the validation error.
func RenderBugsMarkdown(bugs []BugEntry) string {
	md, err := RenderBugsMarkdownStrict(bugs)
	if err != nil {
		return "# Bug Ledger\n\nINVALID LEDGER: record validation failed\n"
	}
	return md
}

// RenderBugsMarkdownStrict writes all BugEntry fields into one self-contained
// document, metadata inline in the v1 form. Ledger writers (AddBug, ResolveBug)
// keep metadata in the bugs.meta.json sidecar instead.
func RenderBugsMarkdownStrict(bugs []BugEntry) (string, error) {
	if len(bugs) > maxBugEntries {
		return "", fmt.Errorf("bug count exceeds %d", maxBugEntries)
	}
	var out strings.Builder
	out.WriteString(defaultBugsMD())
	for _, bug := range bugs {
		row, err := encodeInlineBugRow(bug)
		if err != nil {
			return "", err
		}
		out.WriteString(row + "\n")
	}
	result := out.String()
	if _, err := parseBugDocument(result, nil); err != nil {
		return "", err
	}
	return result, nil
}

func defaultBugsMD() string {
	return "# Bug Ledger\n\n> Discoveries made while coding that should not distract from the active task.\n\n" + bugTableHeader + "\n" + bugTableSeparator + "\n"
}
