// SPDX-License-Identifier: EUPL-1.2
// SPDX-FileCopyrightText: 2026 Lusoris <lusoris@proton.me>

package forge

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/cordanaLLM/praetor/internal/util"
)

// Order: engine-fr-rule-checks-20261027. Body paragraphs keep one physical line.
var bodyTableDelimiter = regexp.MustCompile(`^\|?[ \t]*:?-+:?[ \t]*(?:\|[ \t]*:?-+:?[ \t]*)+\|?$`)

var nonProseLine = regexp.MustCompile(`^(?:#{1,6}(?:[ \t]|$)|[-*+][ \t]|[0-9]{1,9}[.)][ \t]|[>|]|\[[^]]+\]:|[-*_]{3,}[ \t]*$|[=-]+[ \t]*$)`)

// BodyHardWrapFindings checks pull request and issue prose; commits retain 72-column wrapping.
// Bodies above the scalar line bound fail rather than being scanned in part.
func BodyHardWrapFindings(body string) []string {
	lines := strings.SplitN(body, "\n", MaxPRLinesLimit+1)
	if len(lines) > MaxPRLinesLimit {
		return []string{fmt.Sprintf("body exceeds %d lines", MaxPRLinesLimit)}
	}
	var findings []string
	fence := util.MarkdownFence{}
	comment, previous, table := false, false, false
	for i := 0; i < len(lines) && i < MaxPRLinesLimit; i++ {
		line := strings.TrimSuffix(lines[i], "\r")
		trimmed := strings.TrimSpace(line)
		if excludedBodyLine(trimmed, &fence, &comment) {
			previous, table = false, false
			continue
		}
		tableLine := bodyTableLine(lines, i, &table)
		prose := bodyProseLine(line, trimmed) && !tableLine
		if prose && previous {
			findings = append(findings, fmt.Sprintf("body line %d: hard-wrapped prose; keep each paragraph on one line", i+1))
		}
		previous = prose
	}
	return findings
}

func bodyCommentLine(line string, comment *bool) bool {
	inside := *comment || strings.Contains(line, "<!--")
	if inside {
		*comment = !strings.Contains(line, "-->")
	}
	return inside
}

func bodyProseLine(line, trimmed string) bool {
	if trimmed == "" || strings.HasSuffix(line, "  ") || strings.HasSuffix(line, `\`) {
		return false
	}
	return !nonProseLine.MatchString(trimmed)
}

func excludedBodyLine(line string, fence *util.MarkdownFence, comment *bool) bool {
	if fence.Open() {
		return fence.Inside(line)
	}
	if bodyCommentLine(line, comment) {
		return true
	}
	return fence.Inside(line)
}

// GFM tables also allow rows without an outside pipe. The delimiter establishes that
// context, so an ordinary paragraph mentioning a pipe remains prose.
func bodyTableLine(lines []string, index int, table *bool) bool {
	line := strings.TrimSpace(lines[index])
	if !strings.Contains(line, "|") {
		*table = false
		return false
	}
	if *table {
		return true
	}
	if index+1 < len(lines) && bodyTableDelimiter.MatchString(strings.TrimSpace(lines[index+1])) {
		*table = true
		return true
	}
	return false
}
