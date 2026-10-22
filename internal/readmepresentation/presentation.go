// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package readmepresentation

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"strings"

	"github.com/cordanaLLM/praetor/internal/readmegovernance"
	"github.com/cordanaLLM/praetor/internal/util"
)

// CheckFile inspects the repository file at relPath (defaulting to README.md) against rules README-01..README-05.
func CheckFile(ctx context.Context, rootDir, relPath string) ([]Finding, error) {
	findings, _, err := CheckFileWithInfo(ctx, rootDir, relPath)
	return findings, err
}

// CheckFileWithInfo inspects relPath and returns both counted findings and informational lines.
func CheckFileWithInfo(ctx context.Context, rootDir, relPath string) ([]Finding, []Finding, error) {
	if ctx == nil {
		return nil, nil, errors.New("context required")
	}
	if relPath == "" {
		relPath = FileName
	}
	data, err := util.ReadConfinedLimited(rootDir, relPath, MaxFileBytes)
	if errors.Is(err, fs.ErrNotExist) {
		return []Finding{{
			File:    relPath,
			Line:    1,
			RuleID:  RuleIDAbsent,
			Message: fmt.Sprintf("%s is absent", relPath),
		}}, nil, nil
	}
	if err != nil {
		return []Finding{{
			File:    relPath,
			Line:    1,
			RuleID:  RuleIDAbsent,
			Message: describeReadFailure(relPath, err),
		}}, nil, nil
	}
	return CheckContentWithInfo(rootDir, relPath, string(data))
}

func describeReadFailure(relPath string, err error) string {
	switch {
	case errors.Is(err, util.ErrNotRegularFile):
		return fmt.Sprintf("%s is not a regular file", relPath)
	case errors.Is(err, util.ErrFileTooLarge):
		return fmt.Sprintf("%s exceeds maximum size (%d bytes)", relPath, MaxFileBytes)
	default:
		return fmt.Sprintf("%s cannot be read: %s", relPath, sanitizeDisplayPath(err.Error()))
	}
}

// CheckContent evaluates the presentation rules over the provided document content.
func CheckContent(rootDir, relPath, content string) ([]Finding, error) {
	findings, _, err := CheckContentWithInfo(rootDir, relPath, content)
	return findings, err
}

// CheckContentWithInfo evaluates the presentation rules and returns counted findings and informational lines.
func CheckContentWithInfo(rootDir, relPath, content string) ([]Finding, []Finding, error) {
	content = strings.TrimPrefix(content, "\xef\xbb\xbf")
	normalized := strings.ReplaceAll(content, "\r\n", "\n")
	normalized = strings.ReplaceAll(normalized, "\r", "\n")
	lines := strings.Split(normalized, "\n")
	if len(lines) > MaxLines {
		return []Finding{{
			File:    relPath,
			Line:    1,
			RuleID:  RuleIDSections,
			Message: fmt.Sprintf("document exceeds %d lines", MaxLines),
		}}, nil, nil
	}
	var findings []Finding
	var infos []Finding
	headerStart, headerEnd := findHeaderBounds(lines)
	checkHeroBanner(lines, headerStart, headerEnd, relPath, &findings)
	_, blockEnd := checkBadgeBlock(lines, headerStart, headerEnd, relPath, &findings)
	checkTagline(lines, blockEnd, relPath, &findings)

	headerLimit := headerEnd
	if blockEnd > headerLimit {
		headerLimit = blockEnd
	}
	scanBodyLines(rootDir, relPath, lines, headerLimit, &findings, &infos)
	return findings, infos, nil
}

type bodyScanContext struct {
	rootDir              string
	relPath              string
	headerLimit          int
	findings             *[]Finding
	infos                *[]Finding
	fence                util.MarkdownFence
	fenceLine            int
	inComment            bool
	secState             *sectionState
	mermaidWait          bool
	mermaidLine          int
	inMermaidFrontmatter bool
	mermaidFrontmatter   strings.Builder
}

func (c *bodyScanContext) checkFenceStart(trimmed string, idx int) {
	c.fenceLine = idx + 1
	c.inMermaidFrontmatter = false
	c.mermaidFrontmatter.Reset()
	lang := strings.Trim(strings.ToLower(util.MarkdownFenceLanguage(trimmed, c.fence.Marker())), "{}")
	if lang == "mermaid" || strings.HasPrefix(strings.ToLower(trimmed), "```mermaid") || strings.HasPrefix(strings.ToLower(trimmed), "~~~mermaid") {
		c.mermaidWait = true
		c.mermaidLine = idx + 1
	}
}

func (c *bodyScanContext) checkFenceInside(line string, idx int) {
	if !c.mermaidWait {
		return
	}
	trimmed := strings.TrimSpace(line)
	if c.consumeMermaidPreamble(trimmed, idx) {
		return
	}
	checkMermaidBlock(trimmed, idx+1, c.relPath, c.findings)
	c.mermaidWait = false
}

func (c *bodyScanContext) consumeMermaidPreamble(trimmed string, idx int) bool {
	if !c.inMermaidFrontmatter {
		if trimmed == "" {
			return true
		}
		if trimmed == "---" {
			c.inMermaidFrontmatter = true
			c.mermaidLine = idx + 1
			return true
		}
		return false
	}
	if trimmed != "---" {
		if c.mermaidFrontmatter.Len() < 4096 {
			c.mermaidFrontmatter.WriteString(trimmed)
			c.mermaidFrontmatter.WriteByte(' ')
		}
		return true
	}
	c.inMermaidFrontmatter = false
	checkMermaidBlock("%%{init: "+c.mermaidFrontmatter.String()+"}%%", c.mermaidLine, c.relPath, c.findings)
	c.mermaidWait = false
	return true
}

func (c *bodyScanContext) finishFence() {
	if c.mermaidWait {
		checkMermaidBlock("", c.mermaidLine, c.relPath, c.findings)
		c.mermaidWait = false
	}
	c.inMermaidFrontmatter = false
	c.mermaidFrontmatter.Reset()
}

func (c *bodyScanContext) step(line string, idx int) {
	unquoted := stripBlockquote(line)
	trimmed := strings.TrimSpace(unquoted)
	wasOpen := c.fence.Open()
	isInside := c.fence.Inside(trimmed)

	if !wasOpen && isInside {
		c.checkFenceStart(trimmed, idx)
		return
	}
	if isInside {
		c.checkFenceInside(unquoted, idx)
		return
	}
	if wasOpen {
		c.finishFence()
		return
	}
	if advanceCommentState(trimmed, &c.inComment) {
		return
	}
	if leadingSpaces(unquoted) <= 3 && strings.Contains(trimmed, readmegovernance.Start) {
		c.secState.governanceSeen = true
	}
	c.mermaidWait = false
	if idx > c.headerLimit {
		clean := stripInlineComments(unquoted)
		checkSvgBelowHeader(c.rootDir, clean, c.relPath, idx+1, c.findings)
		if leadingSpaces(unquoted) <= 3 {
			checkSectionHeading(clean, idx+1, c.relPath, c.secState, c.findings, c.infos)
		}
	}
}

func scanBodyLines(rootDir, relPath string, lines []string, headerLimit int, findings, infos *[]Finding) {
	ctx := &bodyScanContext{
		rootDir:     rootDir,
		relPath:     relPath,
		headerLimit: headerLimit,
		findings:    findings,
		infos:       infos,
		secState:    newSectionState(),
	}
	n := len(lines)
	if n > MaxLines {
		n = MaxLines
	}
	for i := 0; i < n; i++ {
		ctx.step(lines[i], i)
	}
	if ctx.fence.Open() {
		if ctx.mermaidWait {
			ctx.finishFence()
		} else {
			*findings = append(*findings, Finding{
				File:    relPath,
				Line:    ctx.fenceLine,
				RuleID:  RuleIDDiagrams,
				Message: "unterminated code fence",
			})
		}
	}
}
