package state

import (
	"encoding/base64"
	"encoding/json/v2"
	"fmt"
	"html"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	maxBugEntries     = 10000
	maxBugFieldBytes  = 16384
	maxBugLedgerBytes = 1 << 20
	bugTableHeader    = "| ID | Title | Severity | Status | Location | Resolution |"
	bugTableSeparator = "| :--- | :--- | :--- | :--- | :--- | :--- |"
	bugMetadataPrefix = "<!-- praetor-bug:v1 "
)

type bugMetadata struct {
	Context    string    `json:"context"`
	CreatedAt  time.Time `json:"created_at"`
	ResolvedAt time.Time `json:"resolved_at"`
}

func bugNumber(id string) (int, error) {
	if !strings.HasPrefix(id, "BUG-") {
		return 0, fmt.Errorf("invalid bug ID")
	}
	n, err := strconv.Atoi(strings.TrimPrefix(id, "BUG-"))
	if err != nil || n < 1 || n > 999999999 || id != fmt.Sprintf("BUG-%03d", n) {
		return 0, fmt.Errorf("noncanonical or out-of-range bug ID %q", id)
	}
	return n, nil
}

func nextBugID(rows []bugRow) (string, error) {
	maxID := 0
	for _, row := range rows {
		n, err := bugNumber(row.bug.ID)
		if err != nil {
			return "", err
		}
		if n > maxID {
			maxID = n
		}
	}
	if maxID == 999999999 {
		return "", fmt.Errorf("bug ID space exhausted")
	}
	return fmt.Sprintf("BUG-%03d", maxID+1), nil
}

func validateBug(bug BugEntry) error {
	if _, err := bugNumber(bug.ID); err != nil {
		return err
	}
	if strings.TrimSpace(bug.Title) == "" {
		return fmt.Errorf("bug title is required")
	}
	if !slices.Contains([]string{"p0", "p1", "p2", "p3"}, bug.Severity) {
		return fmt.Errorf("invalid bug severity %q", bug.Severity)
	}
	if !slices.Contains([]string{"open", "investigating", "deferred", "resolved"}, bug.Status) {
		return fmt.Errorf("invalid bug status %q", bug.Status)
	}
	for _, field := range []string{bug.Title, bug.Location, bug.Resolution, bug.Context} {
		if len(field) > maxBugFieldBytes || !utf8.ValidString(field) || strings.ContainsRune(field, 0) {
			return fmt.Errorf("bug fields must be UTF-8 without NUL, at most %d bytes", maxBugFieldBytes)
		}
	}
	return nil
}

// Versioned cells encode edge spaces, so trimming ASCII table padding is lossless.
// Legacy cells are never entity-decoded: literal backslashes/entities stay literal.
func encodeBugCell(text string) string {
	text = strings.NewReplacer("|", "&#124;", "\t", "&#9;", "\r", "&#13;", "\n", "&#10;").Replace(html.EscapeString(text))
	left := len(text) - len(strings.TrimLeft(text, " "))
	if left == len(text) {
		return strings.Repeat("&#32;", left)
	}
	right := len(text) - len(strings.TrimRight(text, " "))
	return strings.Repeat("&#32;", left) + text[left:len(text)-right] + strings.Repeat("&#32;", right)
}

func encodeBugRow(bug BugEntry) (string, error) {
	if err := validateBug(bug); err != nil {
		return "", err
	}
	metadata, err := json.Marshal(bugMetadata{bug.Context, bug.CreatedAt, bug.ResolvedAt})
	if err != nil {
		return "", fmt.Errorf("encode bug metadata: %w", err)
	}
	return fmt.Sprintf("| `%s` | %s | %s | %s | %s | %s | %s%s -->",
		bug.ID, encodeBugCell(bug.Title), bug.Severity, bug.Status, encodeBugCell(bug.Location),
		encodeBugCell(bug.Resolution), bugMetadataPrefix, base64.StdEncoding.EncodeToString(metadata)), nil
}

func decodeBugMetadata(suffix string) (*bugMetadata, error) {
	if !strings.HasPrefix(suffix, bugMetadataPrefix) || !strings.HasSuffix(suffix, " -->") {
		return nil, fmt.Errorf("unknown or malformed bug metadata version")
	}
	encoded := strings.TrimSuffix(strings.TrimPrefix(suffix, bugMetadataPrefix), " -->")
	if len(encoded) > base64.StdEncoding.EncodedLen(6*maxBugFieldBytes+512) {
		return nil, fmt.Errorf("bug metadata exceeds bound")
	}
	raw, err := base64.StdEncoding.Strict().DecodeString(encoded)
	if err != nil {
		return nil, fmt.Errorf("invalid bug metadata encoding: %w", err)
	}
	var wire *struct {
		Context    *string    `json:"context"`
		CreatedAt  *time.Time `json:"created_at"`
		ResolvedAt *time.Time `json:"resolved_at"`
	}
	if err := json.Unmarshal(raw, &wire, json.RejectUnknownMembers(true)); err != nil {
		return nil, fmt.Errorf("invalid bug metadata: %w", err)
	}
	if wire == nil || wire.Context == nil || wire.CreatedAt == nil || wire.ResolvedAt == nil {
		return nil, fmt.Errorf("bug metadata requires context, created_at and resolved_at")
	}
	return &bugMetadata{*wire.Context, *wire.CreatedAt, *wire.ResolvedAt}, nil
}

func decodeBugRow(line string) (BugEntry, error) {
	rawParts := strings.Split(line, "|")
	parts := append([]string(nil), rawParts...)
	if len(parts) != 8 || strings.TrimSpace(parts[0]) != "" {
		return BugEntry{}, fmt.Errorf("expected exactly six bug columns")
	}
	for i := range parts {
		parts[i] = strings.TrimSpace(parts[i])
	}
	if !strings.HasPrefix(parts[1], "`") || !strings.HasSuffix(parts[1], "`") || strings.Count(parts[1], "`") != 2 {
		return BugEntry{}, fmt.Errorf("bug ID must be backtick-quoted")
	}
	bug := BugEntry{ID: strings.Trim(parts[1], "`"), Title: parts[2], Severity: strings.ToLower(parts[3]), Status: strings.ToLower(parts[4]), Location: parts[5], Resolution: parts[6]}
	if parts[7] != "" {
		metadata, err := decodeBugMetadata(parts[7])
		if err != nil {
			return BugEntry{}, err
		}
		bug.Title, bug.Location, bug.Resolution = html.UnescapeString(strings.Trim(rawParts[2], " ")), html.UnescapeString(strings.Trim(rawParts[5], " ")), html.UnescapeString(strings.Trim(rawParts[6], " "))
		bug.Context, bug.CreatedAt, bug.ResolvedAt = metadata.Context, metadata.CreatedAt, metadata.ResolvedAt
	}
	return bug, validateBug(bug)
}
