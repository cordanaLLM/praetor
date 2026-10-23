// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package readmepresentation

import (
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"unicode"

	"github.com/cordanaLLM/praetor/internal/readmegovernance"
	"github.com/cordanaLLM/praetor/internal/util"
)

var (
	mdSvgRe            = regexp.MustCompile(`(?i)!\[[^\]]*\]\(\s*<?([^)>\s"']+\.svg)(?:[?#][^)>\s"']*)?>?(?:\s+["'][^"']*["'])?\s*\)`)
	htmlSvgRe          = regexp.MustCompile(`(?i)<(?:img|source)\b[^>]*?(?:[\s"'/])src\s*=\s*["']([^"']+\.svg(?:[?#][^"']*)?)["']`)
	htmlSrcsetRe       = regexp.MustCompile(`(?i)<(?:img|source)\b[^>]*?(?:[\s"'/])srcset\s*=\s*["']([^"']+)["']`)
	viewBoxRe          = regexp.MustCompile(`(?i)\bviewBox\s*=\s*["']([^"']+)["']`)
	svgTagWidthRe      = regexp.MustCompile(`(?i)<svg[^>]+width\s*=\s*["']?([0-9.]+)`)
	mermaidThemeBaseRe = regexp.MustCompile(`(?i)['"]?theme['"]?\s*:\s*['"]?base['"]?\b`)
	nonWordRe          = regexp.MustCompile(`[^a-z0-9]+`)
)

func stripBlockquote(line string) string {
	s := line
	for depth := 0; depth < 16; depth++ {
		trimmed := strings.TrimLeft(s, " ")
		if len(s)-len(trimmed) > 3 || !strings.HasPrefix(trimmed, ">") {
			break
		}
		s = strings.TrimPrefix(trimmed[1:], " ")
	}
	return s
}

func leadingSpaces(line string) int {
	width := 0
	for i := 0; i < len(line) && i < 16; i++ {
		switch line[i] {
		case ' ':
			width++
		case '\t':
			width += 4
		default:
			return width
		}
	}
	return width
}

func stripInlineComments(line string) string {
	if !strings.Contains(line, "<!--") {
		return line
	}
	return htmlCommentRe.ReplaceAllString(line, "")
}

func stripInlineCode(line string) string {
	if !strings.Contains(line, "`") {
		return line
	}
	spans := util.MarkdownCodeSpans(line, 64)
	if len(spans) == 0 {
		return line
	}
	var b strings.Builder
	b.Grow(len(line))
	pos := 0
	for i := 0; i < len(spans); i++ {
		b.WriteString(line[pos:spans[i].Start])
		pos = spans[i].End
	}
	b.WriteString(line[pos:])
	return b.String()
}

func sanitizeDisplayPath(s string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return '?'
		}
		return r
	}, s)
}

func checkMermaidBlock(firstLine string, lineNum int, relPath string, findings *[]Finding) {
	trimmed := strings.TrimSpace(firstLine)
	if !strings.HasPrefix(trimmed, "%%{init:") {
		*findings = append(*findings, Finding{
			File:    relPath,
			Line:    lineNum,
			RuleID:  RuleIDDiagrams,
			Message: "mermaid diagram must begin with %%{init: line holding 'theme': 'base' and themeVariables",
		})
		return
	}
	hasThemeBase := strings.Contains(trimmed, "'theme': 'base'") ||
		strings.Contains(trimmed, `"theme": "base"`) ||
		mermaidThemeBaseRe.MatchString(trimmed)
	hasThemeVars := strings.Contains(trimmed, "themeVariables")
	if !hasThemeBase || !hasThemeVars {
		*findings = append(*findings, Finding{
			File:    relPath,
			Line:    lineNum,
			RuleID:  RuleIDDiagrams,
			Message: "mermaid diagram init line must hold 'theme': 'base' and themeVariables",
		})
	}
}

const maxSvgTargetsPerLine = 32

func describeSvgReadFailure(safeClean string, err error) string {
	switch {
	case errors.Is(err, util.ErrPathEscapesRoot):
		return fmt.Sprintf("SVG image %s escapes repository root", safeClean)
	case errors.Is(err, util.ErrAbsoluteRelPath):
		return fmt.Sprintf("SVG image %s must be a relative path", safeClean)
	case errors.Is(err, fs.ErrNotExist):
		return fmt.Sprintf("SVG image %s does not exist", safeClean)
	case errors.Is(err, util.ErrNotRegularFile):
		return fmt.Sprintf("SVG image %s is not a regular file", safeClean)
	case errors.Is(err, util.ErrFileTooLarge):
		return fmt.Sprintf("SVG image %s exceeds maximum size (%d bytes)", safeClean, MaxSVGBytes)
	case errors.Is(err, fs.ErrPermission):
		return fmt.Sprintf("SVG image %s cannot be read (permission denied)", safeClean)
	default:
		return fmt.Sprintf("SVG image %s cannot be read", safeClean)
	}
}

func hasWindowsDrivePrefix(s string) bool {
	return len(s) >= 2 && ((s[0] >= 'a' && s[0] <= 'z') || (s[0] >= 'A' && s[0] <= 'Z')) && s[1] == ':'
}

func readConfinedSvg(rootDir, clean string) ([]byte, error) {
	if hasWindowsDrivePrefix(clean) {
		return nil, fmt.Errorf("%w: %q", util.ErrAbsoluteRelPath, clean)
	}
	return util.ReadConfinedLimited(rootDir, clean, MaxSVGBytes)
}

func checkSvgBelowHeader(rootDir, line, relPath string, lineNum int, findings *[]Finding) {
	targets := extractSvgTargets(line)
	for i := 0; i < len(targets) && i < maxSvgTargetsPerLine; i++ {
		target := targets[i]
		if isRemoteTarget(target) {
			continue
		}
		clean := cleanTargetSvgPath(target)
		safeClean := sanitizeDisplayPath(clean)
		data, err := readConfinedSvg(rootDir, clean)
		if err != nil {
			*findings = append(*findings, Finding{
				File:    relPath,
				Line:    lineNum,
				RuleID:  RuleIDDiagrams,
				Message: describeSvgReadFailure(safeClean, err),
			})
			continue
		}
		if width, ok := parseSvgWidth(string(data)); ok && width > float64(SVGMaxWidth) {
			*findings = append(*findings, Finding{
				File:    relPath,
				Line:    lineNum,
				RuleID:  RuleIDDiagrams,
				Message: fmt.Sprintf("SVG image %s width %.0f exceeds column limit %d", safeClean, width, SVGMaxWidth),
			})
		}
	}
}

func appendSubmatchTargets(dst []string, matches [][]string) []string {
	for i := 0; i < len(matches) && i < maxSvgTargetsPerLine; i++ {
		if len(matches[i]) > 1 {
			dst = append(dst, matches[i][1])
		}
	}
	return dst
}

func appendSrcsetSvgTargets(dst []string, matches [][]string) []string {
	for i := 0; i < len(matches) && i < maxSvgTargetsPerLine; i++ {
		if len(matches[i]) < 2 {
			continue
		}
		parts := strings.Split(matches[i][1], ",")
		for j := 0; j < len(parts) && j < maxSvgTargetsPerLine; j++ {
			fields := strings.Fields(parts[j])
			if len(fields) > 0 && strings.HasSuffix(strings.ToLower(cleanTargetSvgPath(fields[0])), ".svg") {
				dst = append(dst, fields[0])
			}
		}
	}
	return dst
}

func extractSvgTargets(line string) []string {
	if len(line) > 8192 || !strings.Contains(strings.ToLower(line), ".svg") {
		return nil
	}
	var targets []string
	if m := mdSvgRe.FindAllStringSubmatch(line, maxSvgTargetsPerLine); len(m) > 0 {
		targets = appendSubmatchTargets(targets, m)
	}
	if m := htmlSvgRe.FindAllStringSubmatch(line, maxSvgTargetsPerLine); len(m) > 0 {
		targets = appendSubmatchTargets(targets, m)
	}
	if m := htmlSrcsetRe.FindAllStringSubmatch(line, maxSvgTargetsPerLine); len(m) > 0 {
		targets = appendSrcsetSvgTargets(targets, m)
	}
	return targets
}

func isRemoteTarget(t string) bool {
	lower := strings.ToLower(strings.TrimSpace(t))
	return strings.HasPrefix(lower, "http://") ||
		strings.HasPrefix(lower, "https://") ||
		strings.HasPrefix(lower, "//") ||
		strings.HasPrefix(lower, "data:")
}

func cleanTargetSvgPath(t string) string {
	clean := strings.TrimSpace(t)
	clean = strings.Split(clean, "?")[0]
	clean = strings.Split(clean, "#")[0]
	if unescaped, err := url.PathUnescape(clean); err == nil {
		clean = unescaped
	}
	clean = strings.ReplaceAll(clean, `\`, "/")
	return strings.TrimLeft(clean, "/")
}

func parseSvgWidth(content string) (float64, bool) {
	if m := viewBoxRe.FindStringSubmatch(content); len(m) > 1 {
		fields := strings.Fields(strings.ReplaceAll(m[1], ",", " "))
		if len(fields) >= 4 {
			if w, err := strconv.ParseFloat(fields[2], 64); err == nil {
				return w, true
			}
		}
	}
	if m := svgTagWidthRe.FindStringSubmatch(content); len(m) > 1 {
		if w, err := strconv.ParseFloat(m[1], 64); err == nil {
			return w, true
		}
	}
	return 0, false
}

func checkSectionHeading(line string, lineNum int, relPath string, state *sectionState, findings, infos *[]Finding) {
	trimmed := strings.TrimSpace(line)
	if strings.Contains(trimmed, readmegovernance.Start) {
		state.governanceSeen = true
	}
	text, ok := util.MarkdownHeadingText(trimmed)
	if !ok || !strings.HasPrefix(trimmed, "## ") {
		return
	}
	clean := strings.TrimSpace(text)
	if state.governanceSeen {
		if !state.governanceInfoEmitted && infos != nil {
			state.governanceInfoEmitted = true
			*infos = append(*infos, Finding{
				File:    relPath,
				Line:    lineNum,
				RuleID:  RuleIDSections,
				Message: fmt.Sprintf("section %q appears after managed governance block (governance block must remain last)", clean),
			})
		}
	}
	if prev, exists := state.seenH2[clean]; exists {
		*findings = append(*findings, Finding{
			File:    relPath,
			Line:    lineNum,
			RuleID:  RuleIDSections,
			Message: fmt.Sprintf("duplicate H2 heading %q (previously at line %d)", clean, prev),
		})
	} else {
		state.seenH2[clean] = lineNum
	}
	checkHeadingKeywordOrder(clean, lineNum, relPath, state, findings)
}

type sectionState struct {
	governanceSeen        bool
	governanceInfoEmitted bool
	seenH2                map[string]int
	lastClass             int
}

func newSectionState() *sectionState {
	return &sectionState{
		seenH2:    make(map[string]int),
		lastClass: -1,
	}
}

var sectionKeywords = [...]struct {
	class    int
	name     string
	keywords []string
}{
	{0, "get-started", []string{"install", "installation", "get started", "getting started", "quick start", "quickstart"}},
	{1, "architecture", []string{"architecture", "at a glance"}},
	{2, "guides", []string{"guide", "guides", "reference"}},
	{3, "contribute", []string{"contribute", "contributing", "contribution", "contributions"}},
	{4, "support", []string{"support"}},
	{5, "roadmap", []string{"roadmap", "roadmap and releases"}},
	{6, "upstream", []string{"license", "licence", "upstream"}},
	{7, "governance", []string{"governance", "standards and governance"}},
}

func normalizeHeadingWords(lower string) string {
	s := strings.ReplaceAll(lower, "&", " and ")
	s = strings.TrimSpace(nonWordRe.ReplaceAllString(s, " "))
	if s == "" {
		return ""
	}
	return " " + s + " "
}

func matchesHeadingKeyword(padded, kw string) bool {
	target := " " + kw + " "
	return strings.HasPrefix(padded, target) || (kw == "at a glance" && strings.HasSuffix(padded, target))
}

func matchHeadingKeywordClass(lower string) int {
	padded := normalizeHeadingWords(lower)
	if padded == "" {
		return -1
	}
	for i := 0; i < len(sectionKeywords); i++ {
		sk := sectionKeywords[i]
		for j := 0; j < len(sk.keywords) && j < 8; j++ {
			if matchesHeadingKeyword(padded, sk.keywords[j]) {
				return sk.class
			}
		}
	}
	return -1
}

func checkHeadingKeywordOrder(clean string, lineNum int, relPath string, state *sectionState, findings *[]Finding) {
	matchedClass := matchHeadingKeywordClass(strings.ToLower(clean))
	if matchedClass < 0 {
		return
	}
	if matchedClass < state.lastClass {
		*findings = append(*findings, Finding{
			File:    relPath,
			Line:    lineNum,
			RuleID:  RuleIDSections,
			Message: fmt.Sprintf("section %q is out of order (keyword class %q appears after %q)", clean, sectionKeywords[matchedClass].name, sectionKeywords[state.lastClass].name),
		})
	} else {
		state.lastClass = matchedClass
	}
}
