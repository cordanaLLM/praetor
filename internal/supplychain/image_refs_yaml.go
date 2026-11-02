// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package supplychain

import (
	"cmp"
	"path/filepath"
	"slices"
	"strings"

	"github.com/cordanaLLM/praetor/internal/ghworkflow"
	"github.com/cordanaLLM/praetor/internal/util"
	"go.yaml.in/yaml/v3"
)

// isYAMLFile reports whether the file path has a .yml or .yaml extension.
func isYAMLFile(slashed string) bool {
	ext := strings.ToLower(filepath.Ext(slashed))
	return ext == ".yml" || ext == ".yaml"
}

// isWorkflowFile reports whether the path points to a GitHub Actions workflow definition.
func isWorkflowFile(slashed string) bool {
	return strings.HasPrefix(slashed, ".github/workflows/") ||
		strings.Contains(slashed, "/.github/workflows/") ||
		strings.Contains(slashed, "github-actions/")
}

// collectYAMLFindings inspects YAML content and returns findings for unpinned container image references.
func collectYAMLFindings(file, content string) []ImageRefFinding {
	if isWorkflowFile(file) {
		return collectWorkflowFindings(file, content)
	}
	lines := strings.Split(content, "\n")
	var findings []ImageRefFinding
	for i := 0; i < len(lines) && i < maxFileLines; i++ {
		findings = appendImageRefFinding(findings, file, i+1, extractYAMLImageRef(lines[i]))
	}
	return findings
}

// collectWorkflowFindings parses a GitHub Actions workflow document and returns findings for container, service, and action image references.
func collectWorkflowFindings(file, content string) []ImageRefFinding {
	spec, err := ghworkflow.Parse([]byte(content))
	if err != nil {
		return []ImageRefFinding{newImageRefFinding(file, 0, err.Error(), true)}
	}
	ids := ghworkflow.SortedJobIDs(spec.Jobs)
	var findings []ImageRefFinding
	for i := 0; i < len(ids) && i < ghworkflow.MaxJobsPerFile; i++ {
		job := spec.Jobs[ids[i]]
		findings = appendWorkflowContainerFinding(findings, file, &job.Container)
		findings = appendWorkflowServiceFindings(findings, file, &job.Services)
		findings = appendWorkflowStepFindings(findings, file, job.Steps)
	}
	slices.SortStableFunc(findings, func(a, b ImageRefFinding) int {
		return cmp.Compare(a.Line, b.Line)
	})
	return findings
}

// appendWorkflowContainerFinding checks the container field of a workflow job node and appends unpinned findings.
func appendWorkflowContainerFinding(findings []ImageRefFinding, file string, container *yaml.Node) []ImageRefFinding {
	switch container.Kind {
	case yaml.ScalarNode:
		if container.ShortTag() != "!!null" {
			return appendImageRefFinding(findings, file, container.Line, container.Value)
		}
	case yaml.MappingNode:
		if img := util.YAMLMappingValue(container, "image"); img != nil && img.Kind == yaml.ScalarNode && img.ShortTag() != "!!null" {
			return appendImageRefFinding(findings, file, img.Line, img.Value)
		}
	}
	return findings
}

// appendWorkflowServiceFindings checks service definitions in a workflow job node and appends unpinned container findings.
func appendWorkflowServiceFindings(findings []ImageRefFinding, file string, services *yaml.Node) []ImageRefFinding {
	if services.Kind != yaml.MappingNode {
		return findings
	}
	for i := 0; i+1 < len(services.Content) && i < maxFileLines; i += 2 {
		if img := util.YAMLMappingValue(services.Content[i+1], "image"); img != nil && img.Kind == yaml.ScalarNode && img.ShortTag() != "!!null" {
			findings = appendImageRefFinding(findings, file, img.Line, img.Value)
		}
	}
	return findings
}

// appendWorkflowStepFindings checks action step uses clauses and appends findings for docker:// references.
func appendWorkflowStepFindings(findings []ImageRefFinding, file string, steps []ghworkflow.Step) []ImageRefFinding {
	for i := 0; i < len(steps) && i < maxFileLines; i++ {
		uses := strings.TrimSpace(steps[i].Uses)
		if strings.HasPrefix(uses, "docker://") {
			findings = appendImageRefFinding(findings, file, steps[i].UsesLine, uses)
		}
	}
	return findings
}

// extractYAMLImageRef parses an image key from a single YAML line and returns the cleaned scalar reference.
func extractYAMLImageRef(line string) string {
	trimmed := strings.TrimSpace(line)
	if strings.HasPrefix(trimmed, "- ") {
		trimmed = strings.TrimSpace(strings.TrimPrefix(trimmed, "- "))
	}
	if trimmed == "" || strings.HasPrefix(trimmed, "#") {
		return ""
	}
	key, rest, ok := strings.Cut(trimmed, ":")
	if !ok || strings.TrimSpace(key) != "image" {
		return ""
	}
	return validYAMLScalarRef(cleanYAMLScalar(rest))
}

// cleanYAMLScalar strips surrounding quotes and inline comments from a YAML scalar value.
func cleanYAMLScalar(raw string) string {
	s := strings.TrimSpace(raw)
	if len(s) >= 2 && (s[0] == '"' || s[0] == '\'') {
		if end := closingQuoteIndex(s); end > 0 {
			return strings.TrimSpace(s[1:end])
		}
	}
	return strings.Trim(strings.TrimSpace(stripInlineComment(s)), `"'`)
}

// closingQuoteIndex finds the matching closing quote index in string s, respecting escape characters.
func closingQuoteIndex(s string) int {
	quote := s[0]
	for i := 1; i < len(s) && i < maxFileLines; i++ {
		if quote == '"' && s[i] == '\\' {
			i++
			continue
		}
		if s[i] == quote {
			return i
		}
	}
	return -1
}

// validYAMLScalarRef validates that a scalar string is not an empty or structural YAML literal.
func validYAMLScalarRef(val string) string {
	if val == "" || val == "|" || val == ">" || val == "{}" || val == "[]" || strings.HasSuffix(val, ":") {
		return ""
	}
	return val
}

// stripInlineComment removes trailing inline comments starting with a space and hash character.
func stripInlineComment(s string) string {
	if idx := strings.Index(s, " #"); idx >= 0 {
		return s[:idx]
	}
	return s
}
