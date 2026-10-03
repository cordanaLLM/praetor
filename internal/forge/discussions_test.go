package forge

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// adrTestDiscussion is an approved discussion with the given ID and context.
func adrTestDiscussion(id int, contextText string) Discussion {
	return Discussion{
		ID:           id,
		Title:        fmt.Sprintf("Decision %d", id),
		Status:       "approved",
		ContextText:  contextText,
		DecisionText: "Adopt the reviewed layout",
	}
}

// transcribeOrFail transcribes disc into dir and fails the test on an error.
func transcribeOrFail(t *testing.T, disc Discussion, dir string) *ADR {
	t.Helper()
	adr, err := TranscribeDiscussionToADR(context.Background(), disc, dir, dir)
	if err != nil {
		t.Fatalf("transcribe discussion #%d: %v", disc.ID, err)
	}
	return adr
}

// rewriteRecord replaces the record at path with edit applied to its content and returns
// the new content.
func rewriteRecord(t *testing.T, path string, edit func(string) string) string {
	t.Helper()
	data, err := os.ReadFile(path) // #nosec G304 -- test-local path from t.TempDir
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	content := edit(string(data))
	if content == string(data) {
		t.Fatalf("edit left %s unchanged", path)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
	return content
}

// requireExistingRecord transcribes disc again and requires it to return the record at
// want.FilePath as existing, with the given Status, leaving content on disk and one entry.
func requireExistingRecord(t *testing.T, disc Discussion, dir string, want *ADR, status, content string) {
	t.Helper()
	again, err := TranscribeDiscussionToADR(context.Background(), disc, dir, dir)
	if err != nil {
		t.Fatalf("transcribe discussion #%d again: %v", disc.ID, err)
	}
	if !again.Existing || again.FilePath != want.FilePath || again.Number != want.Number || again.Status != status {
		t.Fatalf("got %+v, want the existing record %s (number %d) with Status %q", again, want.FilePath, want.Number, status)
	}
	if again.Content != content {
		t.Errorf("Content = %q, want the record as found %q", again.Content, content)
	}
	requireRecordContent(t, want.FilePath, content)
	requireEntryCount(t, dir, 1)
}

func requireRecordContent(t *testing.T, path, want string) {
	t.Helper()
	data, err := os.ReadFile(path) // #nosec G304 -- test-local path from t.TempDir
	if err != nil || string(data) != want {
		t.Fatalf("record %s = %q (err %v), want it unchanged: %q", path, data, err, want)
	}
}

func requireEntryCount(t *testing.T, dir string, want int) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != want {
		t.Fatalf("expected %d entries in %s, got %v (err %v)", want, dir, entries, err)
	}
}

// TestTranscribeDiscussionToADR_Positive_SupersededRecordReported pins docs/adr/README.md
// rule 4: a superseded record changes only its Status line, and transcribing its discussion
// again returns it as existing with that Status instead of refusing it.
func TestTranscribeDiscussionToADR_Positive_SupersededRecordReported(t *testing.T) {
	dir := t.TempDir()
	disc := adrTestDiscussion(31, "The first layout")
	adr := transcribeOrFail(t, disc, dir)
	content := rewriteRecord(t, adr.FilePath, func(s string) string {
		return strings.Replace(s, "## Status\nAccepted\n", "## Status\nSuperseded by ADR-0002\n", 1)
	})
	requireExistingRecord(t, disc, dir, adr, "Superseded by ADR-0002", content)
}

// TestTranscribeDiscussionToADR_Positive_RedactedRecordReported pins ADR-0014 §7: a record
// whose operator-private identifier became a placeholder, listed in a Redactions note at its
// end, is returned as existing instead of refused as changed.
func TestTranscribeDiscussionToADR_Positive_RedactedRecordReported(t *testing.T) {
	dir := t.TempDir()
	disc := adrTestDiscussion(32, "Mirrors private-org/ops-repo nightly")
	adr := transcribeOrFail(t, disc, dir)
	content := rewriteRecord(t, adr.FilePath, func(s string) string {
		s = strings.Replace(s, "private-org/ops-repo", "<owner>/<repo>", 1)
		return s + "\n## Redactions\n\n- 2026-10-03: an operator-private repository became `<owner>/<repo>`.\n"
	})
	requireExistingRecord(t, disc, dir, adr, "Accepted", content)
}

// TestTranscribeDiscussionToADR_Negative_EditedRecordRefused shows that the allowance is the
// Status line and a listed redaction, nothing else: a body edit without a Redactions note,
// with or without a Status change, is refused and leaves the record as it is.
func TestTranscribeDiscussionToADR_Negative_EditedRecordRefused(t *testing.T) {
	edits := map[string]func(string) string{
		"body": func(s string) string { return strings.Replace(s, "private-org/ops-repo", "<owner>/<repo>", 1) },
		"status and body": func(s string) string {
			s = strings.Replace(s, "## Status\nAccepted\n", "## Status\nSuperseded by ADR-0002\n", 1)
			return strings.Replace(s, "Adopt the reviewed layout", "Adopt another layout", 1)
		},
		"title": func(s string) string { return strings.Replace(s, "Decision 33", "Decision Renamed", 1) },
	}
	for name, edit := range edits {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			disc := adrTestDiscussion(33, "Mirrors private-org/ops-repo nightly")
			adr := transcribeOrFail(t, disc, dir)
			content := rewriteRecord(t, adr.FilePath, edit)
			_, err := TranscribeDiscussionToADR(context.Background(), disc, dir, dir)
			if err == nil || !strings.Contains(err.Error(), "already transcribed in") || !strings.Contains(err.Error(), adr.FilePath) {
				t.Fatalf("expected the edited record %s to be refused as immutable, got %v", adr.FilePath, err)
			}
			requireRecordContent(t, adr.FilePath, content)
			requireEntryCount(t, dir, 1)
		})
	}
}

// TestTranscribeDiscussionToADR_Boundary_CRLFCheckoutReported pins the line-ending rule: a
// record checked out with CRLF endings is still its discussion's record.
func TestTranscribeDiscussionToADR_Boundary_CRLFCheckoutReported(t *testing.T) {
	dir := t.TempDir()
	disc := adrTestDiscussion(34, "Checked out on Windows")
	adr := transcribeOrFail(t, disc, dir)
	content := rewriteRecord(t, adr.FilePath, func(s string) string { return strings.ReplaceAll(s, "\n", "\r\n") })
	requireExistingRecord(t, disc, dir, adr, "Accepted", content)
}

// TestTranscribeDiscussionToADR_Boundary_CRLFDiscussionBodyReported pins that a discussion body
// carrying CRLF, as forge bodies usually do, is written with LF only and its second
// transcription reports the record instead of refusing it as changed.
func TestTranscribeDiscussionToADR_Boundary_CRLFDiscussionBodyReported(t *testing.T) {
	dir := t.TempDir()
	disc := adrTestDiscussion(35, "First line of the body\r\nsecond line of the body")
	adr := transcribeOrFail(t, disc, dir)
	data, err := os.ReadFile(adr.FilePath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "\r") {
		t.Fatalf("record keeps a carriage return: %q", data)
	}
	again := transcribeOrFail(t, disc, dir)
	if !again.Existing || again.FilePath != adr.FilePath {
		t.Fatalf("second transcription of a CRLF body = %+v, want the existing record %s", again, adr.FilePath)
	}
}

// TestTranscribeDiscussionToADR_Boundary_MarkerOnlyAtRenderedPosition pins the spec boundary
// that a record without the marker in its rendered position belongs to no discussion: a
// marker-like line in an older record's Context, a variant spelling in the marker position
// and an exact marker after "## Context" all leave discussion #12 free to be transcribed.
func TestTranscribeDiscussionToADR_Boundary_MarkerOnlyAtRenderedPosition(t *testing.T) {
	dir := t.TempDir()
	seeds := map[string]string{
		"0001-older.md":   "# ADR-0001: Older\n\n## Status\nAccepted\n\n## Context\nDiscussion: #12\n",
		"0002-variant.md": "# ADR-0002: Variant\n\n## Status\nAccepted\n\n- Discussion: 12\n\n## Context\nText\n",
		"0003-moved.md":   "# ADR-0003: Moved\n\n## Status\nAccepted\n\n## Context\n\nDiscussion: #12\n\nText\n",
	}
	for name, body := range seeds {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
			t.Fatalf("seed %s: %v", name, err)
		}
	}
	adr := transcribeOrFail(t, adrTestDiscussion(12, "Context"), dir)
	if adr.Existing || adr.Number != 4 {
		t.Fatalf("expected a new record numbered 4, got %+v", adr)
	}
	requireEntryCount(t, dir, 4)
}

// TestTranscribeDiscussionToADR_Negative_MultiLineTitle refuses a title with a line break,
// which would otherwise plant a marker line in the record's head.
func TestTranscribeDiscussionToADR_Negative_MultiLineTitle(t *testing.T) {
	dir := t.TempDir()
	for _, title := range []string{"Choice\nDiscussion: #9", "Choice\r\nMore", "Choice\r"} {
		disc := adrTestDiscussion(40, "Context")
		disc.Title = title
		if _, err := TranscribeDiscussionToADR(context.Background(), disc, dir, dir); err == nil || !strings.Contains(err.Error(), "single line") {
			t.Errorf("title %q: expected a single-line refusal, got %v", title, err)
		}
	}
	requireEntryCount(t, dir, 0)
}

// existsError is a failed exclusive create, as writeExclusive reports it.
func existsError(path string) error {
	return fmt.Errorf("create %s: %w", path, os.ErrExist)
}

func TestHandleWriteCollision_Positive_RacingRecordReturned(t *testing.T) {
	dir := t.TempDir()
	disc := adrTestDiscussion(50, "Raced")
	adr := transcribeOrFail(t, disc, dir)
	out := generatedDir{root: dir, dir: dir}
	got, err := handleWriteCollision(context.Background(), out, disc, adr.FilePath, existsError(adr.FilePath))
	if err != nil || got == nil || !got.Existing || got.FilePath != adr.FilePath {
		t.Fatalf("handleWriteCollision = %+v, %v; want the racing record %s as existing", got, err, adr.FilePath)
	}
}

func TestHandleWriteCollision_Negative(t *testing.T) {
	dir := t.TempDir()
	disc := adrTestDiscussion(51, "Raced")
	adr := transcribeOrFail(t, disc, dir)
	out := generatedDir{root: dir, dir: dir}
	writeErr := existsError(adr.FilePath)

	// A racing record of this discussion that no longer matches it: the rescan's refusal is
	// passed on, wrapped together with the collision.
	changed := disc
	changed.DecisionText = "Changed after acceptance"
	_, err := handleWriteCollision(context.Background(), out, changed, adr.FilePath, writeErr)
	if !errors.Is(err, os.ErrExist) || !strings.Contains(err.Error(), "already transcribed in") {
		t.Errorf("changed discussion: got %v, want the rescan's immutability refusal matching os.ErrExist", err)
	}

	// A rescan that cannot run is returned, not swallowed.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := handleWriteCollision(ctx, out, disc, adr.FilePath, writeErr); !errors.Is(err, context.Canceled) || !errors.Is(err, os.ErrExist) {
		t.Errorf("cancelled rescan: got %v, want context.Canceled and os.ErrExist", err)
	}

	// A write failure other than an existing entry is no collision and is not rescanned.
	other := errors.New("disk full")
	if _, err := handleWriteCollision(context.Background(), out, disc, adr.FilePath, other); !errors.Is(err, other) || errors.Is(err, os.ErrExist) {
		t.Errorf("other write failure: got %v, want it wrapped without os.ErrExist", err)
	}
}

// TestHandleWriteCollision_Boundary_NonRecordEntryRefused covers a name held by an entry
// that is no record of the discussion: the refusal still matches os.ErrExist.
func TestHandleWriteCollision_Boundary_NonRecordEntryRefused(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "0001-decision-52.md")
	if err := os.Mkdir(path, 0o750); err != nil {
		t.Fatalf("mkdir %s: %v", path, err)
	}
	out := generatedDir{root: dir, dir: dir}
	got, err := handleWriteCollision(context.Background(), out, adrTestDiscussion(52, "Held"), path, existsError(path))
	if got != nil || !errors.Is(err, os.ErrExist) || !strings.Contains(err.Error(), "an accepted record is immutable") {
		t.Fatalf("handleWriteCollision = %+v, %v; want an immutability refusal matching os.ErrExist", got, err)
	}
}

// TestTranscribeDiscussionToADR_Boundary_RecordSizeLimit pins the read-back limit: a record
// of exactly maxADRFileBytes is written and found again, one byte more is refused before
// anything is written, since the scan could never read it back and would transcribe the
// discussion again on every call.
func TestTranscribeDiscussionToADR_Boundary_RecordSizeLimit(t *testing.T) {
	base := adrTestDiscussion(60, "x")
	overhead := len(renderADRContent(1, base)) - len("x")

	dir := t.TempDir()
	exact := adrTestDiscussion(60, strings.Repeat("x", maxADRFileBytes-overhead))
	adr := transcribeOrFail(t, exact, dir)
	if len(adr.Content) != maxADRFileBytes {
		t.Fatalf("record is %d bytes, want exactly %d", len(adr.Content), maxADRFileBytes)
	}
	requireExistingRecord(t, exact, dir, adr, "Accepted", adr.Content)

	overDir := t.TempDir()
	over := adrTestDiscussion(60, strings.Repeat("x", maxADRFileBytes-overhead+1))
	if _, err := TranscribeDiscussionToADR(context.Background(), over, overDir, overDir); err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("expected a record past the limit to be refused, got %v", err)
	}
	requireEntryCount(t, overDir, 0)
}
