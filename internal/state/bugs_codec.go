package state

import (
	"encoding/base64"
	"encoding/json/v2"
	"fmt"
	"slices"
	"strings"
)

const (
	bugIDPrefix       = "BUG-"
	bugTableHeader    = "| ID | Title | Severity | Status | Location | Resolution |"
	bugTableSeparator = "| :--- | :--- | :--- | :--- | :--- | :--- |"
	bugMetadataPrefix = "<!-- praetor-bug:v1 "
	// bugMetadataRef marks a row whose metadata lives in the sidecar under its ID.
	bugMetadataRef = "<!-- praetor-bug:v2 -->"
)

// bugRowForm records how a row carries its metadata.
type bugRowForm int

const (
	// bugRowLegacy has no metadata; its cells are read literally.
	bugRowLegacy bugRowForm = iota
	// bugRowInline carries Base64 metadata in a v1 comment.
	bugRowInline
	// bugRowSidecar points at its metadata in the sidecar with a v2 comment.
	bugRowSidecar
)

func metadataOf(bug BugEntry) ledgerMetadata {
	return ledgerMetadata{bug.Context, bug.CreatedAt, bug.ResolvedAt}
}

func bugNumber(id string) (int, error) {
	return ledgerIDNumber(bugIDPrefix, id)
}

func checkBugID(id string) error {
	_, err := bugNumber(id)
	return err
}

func nextBugID(rows []bugRow) (string, error) {
	ids := make([]string, 0, len(rows))
	for _, row := range rows {
		ids = append(ids, row.bug.ID)
	}
	return nextLedgerID(bugIDPrefix, ids)
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
		if err := validateLedgerText(field); err != nil {
			return err
		}
	}
	return nil
}

// encodeBugCells writes the six visible cells and the separator before the
// metadata comment.
func encodeBugCells(bug BugEntry) (string, error) {
	if err := validateBug(bug); err != nil {
		return "", err
	}
	return fmt.Sprintf("| `%s` | %s | %s | %s | %s | %s | ",
		bug.ID, encodeLedgerCell(bug.Title), bug.Severity, bug.Status, encodeLedgerCell(bug.Location),
		encodeLedgerCell(bug.Resolution)), nil
}

// encodeBugRow writes the sidecar form. The caller stores metadataOf(bug) in
// bugs.meta.json under bug.ID.
func encodeBugRow(bug BugEntry) (string, error) {
	cells, err := encodeBugCells(bug)
	if err != nil {
		return "", err
	}
	return cells + bugMetadataRef, nil
}

// encodeInlineBugRow writes the self-contained v1 form with Base64 metadata.
// Only whole-document rendering uses it; ledger writers use the sidecar form.
func encodeInlineBugRow(bug BugEntry) (string, error) {
	cells, err := encodeBugCells(bug)
	if err != nil {
		return "", err
	}
	metadata, err := json.Marshal(metadataOf(bug))
	if err != nil {
		return "", fmt.Errorf("encode bug metadata: %w", err)
	}
	return cells + bugMetadataPrefix + base64.StdEncoding.EncodeToString(metadata) + " -->", nil
}

// rowMetadata resolves a row's metadata comment: the v2 reference reads the
// sidecar index, anything else must be a valid v1 comment.
func rowMetadata(id, suffix string, index ledgerMetaIndex) (*ledgerMetadata, bugRowForm, error) {
	if suffix != bugMetadataRef {
		metadata, err := decodeBugMetadata(suffix)
		return metadata, bugRowInline, err
	}
	metadata, ok := index[id]
	if !ok {
		return nil, bugRowSidecar, fmt.Errorf("%s metadata missing from %s", id, bugMetaName)
	}
	return &metadata, bugRowSidecar, nil
}

func decodeBugMetadata(suffix string) (*ledgerMetadata, error) {
	if !strings.HasPrefix(suffix, bugMetadataPrefix) || !strings.HasSuffix(suffix, " -->") {
		return nil, fmt.Errorf("unknown or malformed bug metadata version")
	}
	encoded := strings.TrimSuffix(strings.TrimPrefix(suffix, bugMetadataPrefix), " -->")
	if len(encoded) > base64.StdEncoding.EncodedLen(6*maxLedgerFieldBytes+512) {
		return nil, fmt.Errorf("bug metadata exceeds bound")
	}
	raw, err := base64.StdEncoding.Strict().DecodeString(encoded)
	if err != nil {
		return nil, fmt.Errorf("invalid bug metadata encoding: %w", err)
	}
	return decodeLedgerMetadataJSON(raw)
}

// decodeBugRow reads one table row. A v2 row takes its metadata from index,
// which is nil when the caller has no sidecar.
func decodeBugRow(line string, index ledgerMetaIndex) (BugEntry, bugRowForm, error) {
	rawParts := strings.Split(line, "|")
	parts := append([]string(nil), rawParts...)
	if len(parts) != 8 || strings.TrimSpace(parts[0]) != "" {
		return BugEntry{}, bugRowLegacy, fmt.Errorf("expected exactly six bug columns")
	}
	for i := range parts {
		parts[i] = strings.TrimSpace(parts[i])
	}
	id, err := decodeLedgerIDCell(parts[1])
	if err != nil {
		return BugEntry{}, bugRowLegacy, err
	}
	bug := BugEntry{ID: id, Title: parts[2], Severity: strings.ToLower(parts[3]), Status: strings.ToLower(parts[4]), Location: parts[5], Resolution: parts[6]}
	if parts[7] == "" {
		return bug, bugRowLegacy, validateBug(bug)
	}
	metadata, form, err := rowMetadata(bug.ID, parts[7], index)
	if err != nil {
		return BugEntry{}, form, err
	}
	bug.Title, bug.Location, bug.Resolution = decodeLedgerCell(rawParts[2]), decodeLedgerCell(rawParts[5]), decodeLedgerCell(rawParts[6])
	bug.Context, bug.CreatedAt, bug.ResolvedAt = metadata.Context, metadata.CreatedAt, metadata.ResolvedAt
	return bug, form, validateBug(bug)
}
