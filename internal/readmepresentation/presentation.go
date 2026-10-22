// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package readmepresentation

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/cordanaLLM/praetor/internal/readmegovernance"
	"github.com/cordanaLLM/praetor/internal/util"
)

// CheckFile inspects the repository file at relPath (defaulting to README.md) against rules README-01..README-05.
func CheckFile(ctx context.Context, rootDir, relPath string) ([]Finding, error) {
	if ctx == nil {
		return nil, errors.New("context required")
	}
	if relPath == "" {
		relPath = FileName
	}
	fullPath := filepath.Join(rootDir, filepath.Clean(relPath))
	data, err := util.ReadFileLimited(fullPath, MaxFileBytes)
	if errors.Is(err, fs.ErrNotExist) {
		return []Finding{{
			File:    relPath,
			Line:    1,
			RuleID:  RuleIDAbsent,
			Message: fmt.Sprintf("%s is absent", relPath),
		}}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", relPath, err)
	}
	return CheckContent(rootDir, relPath, string(data))
}

// CheckContent evaluates the presentation rules over the provided document content.
func CheckContent(rootDir, relPath, content string) ([]Finding, error) {
	lines := strings.Split(strings.ReplaceAll(content, "\r\n", "\n"), "\n")
	if len(lines) > MaxLines {
		return []Finding{{
			File:    relPath,
			Line:    1,
			RuleID:  RuleIDSections,
			Message: fmt.Sprintf("document exceeds %d lines", MaxLines),
		}}, nil
	}
	var findings []Finding
	headerStart, headerEnd := findHeaderBounds(lines)
	checkHeroBanner(lines, headerStart, headerEnd, relPath, &findings)
	_, blockEnd := checkBadgeBlock(lines, headerStart, headerEnd, relPath, &findings)
	checkTagline(lines, blockEnd, relPath, &findings)

	headerLimit := headerEnd
	if blockEnd > headerLimit {
		headerLimit = blockEnd
	}
	scanBodyLines(rootDir, relPath, lines, headerLimit, &findings)
	return findings, nil
}

type bodyScanContext struct {
	rootDir     string
	relPath     string
	headerLimit int
	findings    *[]Finding
	fence       util.MarkdownFence
	secState    *sectionState
	mermaidWait bool
}

func (c *bodyScanContext) checkFenceStart(trimmed string) {
	lang := util.MarkdownFenceLanguage(trimmed, c.fence.Marker())
	if lang == "mermaid" || strings.HasPrefix(strings.ToLower(trimmed), "```mermaid") || strings.HasPrefix(strings.ToLower(trimmed), "~~~mermaid") {
		c.mermaidWait = true
	}
}

func (c *bodyScanContext) checkFenceInside(line string, idx int) {
	if c.mermaidWait {
		checkMermaidBlock(line, idx+1, c.relPath, c.findings)
		c.mermaidWait = false
	}
}

func (c *bodyScanContext) step(line string, idx int) {
	trimmed := strings.TrimSpace(line)
	if strings.Contains(trimmed, readmegovernance.Start) {
		c.secState.governanceSeen = true
	}
	wasOpen := c.fence.Open()
	isInside := c.fence.Inside(trimmed)

	if !wasOpen && isInside {
		c.checkFenceStart(trimmed)
		return
	}
	if isInside {
		c.checkFenceInside(line, idx)
		return
	}
	c.mermaidWait = false
	if idx > c.headerLimit {
		checkSvgBelowHeader(c.rootDir, line, c.relPath, idx+1, c.findings)
		checkSectionHeading(line, idx+1, c.relPath, c.secState, c.findings)
	}
}

func scanBodyLines(rootDir, relPath string, lines []string, headerLimit int, findings *[]Finding) {
	ctx := &bodyScanContext{
		rootDir:     rootDir,
		relPath:     relPath,
		headerLimit: headerLimit,
		findings:    findings,
		secState:    newSectionState(),
	}
	n := len(lines)
	if n > MaxLines {
		n = MaxLines
	}
	for i := 0; i < n; i++ {
		ctx.step(lines[i], i)
	}
}

// Exists reports whether relPath exists under rootDir.
func Exists(rootDir, relPath string) bool {
	full := filepath.Join(rootDir, filepath.Clean(relPath))
	_, err := os.Stat(full)
	return err == nil
}
