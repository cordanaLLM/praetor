// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package readmepresentation

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

var (
	imgHeightAttrRe    = regexp.MustCompile(`(?i)\bheight\s*=\s*["']?([0-9]+)["']?`)
	imgHeightStyleRe   = regexp.MustCompile(`(?i)height\s*:\s*([0-9]+)px`)
	heroWidthPercentRe = regexp.MustCompile(`(?i)(?:width\s*=\s*["']100%["']|width\s*:\s*100%)`)
	htmlCommentRe      = regexp.MustCompile(`<!--\s*(.*?)\s*-->`)
)

func isHeaderOpening(s string) bool {
	return strings.HasPrefix(s, "<div") || strings.HasPrefix(s, "<header")
}

func isHeaderClosing(s string) bool {
	return strings.HasPrefix(s, "</div>") || strings.HasPrefix(s, "</header>")
}

func findHeaderBounds(lines []string) (start, end int) {
	start, end = -1, -1
	n := len(lines)
	if n > MaxLines {
		n = MaxLines
	}
	for i := 0; i < n; i++ {
		trimmed := strings.TrimSpace(lines[i])
		if start < 0 {
			if isHeaderOpening(trimmed) || strings.HasPrefix(trimmed, "# ") {
				start = i
			}
			continue
		}
		if isHeaderClosing(trimmed) {
			end = i
			break
		}
	}
	if start >= 0 && end < 0 {
		end = start + 50
		if end > len(lines) {
			end = len(lines)
		}
	}
	return start, end
}

func checkHeroBanner(lines []string, headerStart, headerEnd int, relPath string, findings *[]Finding) {
	if headerStart < 0 {
		return
	}
	for i := headerStart; i <= headerEnd && i < len(lines); i++ {
		line := lines[i]
		if !strings.Contains(line, "<img") {
			continue
		}
		if h, ok := parseHeroHeight(line); ok && h > HeroMaxHeight {
			*findings = append(*findings, Finding{
				File:    relPath,
				Line:    i + 1,
				RuleID:  RuleIDHero,
				Message: fmt.Sprintf("hero banner height %d exceeds maximum height %d", h, HeroMaxHeight),
			})
		}
		if heroWidthPercentRe.MatchString(line) {
			*findings = append(*findings, Finding{
				File:    relPath,
				Line:    i + 1,
				RuleID:  RuleIDHero,
				Message: `hero banner width="100%" is not permitted`,
			})
		}
	}
}

func parseHeroHeight(line string) (int, bool) {
	if m := imgHeightAttrRe.FindStringSubmatch(line); len(m) > 1 {
		if val, err := strconv.Atoi(m[1]); err == nil {
			return val, true
		}
	}
	if m := imgHeightStyleRe.FindStringSubmatch(line); len(m) > 1 {
		if val, err := strconv.Atoi(m[1]); err == nil {
			return val, true
		}
	}
	return 0, false
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
	for _, prefix := range [...]string{"<!--", "<img", "![", "<picture", "<source", "</picture>", "<h1", "<a ", "</a>"} {
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
	{"licen", 5},
	{"support", 6},
}

func matchNamedBadgeGroup(lower string) (int, bool) {
	for _, bg := range badgeGroupPrefixes {
		if strings.HasPrefix(lower, bg.prefix) {
			return bg.rank, true
		}
	}
	return 0, false
}

func matchBadgeGroupRank(text string) (int, bool) {
	if len(text) > 0 && text[0] >= '1' && text[0] <= '6' {
		return int(text[0] - '0'), true
	}
	return matchNamedBadgeGroup(strings.ToLower(text))
}

func checkBadgesOutsideBlock(lines []string, blockStart, blockEnd int, relPath string, findings *[]Finding) {
	for i := 0; i < len(lines) && i < MaxLines; i++ {
		if blockStart >= 0 && i >= blockStart && i <= blockEnd {
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
	if trimmed == "" || strings.HasPrefix(trimmed, "<!--") {
		return true
	}
	return strings.HasPrefix(trimmed, "</div>") || strings.HasPrefix(trimmed, "</header>")
}

func checkTagline(lines []string, blockEnd int, relPath string, findings *[]Finding) {
	if blockEnd < 0 {
		return
	}
	start := blockEnd + 1
	limit := len(lines)
	if limit > start+50 {
		limit = start + 50
	}
	if limit > MaxLines {
		limit = MaxLines
	}
	for i := start; i < limit; i++ {
		trimmed := strings.TrimSpace(lines[i])
		if isIgnoredTaglinePrecursor(trimmed) {
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

func isBoldLine(trimmed string) bool {
	tags := [...]struct{ open, close string }{
		{"**", "**"},
		{"__", "__"},
		{"<b>", "</b>"},
		{"<strong>", "</strong>"},
	}
	for _, t := range tags {
		if hasEnclosingTags(trimmed, t.open, t.close) {
			return true
		}
	}
	return false
}
