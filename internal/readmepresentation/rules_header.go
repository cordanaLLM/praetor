// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package readmepresentation

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/cordanaLLM/praetor/internal/util"
)

var (
	imgHeightAttrRe    = regexp.MustCompile(`(?i)(?:^|[\s"'/])height\s*=\s*["']?([0-9]+)(?:px)?["']?`)
	imgHeightStyleRe   = regexp.MustCompile(`(?i)(?:^|[\s"';])height\s*:\s*([0-9]+)px`)
	heroWidthPercentRe = regexp.MustCompile(`(?i)(?:width\s*=\s*["']100%["']|width\s*:\s*100%)`)
	htmlCommentRe      = regexp.MustCompile(`<!--\s*(.*?)\s*-->`)
)

func isHeaderOpening(s string) bool {
	return strings.HasPrefix(s, "<div") || strings.HasPrefix(s, "<header")
}

func headerTagDelta(s string) int {
	lower := strings.ToLower(stripInlineComments(s))
	opens := strings.Count(lower, "<div") + strings.Count(lower, "<header")
	closes := strings.Count(lower, "</div>") + strings.Count(lower, "</header>")
	return opens - closes
}

func stepTagHeader(line string, depth *int) bool {
	*depth += headerTagDelta(line)
	return *depth <= 0
}

func stepTitleHeader(trimmed string, sawBadge *bool) bool {
	if strings.Contains(trimmed, "style=for-the-badge") {
		*sawBadge = true
		return false
	}
	return *sawBadge && isBoldLine(trimmed)
}

func closesHeaderAt(line, trimmed string, openedTag bool, depth *int, sawBadge *bool) bool {
	if openedTag {
		return stepTagHeader(line, depth)
	}
	return stepTitleHeader(trimmed, sawBadge)
}

func isHeaderStart(trimmed string) bool {
	return isHeaderOpening(trimmed) || strings.HasPrefix(trimmed, "# ")
}

func isBodyH2(line, trimmed string) bool {
	return leadingSpaces(line) <= 3 && strings.HasPrefix(trimmed, "## ")
}

func findHeaderBounds(lines []string) (start, end int) {
	start, end = -1, -1
	n := min(len(lines), MaxLines)
	openedTag, sawBadge, inComment, depth := false, false, false, 0
	for i := 0; i < n; i++ {
		trimmed := strings.TrimSpace(lines[i])
		if advanceCommentState(trimmed, &inComment) {
			continue
		}
		if start < 0 {
			if isHeaderStart(trimmed) {
				start, openedTag = i, isHeaderOpening(trimmed)
				depth = headerTagDelta(lines[i])
			}
			continue
		}
		if isBodyH2(lines[i], trimmed) {
			end = i - 1
			break
		}
		if closesHeaderAt(lines[i], trimmed, openedTag, &depth, &sawBadge) {
			end = i
			break
		}
	}
	if start >= 0 && end < 0 {
		end = min(start+50, len(lines))
	}
	return start, end
}

func collectImgLine(lines []string, start, headerEnd int) (string, int) {
	line := lines[start]
	if len(line) > 4096 || !strings.Contains(strings.ToLower(line), "<img") || strings.Contains(line, ">") {
		return line, start
	}
	var b strings.Builder
	b.WriteString(line)
	end := min(headerEnd, min(len(lines)-1, start+16))
	j := start
	for j < end {
		j++
		b.WriteByte(' ')
		b.WriteString(lines[j])
		if strings.Contains(lines[j], ">") {
			break
		}
	}
	return b.String(), j
}

func checkHeroBanner(lines []string, headerStart, headerEnd int, relPath string, findings *[]Finding) {
	if headerStart < 0 {
		return
	}
	for i := headerStart; i <= headerEnd && i < len(lines); i++ {
		startLine := i + 1
		line, nextI := collectImgLine(lines, i, headerEnd)
		i = nextI
		if !strings.Contains(strings.ToLower(line), "<img") {
			continue
		}
		if h, ok := parseHeroHeight(line); ok && h > HeroMaxHeight {
			*findings = append(*findings, Finding{
				File:    relPath,
				Line:    startLine,
				RuleID:  RuleIDHero,
				Message: fmt.Sprintf("hero banner height %d exceeds maximum height %d", h, HeroMaxHeight),
			})
		}
		if heroWidthPercentRe.MatchString(line) {
			*findings = append(*findings, Finding{
				File:    relPath,
				Line:    startLine,
				RuleID:  RuleIDHero,
				Message: `hero banner width="100%" is not permitted`,
			})
		}
	}
}

func maxRegexInt(matches [][]string) (int, bool) {
	best, found := 0, false
	for i := 0; i < len(matches) && i < 16; i++ {
		if len(matches[i]) > 1 {
			if val, err := strconv.Atoi(matches[i][1]); err == nil && (!found || val > best) {
				best, found = val, true
			}
		}
	}
	return best, found
}

func parseHeroHeight(line string) (int, bool) {
	if len(line) > 4096 {
		line = line[:4096]
	}
	best, found := maxRegexInt(imgHeightAttrRe.FindAllStringSubmatch(line, 16))
	if m := imgHeightStyleRe.FindStringSubmatch(line); len(m) > 1 {
		if styleBest, ok := maxRegexInt(imgHeightStyleRe.FindAllStringSubmatch(line, 16)); ok && (!found || styleBest > best) {
			best, found = styleBest, true
		}
	}
	return best, found
}

func checkBadgeBlock(lines []string, headerStart, headerEnd int, relPath string, findings *[]Finding) (int, int) {
	blockStart, blockEnd := findBadgeBlockRange(lines, headerStart, headerEnd)
	if blockStart < 0 {
		lineNum := 1
		if headerStart >= 0 {
			lineNum = headerStart + 1
		}
		*findings = append(*findings, Finding{
			File:    relPath,
			Line:    lineNum,
			RuleID:  RuleIDBadgeBlock,
			Message: "missing style=for-the-badge badge block under header",
		})
	} else {
		checkBadgeGroupsOrder(lines, blockStart, blockEnd, relPath, findings)
	}
	checkBadgesOutsideBlock(lines, blockStart, blockEnd, relPath, findings)
	return blockStart, blockEnd
}

func isHeaderPreambleLine(trimmed string) bool {
	if trimmed == "" || isHeaderOpening(trimmed) || strings.HasPrefix(trimmed, "# ") {
		return true
	}
	lower := strings.ToLower(trimmed)
	for _, prefix := range [...]string{
		"<!--", "<img", "![", "<picture", "<source", "</picture>",
		"<h1", "<a ", "</a>", "</div>", "<p", "</p>", "<br",
		"<strong", "<b", "<em", "<i", "*", "_",
	} {
		if strings.HasPrefix(lower, prefix) {
			return true
		}
	}
	return false
}

func hasForTheBadge(lines []string, start, end int) bool {
	for i := start; i >= 0 && i <= end && i < len(lines) && i < MaxLines; i++ {
		if strings.Contains(lines[i], "style=for-the-badge") {
			return true
		}
	}
	return false
}

func advanceCommentState(trimmed string, inComment *bool) bool {
	if *inComment {
		if strings.Contains(trimmed, "-->") {
			*inComment = false
		}
		return true
	}
	if strings.HasPrefix(trimmed, "<!--") && !strings.Contains(trimmed, "-->") {
		*inComment = true
		return true
	}
	return false
}

func isBadgeBlockLine(trimmed string) bool {
	if strings.Contains(trimmed, "style=for-the-badge") {
		return true
	}
	group, _ := parseBadgeGroup(trimmed)
	return group > 0
}

func shouldStopBadgeBlockScan(blockStart int, trimmed string) bool {
	if blockStart < 0 {
		return !isHeaderPreambleLine(trimmed)
	}
	lower := strings.ToLower(trimmed)
	if lower == "<p>" || lower == "</p>" || strings.HasPrefix(lower, "<p ") {
		return false
	}
	return trimmed != "" && !strings.HasPrefix(trimmed, "<!--")
}

func recordBadgeLine(i int, blockStart, blockEnd *int) {
	if *blockStart < 0 {
		*blockStart = i
	}
	*blockEnd = i
}

func findBadgeBlockRange(lines []string, headerStart, headerEnd int) (int, int) {
	if headerStart < 0 {
		headerStart, headerEnd = 0, len(lines)-1
	}
	blockStart, blockEnd := -1, -1
	inComment := false
	for i := headerStart; i <= headerEnd && i < len(lines) && i < MaxLines; i++ {
		trimmed := strings.TrimSpace(lines[i])
		if advanceCommentState(trimmed, &inComment) {
			continue
		}
		if isBadgeBlockLine(trimmed) {
			recordBadgeLine(i, &blockStart, &blockEnd)
			continue
		}
		if shouldStopBadgeBlockScan(blockStart, trimmed) {
			break
		}
	}
	if blockStart >= 0 && !hasForTheBadge(lines, blockStart, blockEnd) {
		return -1, -1
	}
	return blockStart, blockEnd
}

func checkBadgeGroupsOrder(lines []string, blockStart, blockEnd int, relPath string, findings *[]Finding) {
	lastRank := 0
	for i := blockStart; i <= blockEnd && i < len(lines); i++ {
		trimmed := strings.TrimSpace(lines[i])
		rank, ok := parseBadgeGroup(trimmed)
		if !ok {
			continue
		}
		if rank <= lastRank {
			*findings = append(*findings, Finding{
				File:    relPath,
				Line:    i + 1,
				RuleID:  RuleIDBadgeBlock,
				Message: fmt.Sprintf("badge group comment out of order (group %d appears after group %d)", rank, lastRank),
			})
		} else {
			lastRank = rank
		}
	}
}

func parseBadgeGroup(line string) (int, bool) {
	matches := htmlCommentRe.FindStringSubmatch(line)
	if len(matches) < 2 {
		return 0, false
	}
	text := strings.TrimSpace(matches[1])
	if text == "" || strings.HasPrefix(strings.ToLower(text), "readme-standard") {
		return 0, false
	}
	return matchBadgeGroupRank(text)
}

type badgeGroupPrefix struct {
	prefix string
	rank   int
}

var badgeGroupPrefixes = [...]badgeGroupPrefix{
	{"release", 1}, {"version", 1},
	{"ci", 2}, {"build", 2},
	{"security", 3}, {"scorecard", 3},
	{"standards", 4},
	{"license", 5}, {"licence", 5},
	{"support", 6},
}

func isWordByte(b byte) bool {
	return (b >= 'a' && b <= 'z') || (b >= '0' && b <= '9')
}

func matchNamedBadgeGroup(lower string) (int, bool) {
	for _, bg := range badgeGroupPrefixes {
		if strings.HasPrefix(lower, bg.prefix) && (len(lower) == len(bg.prefix) || !isWordByte(lower[len(bg.prefix)])) {
			return bg.rank, true
		}
	}
	return 0, false
}

func matchBadgeGroupRank(text string) (int, bool) {
	if len(text) > 0 && text[0] >= '1' && text[0] <= '6' {
		if len(text) == 1 || text[1] < '0' || text[1] > '9' {
			return int(text[0] - '0'), true
		}
		return 0, false
	}
	return matchNamedBadgeGroup(strings.ToLower(text))
}

func checkBadgesOutsideBlock(lines []string, blockStart, blockEnd int, relPath string, findings *[]Finding) {
	var fence util.MarkdownFence
	inComment := false
	for i := 0; i < len(lines) && i < MaxLines; i++ {
		trimmed := strings.TrimSpace(stripBlockquote(lines[i]))
		if fence.Inside(trimmed) || advanceCommentState(trimmed, &inComment) {
			continue
		}
		if blockStart >= 0 && i >= blockStart && i <= blockEnd {
			continue
		}
		if !strings.Contains(stripInlineComments(lines[i]), "style=for-the-badge") {
			continue
		}
		if strings.Contains(lines[i], "style=for-the-badge") {
			*findings = append(*findings, Finding{
				File:    relPath,
				Line:    i + 1,
				RuleID:  RuleIDBadgeBlock,
				Message: "style=for-the-badge badge must sit in the top contiguous badge block",
			})
		}
	}
}

func isIgnoredTaglinePrecursor(trimmed string) bool {
	if trimmed == "" || strings.HasPrefix(trimmed, "<!--") || trimmed == "---" {
		return true
	}
	lower := strings.ToLower(trimmed)
	for _, prefix := range [...]string{"</div>", "</header>", "</p>", "<p>", "<br", "<hr"} {
		if strings.HasPrefix(lower, prefix) {
			return true
		}
	}
	return false
}

func checkTagline(lines []string, blockEnd int, relPath string, findings *[]Finding) {
	if blockEnd < 0 {
		return
	}
	start := blockEnd + 1
	limit := min(len(lines), min(start+50, MaxLines))
	inComment := false
	for i := start; i < limit; i++ {
		trimmed := strings.TrimSpace(lines[i])
		if advanceCommentState(trimmed, &inComment) || isIgnoredTaglinePrecursor(trimmed) {
			continue
		}
		if !isBoldLine(trimmed) {
			*findings = append(*findings, Finding{
				File:    relPath,
				Line:    i + 1,
				RuleID:  RuleIDTagline,
				Message: "first non-empty text line after badge block must be a bold tagline",
			})
		}
		return
	}
	*findings = append(*findings, Finding{
		File:    relPath,
		Line:    blockEnd + 1,
		RuleID:  RuleIDTagline,
		Message: "first non-empty text line after badge block must be a bold tagline",
	})
}

func hasEnclosingTags(s, open, close string) bool {
	return strings.HasPrefix(s, open) && strings.HasSuffix(s, close) && len(s) >= len(open)+len(close)
}

func unwrapParagraph(s string) string {
	lower := strings.ToLower(s)
	if !strings.HasPrefix(lower, "<p") || !strings.HasSuffix(lower, "</p>") {
		return s
	}
	gt := strings.IndexByte(s, '>')
	if gt < 0 || gt+1 > len(s)-4 {
		return s
	}
	return strings.TrimSpace(s[gt+1 : len(s)-4])
}

func isBoldLine(trimmed string) bool {
	inner := unwrapParagraph(trimmed)
	tags := [...]struct{ open, close string }{
		{"**", "**"},
		{"__", "__"},
		{"<b>", "</b>"},
		{"<strong>", "</strong>"},
	}
	for _, t := range tags {
		if hasEnclosingTags(inner, t.open, t.close) {
			return true
		}
	}
	return false
}
