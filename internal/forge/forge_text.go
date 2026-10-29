// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package forge

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/cordanaLLM/praetor/internal/util"
)

// maxForgeTextLines bounds the line scan of forge text (HISS-02).
const maxForgeTextLines = 100000

var (
	// markdownListItem matches bullet list items, numbered list items, and checklist items.
	markdownListItem = regexp.MustCompile(`^\s*(?:[-*+]|\d+[.)])\s`)
	// sentenceEndRegex matches terminal punctuation optionally followed by closing delimiters.
	sentenceEndRegex = regexp.MustCompile(`[.!?][)\]"'\x60*_~]*$`)
)

type markdownStream struct {
	fence     util.MarkdownFence
	inComment bool
}

func (m *markdownStream) shouldPassThrough(trimmed string) bool {
	if m.fence.Inside(trimmed) {
		return true
	}
	if m.inComment {
		if strings.Contains(trimmed, "-->") {
			m.inComment = false
		}
		return true
	}
	if strings.HasPrefix(trimmed, "<!--") {
		if !strings.Contains(trimmed[4:], "-->") {
			m.inComment = true
		}
		return true
	}
	return isIgnoredLine(trimmed)
}

func isIgnoredLine(trimmed string) bool {
	switch {
	case trimmed == "":
		return true
	case isHeadingLine(trimmed):
		return true
	case isTableLine(trimmed):
		return true
	case isBlockQuoteLine(trimmed):
		return true
	case isThematicBreak(trimmed):
		return true
	default:
		return false
	}
}

type paragraphValidator struct {
	markdownStream
	where    string
	lines    []string
	findings []string
}

func (v *paragraphValidator) checkLine(i int) {
	rawLine := strings.TrimSuffix(v.lines[i], "\r")
	trimmed := strings.TrimSpace(rawLine)
	if v.shouldPassThrough(trimmed) {
		return
	}
	if i+1 < len(v.lines) && isContinuationLine(v.lines[i+1]) && endsInsideSentence(trimmed) {
		v.findings = append(v.findings, fmt.Sprintf(
			"%s line %d: hard-wrapped forge paragraph: line ends inside a sentence followed by continuation line",
			v.where, i+1))
	}
}

// ValidateForgeParagraphs checks forge text (PR bodies, issues, comments, templates) for
// hard-wrapped paragraphs: lines ending mid-sentence that are followed by a line continuing the
// same paragraph or list item. Fences, tables, list markers, headings, block quotes, and HTML
// comments are ignored. Returns findings naming where and the line number.
func ValidateForgeParagraphs(text, where string) []string {
	if text == "" {
		return nil
	}
	lines := strings.Split(text, "\n")
	v := &paragraphValidator{
		where: where,
		lines: lines,
	}
	for i := 0; i < len(lines) && i < maxForgeTextLines; i++ {
		v.checkLine(i)
	}
	return v.findings
}

// UnwrapForgeText joins soft-wrapped lines inside paragraphs and list items into one line
// while preserving blank lines, code fences, tables, headings, block quotes, HTML comments,
// and list markers.
func UnwrapForgeText(text string) string {
	if text == "" {
		return ""
	}
	crlf := strings.Contains(text, "\r\n")
	lines := strings.Split(text, "\n")
	u := newUnwrapper()
	for i := 0; i < len(lines) && i < maxForgeTextLines; i++ {
		u.processLine(lines[i])
	}
	u.flush()

	output := strings.Join(u.result, "\n")
	if crlf {
		output = strings.ReplaceAll(output, "\n", "\r\n")
	}
	return output
}

type unwrapper struct {
	markdownStream
	result  []string
	pending string
}

func newUnwrapper() *unwrapper {
	return &unwrapper{}
}

func (u *unwrapper) flush() {
	if u.pending != "" {
		u.result = append(u.result, u.pending)
		u.pending = ""
	}
}

func (u *unwrapper) processLine(rawLine string) {
	cleanLine := strings.TrimSuffix(rawLine, "\r")
	trimmed := strings.TrimSpace(cleanLine)

	if u.shouldPassThrough(trimmed) {
		u.flush()
		u.result = append(u.result, cleanLine)
		return
	}
	if isListItemLine(cleanLine) {
		u.flush()
		u.pending = cleanLine
		return
	}
	if u.pending != "" {
		u.pending = u.pending + " " + trimmed
	} else {
		u.pending = cleanLine
	}
}

func isHeadingLine(trimmed string) bool {
	_, ok := util.MarkdownHeadingText(trimmed)
	return ok
}

func isTableLine(trimmed string) bool {
	return strings.HasPrefix(trimmed, "|")
}

func isBlockQuoteLine(trimmed string) bool {
	return strings.HasPrefix(trimmed, ">")
}

func isListItemLine(line string) bool {
	return markdownListItem.MatchString(line)
}

func isThematicBreak(trimmed string) bool {
	return trimmed == "---" || trimmed == "***" || trimmed == "___"
}

func isFenceDelimiter(trimmed string) bool {
	return strings.HasPrefix(trimmed, "```") || strings.HasPrefix(trimmed, "~~~")
}

func isContinuationLine(line string) bool {
	trimmed := strings.TrimSpace(strings.TrimSuffix(line, "\r"))
	if trimmed == "" {
		return false
	}
	if isFenceDelimiter(trimmed) || isHeadingLine(trimmed) || isTableLine(trimmed) ||
		isBlockQuoteLine(trimmed) || strings.HasPrefix(trimmed, "<!--") ||
		isListItemLine(line) || isThematicBreak(trimmed) {
		return false
	}
	return true
}

func endsInsideSentence(line string) bool {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" {
		return false
	}
	return !sentenceEndRegex.MatchString(trimmed)
}
