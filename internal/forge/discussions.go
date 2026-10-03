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
	MaxADRFilesLimit    = 10000
	maxADRFileBytes     = 1 << 20
	maxHeaderLinesLimit = 64
	// adrFilePerm is the mode applied to a transcribed ADR file.
	adrFilePerm = 0o644
	// adrDirPerm is the mode applied to the ADR directory.
	adrDirPerm = 0o750
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
	Status   string `json:"status"`
	Content  string `json:"content"`
	Existing bool   `json:"existing,omitempty"`
}

var (
	// adrFileRegex matches an ADR filename. The slug is optional so that a record whose
	// title carries no [a-z0-9] character still participates in the numbering sequence
	// and cannot be silently overwritten by the next such record.
	adrFileRegex  = regexp.MustCompile(`^(\d{4})(?:-([a-z0-9\-]*))?\.md$`)
	nonAlphaRegex = regexp.MustCompile(`[^a-z0-9]+`)
	// discussionMarkerRegex matches an ADR reference line or front-matter entry carrying the discussion ID.
	discussionMarkerRegex = regexp.MustCompile(`(?i)^(?:[-*]\s+)?(?:Reference:\s*)?(?:Discussion|Discussion-ID|discussion_id)(?:\s*ID)?:\s*#?\s*(\d+)\s*$`)
)

// TranscribeDiscussionToADR converts an approved RFC discussion into an immutable ADR record.
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

func handleWriteCollision(ctx context.Context, out generatedDir, disc Discussion, filePath string, writeErr error) (*ADR, error) {
	if errors.Is(writeErr, os.ErrExist) {
		if recheck, err := findExistingDiscussionADR(ctx, out, disc); err == nil && recheck != nil {
			return recheck, nil
		}
		return nil, fmt.Errorf("ADR %s already exists: an accepted record is immutable", filePath)
	}
	return nil, fmt.Errorf("failed to write ADR file to %s: %w", filePath, writeErr)
}

func writeNewADR(ctx context.Context, out generatedDir, disc Discussion, nextNumber int) (*ADR, error) {
	slug := adrSlug(disc.ID, disc.Title)
	filename := fmt.Sprintf("%04d-%s.md", nextNumber, slug)
	filePath := out.path(filename)
	if util.PathExists(filePath) {
		return nil, fmt.Errorf("ADR %s already exists: an accepted record is immutable", filePath)
	}

	content := renderADRContent(nextNumber, disc)
	if err := out.writeExclusive(filename, []byte(content), adrFilePerm); err != nil {
		return handleWriteCollision(ctx, out, disc, filePath, err)
	}

	return &ADR{
		Number:   nextNumber,
		Slug:     slug,
		FilePath: filePath,
		Title:    disc.Title,
		Status:   "Accepted",
		Content:  content,
		Existing: false,
	}, nil
}

// validateDiscussion rejects a discussion that must not become an ADR.
func validateDiscussion(disc Discussion) error {
	status := strings.ToLower(strings.TrimSpace(disc.Status))
	if status != "approved" && status != "accepted" {
		return fmt.Errorf("cannot transcribe discussion #%d: status '%s' is not approved", disc.ID, disc.Status)
	}
	if strings.TrimSpace(disc.Title) == "" {
		return errors.New("discussion title cannot be empty")
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

func extractDiscussionID(content string) (int, bool) {
	lines := strings.Split(strings.ReplaceAll(content, "\r\n", "\n"), "\n")
	for i := 0; i < len(lines) && i < maxHeaderLinesLimit; i++ {
		line := strings.TrimSpace(lines[i])
		if line == "" {
			continue
		}
		matches := discussionMarkerRegex.FindStringSubmatch(line)
		if len(matches) == 2 {
			id, err := strconv.Atoi(matches[1])
			if err == nil && id > 0 {
				return id, true
			}
		}
	}
	return 0, false
}

func normalizeADRContent(text string) string {
	return strings.TrimSpace(strings.ReplaceAll(text, "\r\n", "\n"))
}

func isIgnorableADRError(err error) bool {
	return errors.Is(err, util.ErrNotRegularFile) ||
		errors.Is(err, util.ErrFileTooLarge) ||
		errors.Is(err, util.ErrPathEscapesRoot) ||
		errors.Is(err, os.ErrNotExist)
}

func resolveExistingADRSlug(candidate string, disc Discussion) string {
	if candidate != "" {
		return candidate
	}
	return adrSlug(disc.ID, disc.Title)
}

func inspectExistingADR(out generatedDir, name string, matches []string, disc Discussion) (*ADR, bool, error) {
	data, err := out.readLimited(name, maxADRFileBytes)
	if err != nil {
		if isIgnorableADRError(err) {
			return nil, false, nil
		}
		return nil, false, fmt.Errorf("read ADR file %q: %w", out.path(name), err)
	}
	content := string(data)
	discID, ok := extractDiscussionID(content)
	if !ok || discID != disc.ID {
		return nil, false, nil
	}

	num, err := strconv.Atoi(matches[1])
	if err != nil {
		return nil, false, fmt.Errorf("parse ADR number %q: %w", matches[1], err)
	}

	expected := renderADRContent(num, disc)
	if normalizeADRContent(content) != normalizeADRContent(expected) {
		return nil, false, fmt.Errorf("discussion #%d already transcribed in %s: an accepted record is immutable", disc.ID, out.path(name))
	}

	return &ADR{
		Number:   num,
		Slug:     resolveExistingADRSlug(matches[2], disc),
		FilePath: out.path(name),
		Title:    disc.Title,
		Status:   "Accepted",
		Content:  content,
		Existing: true,
	}, true, nil
}

func processADREntry(ctx context.Context, out generatedDir, entry os.DirEntry, disc Discussion) (int, *ADR, error) {
	if err := ctx.Err(); err != nil {
		return 0, nil, fmt.Errorf("context cancelled during ADR directory scan: %w", err)
	}
	if entry.IsDir() {
		return 0, nil, nil
	}
	matches := adrFileRegex.FindStringSubmatch(entry.Name())
	if len(matches) != 3 {
		return 0, nil, nil
	}
	num, err := strconv.Atoi(matches[1])
	if err != nil {
		return 0, nil, fmt.Errorf("parse ADR number %q: %w", matches[1], err)
	}
	if disc.ID <= 0 {
		return num, nil, nil
	}
	adr, match, err := inspectExistingADR(out, entry.Name(), matches, disc)
	if err != nil {
		return 0, nil, err
	}
	if match {
		return num, adr, nil
	}
	return num, nil, nil
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

func findExistingDiscussionADR(ctx context.Context, out generatedDir, disc Discussion) (*ADR, error) {
	adr, _, err := scanADRDirectory(ctx, out, disc)
	return adr, err
}

func slugify(text string) string {
	clean := strings.ToLower(strings.TrimSpace(text))
	hyphenated := nonAlphaRegex.ReplaceAllString(clean, "-")
	return strings.Trim(hyphenated, "-")
}

func renderADRContent(number int, disc Discussion) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "# ADR-%04d: %s\n\n", number, disc.Title)
	sb.WriteString("## Status\nAccepted\n\n")
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
