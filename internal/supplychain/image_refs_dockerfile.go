// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package supplychain

import (
	"path/filepath"
	"strings"
)

// isDockerfile reports whether the file path matches Dockerfile or Containerfile naming conventions.
func isDockerfile(slashed string) bool {
	base := filepath.Base(slashed)
	lower := strings.ToLower(base)
	lower = strings.TrimSuffix(lower, ".tmpl")
	return lower == "dockerfile" || lower == "containerfile" ||
		strings.HasPrefix(lower, "dockerfile.") || strings.HasPrefix(lower, "containerfile.") ||
		strings.HasSuffix(lower, ".dockerfile") || strings.HasSuffix(lower, ".containerfile")
}

// collectDockerfileFindings parses FROM instructions in Dockerfile content and returns findings for unpinned images.
func collectDockerfileFindings(file, content string) []ImageRefFinding {
	stmts := dockerfileStatements(content)
	stages := dockerfileStages(stmts)
	var findings []ImageRefFinding
	for i := 0; i < len(stmts) && i < maxFileLines; i++ {
		target, _ := parseDockerfileFrom(stmts[i].text)
		if target == "" || strings.EqualFold(target, "scratch") || stages[strings.ToLower(target)] {
			continue
		}
		findings = appendImageRefFinding(findings, file, stmts[i].line, target)
	}
	return findings
}

// dockerfileStages collects stage names defined in multi-stage Dockerfile FROM statements.
func dockerfileStages(stmts []numberedLine) map[string]bool {
	stages := make(map[string]bool, len(stmts))
	for i := 0; i < len(stmts) && i < maxFileLines; i++ {
		if target, stage := parseDockerfileFrom(stmts[i].text); target != "" && stage != "" {
			stages[strings.ToLower(stage)] = true
		}
	}
	return stages
}

// dockerfileStatements splits Dockerfile content into logical statements, joining line continuations.
func dockerfileStatements(content string) []numberedLine {
	raw := strings.Split(content, "\n")
	var out []numberedLine
	startLine := 0
	var current strings.Builder
	for i := 0; i < len(raw) && i < maxFileLines; i++ {
		trimmed := strings.TrimSpace(raw[i])
		if strings.HasPrefix(trimmed, "#") {
			continue
		}
		if current.Len() == 0 {
			if trimmed == "" {
				continue
			}
			startLine = i + 1
		}
		if strings.HasSuffix(trimmed, `\`) {
			current.WriteString(strings.TrimSpace(strings.TrimSuffix(trimmed, `\`)) + " ")
			continue
		}
		current.WriteString(trimmed)
		out = append(out, numberedLine{line: startLine, text: current.String()})
		current.Reset()
	}
	if current.Len() > 0 {
		out = append(out, numberedLine{line: startLine, text: current.String()})
	}
	return out
}

// parseDockerfileFrom parses a Dockerfile FROM statement, returning the base image target and optional stage name.
func parseDockerfileFrom(stmt string) (target, stage string) {
	fields := strings.Fields(stripInlineComment(stmt))
	if len(fields) < 2 || !strings.EqualFold(fields[0], "FROM") {
		return "", ""
	}
	idx := 1
	for idx < len(fields) && strings.HasPrefix(fields[idx], "--") {
		idx++
	}
	if idx >= len(fields) {
		return "", ""
	}
	target = fields[idx]
	for j := idx + 1; j+1 < len(fields) && j < maxLineLiterals; j++ {
		if strings.EqualFold(fields[j], "AS") {
			stage = fields[j+1]
			break
		}
	}
	return target, stage
}
