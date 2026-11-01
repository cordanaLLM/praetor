// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package supplychain

import (
	"path/filepath"
	"regexp"
	"strings"
)

var (
	// devcontainerImageField matches `"image": "..."` in devcontainer.json files.
	devcontainerImageField = regexp.MustCompile(`"image"\s*:\s*"([^"]+)"`)

	// quotedLiteralPattern matches double-quoted, single-quoted, or backtick string literals.
	quotedLiteralPattern = regexp.MustCompile(`"([^"\\\n]+)"|'([^'\\\n]+)'|` + "`" + `([^` + "`" + `\n]+)` + "`")
)

// sourceCodeExts are source extensions scanned for fully qualified image references.
var sourceCodeExts = map[string]bool{
	".go": true, ".py": true, ".rs": true,
	".c": true, ".h": true, ".cc": true, ".cpp": true, ".cxx": true, ".hpp": true, ".hh": true,
	".cu": true, ".hip": true, ".mm": true,
	".ts": true, ".tsx": true, ".js": true, ".jsx": true, ".mjs": true, ".cjs": true, ".svelte": true,
}

func isDevcontainerJSON(slashed string) bool {
	base := strings.ToLower(filepath.Base(slashed))
	return base == "devcontainer.json" || base == ".devcontainer.json"
}

func isSourceCodeFile(slashed string) bool {
	return sourceCodeExts[strings.ToLower(filepath.Ext(slashed))]
}

func collectDevcontainerFindings(file, content string) []ImageRefFinding {
	lines := strings.Split(content, "\n")
	var findings []ImageRefFinding
	for i := 0; i < len(lines) && i < maxFileLines; i++ {
		m := devcontainerImageField.FindStringSubmatch(lines[i])
		if len(m) < 2 {
			continue
		}
		findings = appendImageRefFinding(findings, file, i+1, m[1])
	}
	return findings
}

func collectSourceLiteralFindings(file, content string) []ImageRefFinding {
	folded := foldAdjacentStringLines(content)
	var findings []ImageRefFinding
	for i := 0; i < len(folded) && i < maxFileLines; i++ {
		trimmed := strings.TrimSpace(folded[i].text)
		if strings.HasPrefix(trimmed, "//") || strings.HasPrefix(trimmed, "#") || strings.HasPrefix(trimmed, "/*") || strings.HasPrefix(trimmed, "*") {
			continue
		}
		matches := quotedLiteralPattern.FindAllStringSubmatch(folded[i].text, maxLineLiterals)
		for j := 0; j < len(matches) && j < maxLineLiterals; j++ {
			lit := firstNonEmptySubmatch(matches[j][1:])
			if isUnpinnedQualifiedImageLiteral(lit) {
				findings = appendImageRefFinding(findings, file, folded[i].line, lit)
			}
		}
	}
	return findings
}

func firstNonEmptySubmatch(groups []string) string {
	for i := 0; i < len(groups); i++ {
		if groups[i] != "" {
			return groups[i]
		}
	}
	return ""
}

// foldAdjacentStringLines joins adjacent lines where a double-quoted string literal is split
// across lines via "+" or implicit concatenation, preserving the starting 1-based line number.
func foldAdjacentStringLines(content string) []numberedLine {
	raw := strings.Split(content, "\n")
	out := make([]numberedLine, 0, len(raw))
	for i := 0; i < len(raw) && i < maxFileLines; i++ {
		lineNum := i + 1
		combined := raw[i]
		for i+1 < len(raw) && i+1 < maxFileLines && continuesQuotedLiteral(combined, raw[i+1]) {
			combined = joinContinuedLiteral(combined, raw[i+1])
			i++
		}
		out = append(out, numberedLine{line: lineNum, text: combined})
	}
	return out
}

func continuesQuotedLiteral(curr, next string) bool {
	right := strings.TrimSpace(curr)
	left := strings.TrimSpace(next)
	if !strings.HasPrefix(left, `"`) {
		return false
	}
	if strings.HasSuffix(right, `+`) {
		right = strings.TrimSpace(strings.TrimSuffix(right, `+`))
	}
	return strings.HasSuffix(right, `"`)
}

func joinContinuedLiteral(curr, next string) string {
	right := strings.TrimSpace(curr)
	if strings.HasSuffix(right, `+`) {
		right = strings.TrimSpace(strings.TrimSuffix(right, `+`))
	}
	right = strings.TrimSuffix(right, `"`)
	left := strings.TrimPrefix(strings.TrimSpace(next), `"`)
	return right + left
}
