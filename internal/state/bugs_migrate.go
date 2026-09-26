package state

import (
	"bytes"
	"context"
	"encoding/json/v2"
	"errors"
	"fmt"
	"strings"
)

// BugMigrationReport describes one bug metadata migration.
type BugMigrationReport struct {
	// Rows counts every ledger row; Migrated the inline rows moved to the sidecar.
	Rows         int  `json:"rows"`
	Migrated     int  `json:"migrated"`
	BytesBefore  int  `json:"bytes_before"`
	BytesAfter   int  `json:"bytes_after"`
	SidecarBytes int  `json:"sidecar_bytes"`
	Changed      bool `json:"changed"`
}

var errNothingToMigrate = errors.New("no inline bug metadata to migrate")

// MigrateBugMetadata moves every inline v1 metadata comment in BUGS.md into
// the bugs.meta.json sidecar, keyed by bug ID, and resynchronizes the ledger.
// Before writing it re-reads the migrated ledger with its sidecar and requires
// every record, metadata included, to equal the original; any difference
// refuses the migration with both files unchanged. It refuses a ledger whose
// last sync no longer verifies. Legacy rows carry no metadata and stay as
// written. A ledger without inline rows is left untouched, so a second run is a
// no-op.
func MigrateBugMetadata(ctx context.Context, rootPath string) (*BugMigrationReport, error) {
	return migrateBugMetadata(ctx, rootPath, encodeBugRow)
}

// migrateBugMetadata takes the row encoder as a parameter so tests can prove a
// faulty encoding is refused before anything is written.
func migrateBugMetadata(ctx context.Context, rootPath string, encode func(BugEntry) (string, error)) (*BugMigrationReport, error) {
	if ctx == nil {
		return nil, errors.New("bug metadata migration requires a context")
	}
	if err := VerifyStateSync(ctx, rootPath); err != nil {
		return nil, fmt.Errorf("state migrate-bugs refuses an unverified ledger: %w", err)
	}
	report := &BugMigrationReport{}
	err := updateBugLedgerContext(ctx, rootPath, func(doc *bugDocument) (string, error) {
		return migrateBugDocument(doc, report, encode)
	})
	if errors.Is(err, errNothingToMigrate) {
		return report, nil
	}
	if err != nil {
		return nil, fmt.Errorf("migrate bug metadata: %w", err)
	}
	report.Changed = true
	summary := fmt.Sprintf("migrate-bugs %d rows to %s", report.Migrated, bugMetaName)
	if _, err := SyncState(ctx, rootPath, summary); err != nil {
		return report, fmt.Errorf("resync after bug metadata migration: %w", err)
	}
	return report, nil
}

// migrateBugDocument rewrites each inline row into the sidecar form, records
// its metadata in doc.meta and verifies the round trip before returning.
func migrateBugDocument(doc *bugDocument, report *BugMigrationReport, encode func(BugEntry) (string, error)) (string, error) {
	report.Rows, report.BytesBefore, report.BytesAfter = len(doc.rows), len(doc.text), len(doc.text)
	before := doc.entries()
	var out strings.Builder
	last := 0
	for _, row := range doc.rows {
		if row.form != bugRowInline {
			continue
		}
		encoded, err := encode(row.bug)
		if err != nil {
			return "", fmt.Errorf("%s: %w", row.bug.ID, err)
		}
		out.WriteString(doc.text[last:row.start])
		out.WriteString(encoded)
		last = row.end
		doc.meta[row.bug.ID] = metadataOf(row.bug)
		report.Migrated++
	}
	if report.Migrated == 0 {
		return "", errNothingToMigrate
	}
	out.WriteString(doc.text[last:])
	updated := out.String()
	sidecar, err := encodeBugMeta(doc.meta)
	if err != nil {
		return "", err
	}
	report.BytesAfter, report.SidecarBytes = len(updated), len(sidecar)
	return updated, verifyBugRoundTrip(before, updated, doc.meta)
}

// verifyBugRoundTrip re-reads the migrated ledger with its sidecar and
// compares every record field by field, in order.
func verifyBugRoundTrip(before []BugEntry, updated string, index ledgerMetaIndex) error {
	doc, err := parseBugDocument(updated, index)
	if err != nil {
		return fmt.Errorf("round trip: migrated ledger does not parse: %w", err)
	}
	after := doc.entries()
	if len(after) != len(before) {
		return fmt.Errorf("round trip: %d records before, %d after", len(before), len(after))
	}
	for i := range before {
		if err := sameBugRecord(before[i], after[i]); err != nil {
			return err
		}
	}
	return nil
}

// sameBugRecord compares canonical encodings, so time instants, offsets and
// nanoseconds must match exactly, not merely denote the same moment.
func sameBugRecord(want, got BugEntry) error {
	wantJSON, err := json.Marshal(want, json.Deterministic(true))
	if err != nil {
		return fmt.Errorf("round trip: encode %s: %w", want.ID, err)
	}
	gotJSON, err := json.Marshal(got, json.Deterministic(true))
	if err != nil {
		return fmt.Errorf("round trip: encode %s: %w", got.ID, err)
	}
	if !bytes.Equal(wantJSON, gotJSON) {
		return fmt.Errorf("round trip: record %s changed", want.ID)
	}
	return nil
}
