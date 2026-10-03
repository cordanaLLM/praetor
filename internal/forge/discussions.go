package forge

import (
	"context"
	"errors"
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/cordanaLLM/praetor/internal/util"
)

// Invariant bounds adhering to HISS-02.
const (
	MaxADRFilesLimit = 10000
	maxADRFileBytes  = 1 << 20
	// adrFilePerm is the mode applied to a transcribed ADR file.
	adrFilePerm = 0o644
	// adrDirPerm is the mode applied to the ADR directory.
	adrDirPerm = 0o750
)

// Positions in the head renderADRContent writes: the title, a blank line, "## Status", the
// Status line, a blank line, the discussion marker "Discussion: #N", a blank line and
// "## Context". adrHeaderLines is the number of head lines.
const (
	adrTitleLine   = 0
	adrStatusLine  = 3
	adrHeaderLines = 8
)

const (
	// adrAcceptedStatus is the Status line of a newly transcribed record.
	adrAcceptedStatus = "Accepted"
	// adrRedactionsHeading opens the Redactions note ADR-0014 §7 appends to a record.
	adrRedactionsHeading = "## Redactions"
)

// Discussion represents an RFC or architectural proposal from a forge discussion board.
type Discussion struct {
	ID                   int       `json:"id"`
	Title                string    `json:"title"`
	Category             string    `json:"category"`
	Author               string    `json:"author"`
	Status               string    `json:"status"` // "approved", "accepted", "open", "closed"
	ContextText          string    `json:"context"`
	DecisionText         string    `json:"decision"`
	PositiveConsequences []string  `json:"positive_consequences,omitempty"`
	NegativeConsequences []string  `json:"negative_consequences,omitempty"`
	CreatedAt            time.Time `json:"created_at"`
}

// ADR represents an Architectural Decision Record materialized on disk.
type ADR struct {
	Number   int    `json:"number"`
	Slug     string `json:"slug"`
	FilePath string `json:"file_path"`
	Title    string `json:"title"`
	// Status is the record's Status line: Accepted for a record this call wrote, the line on
	// disk for an existing one, such as "Superseded by ADR-0007".
	Status  string `json:"status"`
	Content string `json:"content"`
	// Existing reports that the discussion's record was already on disk, so the call wrote
	// nothing and Content is the record as found.
	Existing bool `json:"existing,omitempty"`
}

var (
	// adrFileRegex matches an ADR filename. The slug is optional so that a record whose
	// title carries no [a-z0-9] character still participates in the numbering sequence
	// and cannot be silently overwritten by the next such record.
	adrFileRegex  = regexp.MustCompile(`^(\d{4})(?:-([a-z0-9\-]*))?\.md$`)
	nonAlphaRegex = regexp.MustCompile(`[^a-z0-9]+`)
)

// TranscribeDiscussionToADR converts an approved RFC discussion into an immutable ADR record.
//
// It is idempotent per discussion. Every record carries the marker line "Discussion: #N" in
// the position renderADRContent writes it, between the Status section and "## Context". When
// a record in adrDir carries disc's marker there, nothing is written: the record is returned
// with Existing set and Status read from it. Its Status line is not compared, since a
// superseded record changes only that line (docs/adr/README.md rule 4), but every other line
// must still be what disc renders to, or the call is refused because an accepted record is
// immutable. A record that lists Redactions (ADR-0014 §7) may differ from disc by the
// identifiers it redacted, so it is returned without that comparison. A record without the
// marker in its rendered position, such as one an older version wrote, belongs to no
// discussion and only holds its number.
//
// A new record takes the next free number and is created exclusively: of concurrent calls
// for one discussion, one writes the record and every other returns it as existing.
// Concurrent calls for different discussions are not serialised and can take the same
// number. A non-positive discussion ID and a title spanning several lines are refused.
//
// A relative adrDir is a location inside repoRoot and the record is written confined to
// repoRoot, so a repository that ships its ADR directory (or an ancestor) as a link leading
// outside it cannot redirect the record (BUG-826). An absolute adrDir is written as given.
func TranscribeDiscussionToADR(ctx context.Context, disc Discussion, repoRoot, adrDir string) (*ADR, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("context cancelled before discussion transcription: %w", err)
	}

	if err := validateDiscussion(disc); err != nil {
		return nil, err
	}

	out := generatedDir{root: repoRoot, dir: adrDir}
	if err := out.mkdir(adrDirPerm); err != nil {
		return nil, fmt.Errorf("failed to create ADR directory %s: %w", out.location(), err)
	}

	existing, nextNumber, err := scanADRDirectory(ctx, out, disc)
	if err != nil {
		return nil, err
	}
	if existing != nil {
		return existing, nil
	}

	return writeNewADR(ctx, out, disc, nextNumber)
}

func adrSlug(discID int, title string) string {
	slug := slugify(title)
	if slug == "" {
		return fmt.Sprintf("discussion-%d", discID)
	}
	return slug
}

// writeNewADR creates disc's record under nextNumber. The create is exclusive, so a name
// another call or an older record already holds is never overwritten; handleWriteCollision
// decides what such a collision means. A record larger than maxADRFileBytes is refused before
// it is written: the scan could never read it back, and the discussion would be transcribed
// again on every call.
func writeNewADR(ctx context.Context, out generatedDir, disc Discussion, nextNumber int) (*ADR, error) {
	slug := adrSlug(disc.ID, disc.Title)
	filename := fmt.Sprintf("%04d-%s.md", nextNumber, slug)
	content := renderADRContent(nextNumber, disc)
	if len(content) > maxADRFileBytes {
		return nil, fmt.Errorf("cannot transcribe discussion #%d: its record of %d bytes exceeds the %d-byte limit", disc.ID, len(content), maxADRFileBytes)
	}
	if err := out.writeExclusive(filename, []byte(content), adrFilePerm); err != nil {
		return handleWriteCollision(ctx, out, disc, out.path(filename), err)
	}

	return &ADR{
		Number:   nextNumber,
		Slug:     slug,
		FilePath: out.path(filename),
		Title:    disc.Title,
		Status:   adrAcceptedStatus,
		Content:  content,
	}, nil
}

// handleWriteCollision resolves writeErr, the failed exclusive create of filePath. When an
// entry already held the name, the directory is scanned again: a concurrent call that created
// disc's record first has it returned as existing, and any other entry is refused because an
// accepted record is immutable. An error from the rescan, such as the refusal of a racing
// record that no longer matches disc, is returned. Every refusal wraps writeErr, so it matches
// os.ErrExist through errors.Is, and the rescan's error is wrapped beside it.
func handleWriteCollision(ctx context.Context, out generatedDir, disc Discussion, filePath string, writeErr error) (*ADR, error) {
	if !errors.Is(writeErr, os.ErrExist) {
		return nil, fmt.Errorf("failed to write ADR file to %s: %w", filePath, writeErr)
	}
	existing, _, err := scanADRDirectory(ctx, out, disc)
	if err != nil {
		return nil, fmt.Errorf("ADR %s already exists (%w); rescanning the ADR directory: %w", filePath, writeErr, err)
	}
	if existing != nil {
		return existing, nil
	}
	return nil, fmt.Errorf("ADR %s already exists: an accepted record is immutable: %w", filePath, writeErr)
}

// validateDiscussion rejects a discussion that must not become an ADR. A title spanning
// several lines is refused: the title is the record's first line, and a line break in it
// would move the discussion marker out of its rendered position or plant a second one.
func validateDiscussion(disc Discussion) error {
	status := strings.ToLower(strings.TrimSpace(disc.Status))
	if status != "approved" && status != "accepted" {
		return fmt.Errorf("cannot transcribe discussion #%d: status '%s' is not approved", disc.ID, disc.Status)
	}
	if strings.TrimSpace(disc.Title) == "" {
		return errors.New("discussion title cannot be empty")
	}
	if strings.ContainsAny(disc.Title, "\r\n") {
		return fmt.Errorf("cannot transcribe discussion #%d: the title must be a single line", disc.ID)
	}
	if strings.TrimSpace(disc.ContextText) == "" {
		return errors.New("discussion context cannot be empty")
	}
	if strings.TrimSpace(disc.DecisionText) == "" {
		return errors.New("discussion decision cannot be empty")
	}
	if disc.ID <= 0 {
		return fmt.Errorf("cannot transcribe discussion #%d: discussion ID must be positive", disc.ID)
	}
	return nil
}

// splitRecordHead splits LF text into its adrHeaderLines head lines and, as the last element,
// everything after them.
func splitRecordHead(text string) []string {
	return strings.SplitN(text, "\n", adrHeaderLines+1)
}

// namesDiscussion reports whether record, split by splitRecordHead, carries the head
// renderADRContent writes as expected: the title line, then every fixed line and the marker
// "Discussion: #N" in their rendered positions. The title and Status lines themselves are
// not compared. A record without content after its head names no discussion.
func namesDiscussion(record, expected []string) bool {
	if len(record) != len(expected) || len(record) <= adrHeaderLines || !strings.HasPrefix(record[adrTitleLine], "# ADR-") {
		return false
	}
	for i := adrTitleLine + 1; i < adrHeaderLines; i++ {
		if i != adrStatusLine && record[i] != expected[i] {
			return false
		}
	}
	return true
}

// recordMatchesDiscussion reports whether record, which namesDiscussion already matched
// against expected, still holds what the discussion renders to: the same title line and the
// same text after the head. The Status line is not compared (docs/adr/README.md rule 4). A
// record that lists Redactions the rendering does not carry is taken as matching, since
// ADR-0014 §7 lets it differ from the discussion by the identifiers it redacted, which no
// comparison can tell apart from a changed discussion.
func recordMatchesDiscussion(record, expected []string) bool {
	if hasRedactions(record[adrHeaderLines]) && !hasRedactions(expected[adrHeaderLines]) {
		return true
	}
	return strings.TrimSpace(record[adrTitleLine]) == strings.TrimSpace(expected[adrTitleLine]) &&
		strings.TrimSpace(record[adrHeaderLines]) == strings.TrimSpace(expected[adrHeaderLines])
}

// hasRedactions reports whether LF text has a line that opens a Redactions note.
func hasRedactions(text string) bool {
	return strings.Contains("\n"+text+"\n", "\n"+adrRedactionsHeading+"\n")
}

func isIgnorableADRError(err error) bool {
	return errors.Is(err, util.ErrNotRegularFile) ||
		errors.Is(err, util.ErrFileTooLarge) ||
		errors.Is(err, os.ErrNotExist)
}

func resolveExistingADRSlug(candidate string, disc Discussion) string {
	if candidate != "" {
		return candidate
	}
	return adrSlug(disc.ID, disc.Title)
}

// inspectExistingADR reads the record name, numbered num, and returns it as disc's existing
// record when its head names disc (namesDiscussion). It returns nil for a record of another
// discussion or of none, and for an entry that vanished, outgrew maxADRFileBytes or stopped
// being a regular file after the listing. A record of disc that no longer matches disc is
// refused, because an accepted record is immutable.
func inspectExistingADR(out generatedDir, name string, num int, slug string, disc Discussion) (*ADR, error) {
	data, err := out.readLimited(name, maxADRFileBytes)
	if isIgnorableADRError(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read ADR file %q: %w", out.path(name), err)
	}
	content, _ := util.NormalizeLineEndings(string(data))
	record := splitRecordHead(content)
	expected := splitRecordHead(renderADRContent(num, disc))
	if !namesDiscussion(record, expected) {
		return nil, nil
	}
	if !recordMatchesDiscussion(record, expected) {
		return nil, fmt.Errorf("discussion #%d already transcribed in %s: an accepted record is immutable", disc.ID, out.path(name))
	}

	return &ADR{
		Number:   num,
		Slug:     resolveExistingADRSlug(slug, disc),
		FilePath: out.path(name),
		Title:    disc.Title,
		Status:   strings.TrimSpace(record[adrStatusLine]),
		Content:  string(data),
		Existing: true,
	}, nil
}

// processADREntry returns the number an ADR directory entry holds, 0 for an entry that is no
// record, and disc's existing record when the entry is it.
func processADREntry(ctx context.Context, out generatedDir, entry os.DirEntry, disc Discussion) (int, *ADR, error) {
	if err := ctx.Err(); err != nil {
		return 0, nil, fmt.Errorf("context cancelled during ADR directory scan: %w", err)
	}
	matches := adrFileRegex.FindStringSubmatch(entry.Name())
	if entry.IsDir() || len(matches) != 3 {
		return 0, nil, nil
	}
	num, err := strconv.Atoi(matches[1])
	if err != nil {
		return 0, nil, fmt.Errorf("parse ADR number %q: %w", matches[1], err)
	}
	if !entry.Type().IsRegular() {
		// Every record is written as a regular file, so a link, pipe or device is never a
		// discussion's record. It is not opened, and it still holds its number.
		return num, nil, nil
	}
	adr, err := inspectExistingADR(out, entry.Name(), num, matches[2], disc)
	return num, adr, err
}

func scanADRDirectory(ctx context.Context, out generatedDir, disc Discussion) (*ADR, int, error) {
	entries, err := os.ReadDir(out.location())
	if err != nil {
		return nil, 0, fmt.Errorf("read ADR directory %q: %w", out.location(), err)
	}

	maxNumber := 0
	for i := 0; i < len(entries) && i < MaxADRFilesLimit; i++ {
		num, adr, processErr := processADREntry(ctx, out, entries[i], disc)
		if processErr != nil {
			return nil, 0, processErr
		}
		if adr != nil {
			return adr, 0, nil
		}
		if num > maxNumber {
			maxNumber = num
		}
	}
	return nil, maxNumber + 1, nil
}

func slugify(text string) string {
	clean := strings.ToLower(strings.TrimSpace(text))
	hyphenated := nonAlphaRegex.ReplaceAllString(clean, "-")
	return strings.Trim(hyphenated, "-")
}

func renderADRContent(number int, disc Discussion) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "# ADR-%04d: %s\n\n", number, disc.Title)
	sb.WriteString("## Status\n" + adrAcceptedStatus + "\n\n")
	fmt.Fprintf(&sb, "Discussion: #%d\n\n", disc.ID)
	sb.WriteString("## Context\n")
	sb.WriteString(strings.TrimSpace(disc.ContextText) + "\n\n")
	sb.WriteString("## Decision\n")
	sb.WriteString(strings.TrimSpace(disc.DecisionText) + "\n\n")
	sb.WriteString("## Consequences\n")

	if len(disc.PositiveConsequences) > 0 {
		for i := 0; i < len(disc.PositiveConsequences) && i < MaxADRFilesLimit; i++ {
			sb.WriteString("- **Positive**: " + strings.TrimSpace(disc.PositiveConsequences[i]) + "\n")
		}
	} else {
		sb.WriteString("- **Positive**: Architectural consensus established across multi-forge federation.\n")
	}

	for i := 0; i < len(disc.NegativeConsequences) && i < MaxADRFilesLimit; i++ {
		sb.WriteString("- **Negative**: " + strings.TrimSpace(disc.NegativeConsequences[i]) + "\n")
	}

	return sb.String()
}
