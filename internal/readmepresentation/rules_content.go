// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package readmepresentation

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/cordanaLLM/praetor/internal/readmegovernance"
	"github.com/cordanaLLM/praetor/internal/util"
)

var (
	mdSvgRe       = regexp.MustCompile(`!\[[^\]]*\]\(([^)]+\.svg(?:\?[^)]*)?)\)`)
	htmlSvgRe     = regexp.MustCompile(`(?i)<img[^>]+src=["']([^"']+\.svg(?:\?[^"']*)?)["']`)
	viewBoxRe     = regexp.MustCompile(`(?i)\bviewBox\s*=\s*["']([^"']+)["']`)
	svgTagWidthRe = regexp.MustCompile(`(?i)<svg[^>]+width\s*=\s*["']?([0-9.]+)`)
)

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
		(strings.Contains(trimmed, "theme") && strings.Contains(trimmed, "base"))
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

func checkSvgBelowHeader(rootDir, line, relPath string, lineNum int, findings *[]Finding) {
	targets := extractSvgTargets(line)
	for i := 0; i < len(targets) && i < maxSvgTargetsPerLine; i++ {
		target := targets[i]
		if isRemoteTarget(target) {
			continue
		}
		clean := cleanTargetSvgPath(target)
		fullPath := filepath.Join(rootDir, filepath.Clean(clean))
		data, err := util.ReadFileLimited(fullPath, MaxSVGBytes)
		if err != nil {
			*findings = append(*findings, Finding{
				File:    relPath,
				Line:    lineNum,
				RuleID:  RuleIDDiagrams,
				Message: fmt.Sprintf("SVG image %s cannot be read: %v", clean, err),
			})
			continue
		}
		if width, ok := parseSvgWidth(string(data)); ok && width > float64(SVGMaxWidth) {
			*findings = append(*findings, Finding{
				File:    relPath,
				Line:    lineNum,
				RuleID:  RuleIDDiagrams,
				Message: fmt.Sprintf("SVG image %s width %.0f exceeds column limit %d", clean, width, SVGMaxWidth),
			})
		}
	}
}

func extractSvgTargets(line string) []string {
	var targets []string
	if m := mdSvgRe.FindAllStringSubmatch(line, maxSvgTargetsPerLine); len(m) > 0 {
		for i := 0; i < len(m) && i < maxSvgTargetsPerLine; i++ {
			if len(m[i]) > 1 {
				targets = append(targets, m[i][1])
			}
		}
	}
	if m := htmlSvgRe.FindAllStringSubmatch(line, maxSvgTargetsPerLine); len(m) > 0 {
		for i := 0; i < len(m) && i < maxSvgTargetsPerLine; i++ {
			if len(m[i]) > 1 {
				targets = append(targets, m[i][1])
			}
		}
	}
	return targets
}

func isRemoteTarget(t string) bool {
	return strings.HasPrefix(t, "http://") || strings.HasPrefix(t, "https://") || strings.HasPrefix(t, "data:")
}

func cleanTargetSvgPath(t string) string {
	clean := strings.Split(t, "?")[0]
	return strings.Split(clean, "#")[0]
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

func checkSectionHeading(line string, lineNum int, relPath string, state *sectionState, findings *[]Finding) {
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
		*findings = append(*findings, Finding{
			File:    relPath,
			Line:    lineNum,
			RuleID:  RuleIDSections,
			Message: fmt.Sprintf("section %q appears after managed governance block (governance block must remain last)", clean),
		})
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
	governanceSeen bool
	seenH2         map[string]int
	lastClass      int
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
	{0, "get-started", []string{"install", "get-started", "get started"}},
	{1, "architecture", []string{"architecture", "at-glance", "at a glance"}},
	{2, "guides", []string{"guide", "reference"}},
	{3, "contribute", []string{"contribut"}},
	{4, "support", []string{"support"}},
	{5, "roadmap", []string{"roadmap", "release"}},
	{6, "upstream", []string{"licen", "upstream"}},
	{7, "governance", []string{"standard", "governance"}},
}

func matchHeadingKeywordClass(lower string) int {
	for i := 0; i < len(sectionKeywords); i++ {
		sk := sectionKeywords[i]
		for j := 0; j < len(sk.keywords) && j < 8; j++ {
			if strings.Contains(lower, sk.keywords[j]) {
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
