// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package supplychain

import (
	"cmp"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/cordanaLLM/praetor/internal/ghworkflow"
	"github.com/cordanaLLM/praetor/internal/util"
	"go.yaml.in/yaml/v3"
)

// yamlFlowImageKey matches an `image:` key preceded by a YAML flow delimiter (`{`, `[`, `,`).
var yamlFlowImageKey = regexp.MustCompile(`[{\[,]\s*image\s*:`)

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
	coveredLines := make(map[int]bool, len(lines))
	var findings []ImageRefFinding
	for i := 0; i < len(lines) && i < maxFileLines; i++ {
		if ref := extractYAMLImageRef(lines[i]); ref != "" {
			coveredLines[i+1] = true
			findings = appendImageRefFinding(findings, file, i+1, ref)
			continue
		}
		if nextLine, ref := extractMultilineYAMLImageRef(lines, i); ref != "" {
			coveredLines[nextLine] = true
			findings = appendImageRefFinding(findings, file, nextLine, ref)
			continue
		}
		refs := extractYAMLFlowImageRefs(lines[i])
		for j := 0; j < len(refs) && j < maxLineLiterals; j++ {
			coveredLines[i+1] = true
			findings = appendImageRefFinding(findings, file, i+1, refs[j])
		}
	}
	findings = appendYAMLNodeFindings(findings, file, content, coveredLines)
	slices.SortStableFunc(findings, func(a, b ImageRefFinding) int {
		return cmp.Compare(a.Line, b.Line)
	})
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

// extractYAMLFlowImageRefs extracts scalar image references from flow mappings on a single YAML line.
func extractYAMLFlowImageRefs(line string) []string {
	matches := yamlFlowImageKey.FindAllStringIndex(line, maxLineLiterals)
	var refs []string
	for i := 0; i < len(matches) && i < maxLineLiterals; i++ {
		rest := line[matches[i][1]:]
		if ref := validYAMLScalarRef(cleanYAMLFlowScalar(rest)); ref != "" {
			refs = append(refs, ref)
		}
	}
	return refs
}

// extractMultilineYAMLImageRef checks whether lines[idx] is a bare `image:` key whose scalar
// value sits on the next non-comment line, returning its 1-based line number and reference.
func extractMultilineYAMLImageRef(lines []string, idx int) (int, string) {
	trimmed := strings.TrimSpace(stripInlineComment(lines[idx]))
	trimmed = strings.TrimSpace(strings.TrimPrefix(trimmed, "- "))
	if trimmed != "image:" || idx+1 >= len(lines) {
		return 0, ""
	}
	nextRaw := strings.TrimSpace(stripInlineComment(lines[idx+1]))
	if nextRaw == "" || strings.HasPrefix(nextRaw, "#") || strings.HasPrefix(nextRaw, "- ") {
		return 0, ""
	}
	if nextRaw[0] != '"' && nextRaw[0] != '\'' && strings.Contains(nextRaw, ": ") {
		return 0, ""
	}
	return idx + 2, validYAMLScalarRef(cleanYAMLScalar(nextRaw))
}

// cleanYAMLFlowScalar strips quotes, trailing flow delimiters, and inline comments from a flow mapping scalar.
func cleanYAMLFlowScalar(raw string) string {
	s := strings.TrimSpace(raw)
	if len(s) >= 2 && (s[0] == '"' || s[0] == '\'') {
		if end := closingQuoteIndex(s); end > 0 {
			return strings.TrimSpace(s[1:end])
		}
	}
	s = trimFlowClauseEnd(stripInlineComment(s))
	return strings.Trim(strings.TrimSpace(s), `"'`)
}

// trimFlowClauseEnd truncates an unquoted YAML flow scalar at the first top-level comma or closing delimiter.
func trimFlowClauseEnd(s string) string {
	depth := 0
	for i := 0; i < len(s) && i < maxFileLines; i++ {
		switch s[i] {
		case '{', '[':
			depth++
		case '}', ']':
			if depth == 0 {
				return s[:i]
			}
			depth--
		case ',':
			if depth == 0 {
				return s[:i]
			}
		}
	}
	return s
}

// appendYAMLNodeFindings walks parsed YAML mapping nodes iteratively to catch multiline or nested
// flow image fields not already covered by single-line extraction (HISS-01, HISS-02).
func appendYAMLNodeFindings(findings []ImageRefFinding, file, content string, coveredLines map[int]bool) []ImageRefFinding {
	var root yaml.Node
	if err := yaml.Unmarshal([]byte(content), &root); err != nil {
		return findings
	}
	stack := []*yaml.Node{&root}
	for steps := 0; len(stack) > 0 && steps < maxFileLines; steps++ {
		node := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if node == nil {
			continue
		}
		if node.Kind == yaml.MappingNode {
			findings = appendYAMLMappingImageFindings(findings, file, node, coveredLines)
		}
		for i := len(node.Content) - 1; i >= 0 && len(stack) < maxFileLines; i-- {
			stack = append(stack, node.Content[i])
		}
	}
	return findings
}

// appendYAMLMappingImageFindings inspects a YAML mapping node for scalar image keys not yet covered on their line.
func appendYAMLMappingImageFindings(findings []ImageRefFinding, file string, mapping *yaml.Node, coveredLines map[int]bool) []ImageRefFinding {
	for i := 0; i+1 < len(mapping.Content) && i < maxFileLines; i += 2 {
		keyNode := mapping.Content[i]
		valNode := mapping.Content[i+1]
		if keyNode.Kind != yaml.ScalarNode || keyNode.Value != "image" || !isInspectableYAMLScalar(valNode) {
			continue
		}
		if coveredLines[valNode.Line] {
			continue
		}
		coveredLines[valNode.Line] = true
		findings = appendImageRefFinding(findings, file, valNode.Line, validYAMLScalarRef(strings.TrimSpace(valNode.Value)))
	}
	return findings
}

// isInspectableYAMLScalar reports whether a YAML node is a non-null, non-block scalar value.
func isInspectableYAMLScalar(node *yaml.Node) bool {
	if node == nil || node.Kind != yaml.ScalarNode || node.ShortTag() == "!!null" {
		return false
	}
	return node.Style != yaml.LiteralStyle && node.Style != yaml.FoldedStyle
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
