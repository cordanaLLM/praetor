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

	nextNumber, err := resolveNextADRNumber(out.location())
	if err != nil {
		return nil, fmt.Errorf("failed to resolve next ADR sequence number: %w", err)
	}

	slug := slugify(disc.Title)
	if slug == "" {
		// A title written entirely outside [a-z0-9] (for example a CJK title) would
		// otherwise produce the same slug-less filename for every such discussion.
		slug = fmt.Sprintf("discussion-%d", disc.ID)
	}
	filename := fmt.Sprintf("%04d-%s.md", nextNumber, slug)
	filePath := out.path(filename)
	if util.PathExists(filePath) {
		return nil, fmt.Errorf("ADR %s already exists: an accepted record is immutable", filePath)
	}

	content := renderADRContent(nextNumber, disc)
	if err := out.write(filename, []byte(content), adrFilePerm); err != nil {
		return nil, fmt.Errorf("failed to write ADR file to %s: %w", filePath, err)
	}

	return &ADR{
		Number:   nextNumber,
		Slug:     slug,
		FilePath: filePath,
		Title:    disc.Title,
		Status:   "Accepted",
		Content:  content,
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
	return nil
}

func resolveNextADRNumber(adrDir string) (int, error) {
	entries, err := os.ReadDir(adrDir)
	if err != nil {
		return 0, fmt.Errorf("read ADR directory %q: %w", adrDir, err)
	}

	maxNumber := 0
	for i := 0; i < len(entries) && i < MaxADRFilesLimit; i++ {
		entry := entries[i]
		if entry.IsDir() {
			continue
		}
		matches := adrFileRegex.FindStringSubmatch(entry.Name())
		if len(matches) == 3 {
			if num, parseErr := strconv.Atoi(matches[1]); parseErr == nil && num > maxNumber {
				maxNumber = num
			}
		}
	}
	return maxNumber + 1, nil
}

func slugify(text string) string {
	clean := strings.ToLower(strings.TrimSpace(text))
	hyphenated := nonAlphaRegex.ReplaceAllString(clean, "-")
	return strings.Trim(hyphenated, "-")
}

func renderADRContent(number int, disc Discussion) string {
	var sb strings.Builder
	header := fmt.Sprintf("# ADR-%04d: %s\n\n", number, disc.Title)
	sb.WriteString(header)
	sb.WriteString("## Status\nAccepted\n\n")
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
