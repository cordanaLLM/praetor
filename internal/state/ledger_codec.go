package state

import (
	"encoding/json/v2"
	"fmt"
	"html"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// The table ledgers under .workingdir (BUGS.md, QUESTIONS.md) share one cell codec, one
// text bound and one metadata record shape. Each ledger declares only what differs: its ID
// prefix, its columns and its row marker.
const (
	maxLedgerEntries    = 10000
	maxLedgerFieldBytes = 16384
	maxLedgerBytes      = 1 << 20
	maxLedgerIDNumber   = 999999999
)

// ledgerMetadata is one record's sidecar payload. ResolvedAt is when the record was closed:
// a bug's resolution, a question's decision.
type ledgerMetadata struct {
	Context    string    `json:"context"`
	CreatedAt  time.Time `json:"created_at"`
	ResolvedAt time.Time `json:"resolved_at"`
}

// ledgerIDNumber parses a canonical ledger ID such as BUG-007 or Q-012.
func ledgerIDNumber(prefix, id string) (int, error) {
	if !strings.HasPrefix(id, prefix) {
		return 0, fmt.Errorf("invalid ledger ID %q: want prefix %s", id, prefix)
	}
	n, err := strconv.Atoi(strings.TrimPrefix(id, prefix))
	if err != nil || n < 1 || n > maxLedgerIDNumber || id != fmt.Sprintf("%s%03d", prefix, n) {
		return 0, fmt.Errorf("noncanonical or out-of-range ledger ID %q", id)
	}
	return n, nil
}

// nextLedgerID returns the ID after the largest in ids. Counting rows instead reuses the ID
// of any row that is gone, so two records would answer to one ID.
func nextLedgerID(prefix string, ids []string) (string, error) {
	maxID := 0
	for _, id := range ids {
		n, err := ledgerIDNumber(prefix, id)
		if err != nil {
			return "", err
		}
		maxID = max(maxID, n)
	}
	if maxID == maxLedgerIDNumber {
		return "", fmt.Errorf("%s ID space exhausted", prefix)
	}
	return fmt.Sprintf("%s%03d", prefix, maxID+1), nil
}

func validateLedgerText(field string) error {
	if len(field) > maxLedgerFieldBytes || !utf8.ValidString(field) || strings.ContainsRune(field, 0) {
		return fmt.Errorf("ledger fields must be UTF-8 without NUL, at most %d bytes", maxLedgerFieldBytes)
	}
	return nil
}

// encodeLedgerCell writes text as one table cell. Versioned cells encode edge spaces, so
// trimming ASCII table padding is lossless, and '|', tabs and line breaks become entities,
// so no field can end a cell or a row. Legacy cells are never entity-decoded: literal
// backslashes and entities stay literal.
func encodeLedgerCell(text string) string {
	text = strings.NewReplacer("|", "&#124;", "\t", "&#9;", "\r", "&#13;", "\n", "&#10;").Replace(html.EscapeString(text))
	left := len(text) - len(strings.TrimLeft(text, " "))
	if left == len(text) {
		return strings.Repeat("&#32;", left)
	}
	right := len(text) - len(strings.TrimRight(text, " "))
	return strings.Repeat("&#32;", left) + text[left:len(text)-right] + strings.Repeat("&#32;", right)
}

// decodeLedgerCell reverses encodeLedgerCell for one raw cell, table padding included.
func decodeLedgerCell(raw string) string {
	return html.UnescapeString(strings.Trim(raw, " "))
}

// decodeLedgerIDCell reads a backtick-quoted ID cell.
func decodeLedgerIDCell(cell string) (string, error) {
	cell = strings.TrimSpace(cell)
	if !strings.HasPrefix(cell, "`") || !strings.HasSuffix(cell, "`") || strings.Count(cell, "`") != 2 {
		return "", fmt.Errorf("ledger ID must be backtick-quoted")
	}
	return strings.Trim(cell, "`"), nil
}

// claimsLedgerRow reports whether a line presents itself as a row of the ledger whose IDs
// start with prefix, so a row that fails to decode is an error rather than skipped prose.
func claimsLedgerRow(line, prefix string) bool {
	if !strings.Contains(line, "|") {
		return false
	}
	cell, _, _ := strings.Cut(strings.TrimPrefix(line, "|"), "|")
	return strings.HasPrefix(strings.Trim(strings.TrimSpace(cell), "`"), prefix)
}

// decodeLedgerMetadataJSON is the one strict metadata reader for every ledger and form:
// unknown, missing, null, duplicate and case-alias fields are rejected.
func decodeLedgerMetadataJSON(raw []byte) (*ledgerMetadata, error) {
	var wire *struct {
		Context    *string    `json:"context"`
		CreatedAt  *time.Time `json:"created_at"`
		ResolvedAt *time.Time `json:"resolved_at"`
	}
	if err := json.Unmarshal(raw, &wire, json.RejectUnknownMembers(true)); err != nil {
		return nil, fmt.Errorf("invalid ledger metadata: %w", err)
	}
	if wire == nil || wire.Context == nil || wire.CreatedAt == nil || wire.ResolvedAt == nil {
		return nil, fmt.Errorf("ledger metadata requires context, created_at and resolved_at")
	}
	return &ledgerMetadata{*wire.Context, *wire.CreatedAt, *wire.ResolvedAt}, nil
}
