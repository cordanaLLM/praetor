package forge

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/cordanaLLM/praetor/internal/util"
)

// Invariant bounds adhering to HISS-02.
const (
	MaxADRFilesLimit = 10000
	// adrDirPerm is the mode of a created ADR directory.
	adrDirPerm os.FileMode = 0o755
	// adrFilePerm is the mode of a written ADR file.
	adrFilePerm os.FileMode = 0o644
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
	adrFileRegex  = regexp.MustCompile(`^(\d{4})-([a-z0-9\-]+)\.md$`)
	nonAlphaRegex = regexp.MustCompile(`[^a-z0-9]+`)
)

// TranscribeDiscussionToADR converts an approved RFC discussion into an immutable ADR record.
func TranscribeDiscussionToADR(ctx context.Context, disc Discussion, adrDir string) (*ADR, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("context cancelled before discussion transcription: %w", err)
	}

	status := strings.ToLower(strings.TrimSpace(disc.Status))
	if status != "approved" && status != "accepted" {
		return nil, fmt.Errorf("cannot transcribe discussion #%d: status '%s' is not approved", disc.ID, disc.Status)
	}
	if strings.TrimSpace(disc.Title) == "" {
		return nil, errors.New("discussion title cannot be empty")
	}
	if strings.TrimSpace(disc.ContextText) == "" {
		return nil, errors.New("discussion context cannot be empty")
	}
	if strings.TrimSpace(disc.DecisionText) == "" {
		return nil, errors.New("discussion decision cannot be empty")
	}

	if err := util.MkdirSecure(adrDir, adrDirPerm); err != nil {
		return nil, fmt.Errorf("failed to create ADR directory %s: %w", adrDir, err)
	}

	nextNumber, err := resolveNextADRNumber(adrDir)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve next ADR sequence number: %w", err)
	}

	slug := slugify(disc.Title)
	filename := fmt.Sprintf("%04d-%s.md", nextNumber, slug)
	filePath := filepath.Join(adrDir, filename)

	content := renderADRContent(nextNumber, disc)
	if err := util.WriteFileSecure(filePath, []byte(content), adrFilePerm); err != nil {
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

func resolveNextADRNumber(adrDir string) (int, error) {
	entries, err := os.ReadDir(adrDir)
	if err != nil {
		return 0, err
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
	fmt.Fprintf(&sb, "# ADR-%04d: %s\n\n", number, disc.Title)
	sb.WriteString("## Status\nAccepted\n\n")
	sb.WriteString("## Context\n")
	sb.WriteString(strings.TrimSpace(disc.ContextText) + "\n\n")
	sb.WriteString("## Decision\n")
	sb.WriteString(strings.TrimSpace(disc.DecisionText) + "\n\n")
	sb.WriteString("## Consequences\n")

	if len(disc.PositiveConsequences) > 0 {
		for _, pos := range disc.PositiveConsequences {
			fmt.Fprintf(&sb, "- **Positive**: %s\n", strings.TrimSpace(pos))
		}
	} else {
		sb.WriteString("- **Positive**: Architectural consensus established across multi-forge federation.\n")
	}

	if len(disc.NegativeConsequences) > 0 {
		for _, neg := range disc.NegativeConsequences {
			fmt.Fprintf(&sb, "- **Negative**: %s\n", strings.TrimSpace(neg))
		}
	}

	return sb.String()
}
