// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package adopt

import (
	"fmt"
	"regexp"
	"strings"
	"time"

	"go.yaml.in/yaml/v3"

	"github.com/cordanaLLM/praetor/internal/config"

	"github.com/cordanaLLM/praetor/internal/util"
)

const (
	maxExclusionRules  = 2048
	maxYamlMappingKeys = 256
	maxCommentLines    = 64
	maxLintersPerRule  = 128
	maxDeclaredLinters = 256
)

var (
	exclusionCommentPattern = regexp.MustCompile(
		`(?i)^\s*category\s+([ABCD])\s*,\s*(?:entered\s+(\d{4}-\d{2}-\d{2})\s*,\s*)?expires\s+(\d{4}-\d{2}-\d{2})\s*,\s*#(\d+)\s*$`,
	)
	baselineCommentPattern = regexp.MustCompile(
		`(?i)^\s*baseline\s+#(\d+)\s*$`,
	)
)

type golangciExclusionRule struct {
	file             string
	line             int
	path             string
	pathExcept       string
	linters          []string
	text             string
	source           string
	hasComment       bool
	isBaseline       bool
	issue            string
	inGeneratedBlock bool
	exception        config.Exception
}

func (r golangciExclusionRule) targetPath() string {
	if r.path != "" {
		return r.path
	}
	if r.pathExcept != "" {
		return "except " + r.pathExcept
	}
	return ""
}

func (r golangciExclusionRule) String() string {
	var parts []string
	if r.text != "" {
		parts = append(parts, fmt.Sprintf("Text: %q", r.text))
	}
	if r.source != "" {
		parts = append(parts, fmt.Sprintf("Source: %q", r.source))
	}
	if r.path != "" {
		parts = append(parts, fmt.Sprintf("Path: %q", r.path))
	}
	if r.pathExcept != "" {
		parts = append(parts, fmt.Sprintf("Path Except: %q", r.pathExcept))
	}
	if len(r.linters) > 0 {
		parts = append(parts, fmt.Sprintf("Linters: %q", strings.Join(r.linters, ", ")))
	}
	return strings.Join(parts, ", ")
}

type parsedExclusionComment struct {
	valid      bool
	isBaseline bool
	issue      string
	exception  config.Exception
}

func isGeneratedBlockMarker(trimmed string) bool {
	return trimmed == GeneratedExceptionsBegin || trimmed == GeneratedExceptionsEnd
}

func parseExclusionCommentLine(line string) parsedExclusionComment {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" || isGeneratedBlockMarker(trimmed) {
		return parsedExclusionComment{}
	}
	clean := strings.TrimSpace(strings.TrimPrefix(trimmed, "#"))
	if matches := exclusionCommentPattern.FindStringSubmatch(clean); len(matches) == 5 {
		return parsedExclusionComment{
			valid: true,
			exception: config.Exception{
				Rule:     config.ExceptionRuleDeclaredLinters,
				Tool:     "exclusion",
				Category: strings.ToUpper(matches[1]),
				Entered:  matches[2],
				Expires:  matches[3],
				Reason:   "#" + matches[4],
			},
		}
	}
	if matches := baselineCommentPattern.FindStringSubmatch(clean); len(matches) == 2 {
		return parsedExclusionComment{
			valid:      true,
			isBaseline: true,
			issue:      "#" + matches[1],
		}
	}
	return parsedExclusionComment{}
}

func parseExclusionComment(comment string, inGeneratedBlock bool) parsedExclusionComment {
	lines := strings.Split(comment, "\n")
	var found parsedExclusionComment
	for i := 0; i < len(lines) && i < maxCommentLines; i++ {
		trimmed := strings.TrimSpace(lines[i])
		if trimmed == "" || isGeneratedBlockMarker(trimmed) {
			continue
		}
		parsed := parseExclusionCommentLine(trimmed)
		if !parsed.valid {
			if inGeneratedBlock {
				return parsedExclusionComment{}
			}
			continue
		}
		found = parsed
	}
	return found
}

func extractRuleComment(item *yaml.Node) string {
	if item.HeadComment != "" {
		return item.HeadComment
	}
	if item.LineComment != "" {
		return item.LineComment
	}
	if item.Kind == yaml.MappingNode {
		for i := 0; i < len(item.Content) && i < maxCommentLines; i++ {
			child := item.Content[i]
			if child.Line == item.Line && child.LineComment != "" {
				return child.LineComment
			}
		}
	}
	return ""
}

func applyRuleField(rule *golangciExclusionRule, key string, valNode *yaml.Node) {
	switch key {
	case "path":
		rule.path = valNode.Value
	case "path-except":
		rule.pathExcept = valNode.Value
	case "text":
		rule.text = valNode.Value
	case "source":
		rule.source = valNode.Value
	case "linters":
		rule.linters = parseLintersNode(valNode)
	}
}

func parseLintersNode(valNode *yaml.Node) []string {
	if valNode.Kind == yaml.ScalarNode && valNode.Value != "" {
		return []string{valNode.Value}
	}
	if valNode.Kind == yaml.SequenceNode {
		var list []string
		for k := 0; k < len(valNode.Content) && k < maxLintersPerRule; k++ {
			if valNode.Content[k].Value != "" {
				list = append(list, valNode.Content[k].Value)
			}
		}
		return list
	}
	return nil
}

func parseRuleItemNode(file string, item *yaml.Node, inGeneratedBlock bool) golangciExclusionRule {
	rule := golangciExclusionRule{file: file, line: item.Line, inGeneratedBlock: inGeneratedBlock}
	if item.Kind != yaml.MappingNode {
		return rule
	}
	for i := 0; i+1 < len(item.Content) && i < maxYamlMappingKeys; i += 2 {
		applyRuleField(&rule, item.Content[i].Value, item.Content[i+1])
	}
	rawComment := extractRuleComment(item)
	parsed := parseExclusionComment(rawComment, inGeneratedBlock)
	rule.hasComment = parsed.valid
	rule.isBaseline = parsed.isBaseline
	rule.issue = parsed.issue
	rule.exception = parsed.exception
	return rule
}

func findYAMLMappingKey(node *yaml.Node, key string) *yaml.Node {
	for i := 0; i+1 < len(node.Content) && i < maxYamlMappingKeys; i += 2 {
		if node.Content[i].Value == key {
			return node.Content[i+1]
		}
	}
	return nil
}

type golangciExclusionConfig struct {
	rules         []golangciExclusionRule
	warnUnused    bool
	blockFailures []string
}

func findExclusionsMapping(doc *yaml.Node) *yaml.Node {
	if doc == nil || doc.Kind != yaml.MappingNode {
		return nil
	}
	lintersNode := findYAMLMappingKey(doc, "linters")
	if lintersNode == nil || lintersNode.Kind != yaml.MappingNode {
		return nil
	}
	exclusionsNode := findYAMLMappingKey(lintersNode, "exclusions")
	if exclusionsNode == nil || exclusionsNode.Kind != yaml.MappingNode {
		return nil
	}
	return exclusionsNode
}

func isLineInGeneratedBlock(line1Based, beginLine, endLine int) bool {
	if beginLine == -1 || endLine == -1 {
		return false
	}
	return line1Based > beginLine+1 && line1Based <= endLine+1
}

func parseExclusionRulesSeq(fileName string, seq *yaml.Node, beginLine, endLine int) []golangciExclusionRule {
	if seq == nil || seq.Kind != yaml.SequenceNode {
		return nil
	}
	var rules []golangciExclusionRule
	for i := 0; i < len(seq.Content) && i < maxExclusionRules; i++ {
		item := seq.Content[i]
		inBlock := isLineInGeneratedBlock(item.Line, beginLine, endLine)
		rules = append(rules, parseRuleItemNode(fileName, item, inBlock))
	}
	return rules
}

func parseExclusionsNode(fileName string, node *yaml.Node, beginLine, endLine int) *golangciExclusionConfig {
	if node == nil {
		return &golangciExclusionConfig{}
	}
	warnNode := findYAMLMappingKey(node, "warn-unused")
	warnUnused := warnNode != nil && strings.EqualFold(warnNode.Value, "true")
	rules := parseExclusionRulesSeq(fileName, findYAMLMappingKey(node, "rules"), beginLine, endLine)
	return &golangciExclusionConfig{rules: rules, warnUnused: warnUnused}
}

func validateGeneratedBlockComments(fileName string, lines []string, beginLine, endLine int) []string {
	if beginLine == -1 && endLine == -1 {
		return nil
	}
	if beginLine == -1 || endLine == -1 {
		return []string{formatExclusionFailure(fileName, 1, nil, "", "unclosed generated-exceptions block")}
	}
	var failures []string
	for i := beginLine + 1; i < endLine && i < maxGolangciLines; i++ {
		trimmed := strings.TrimSpace(lines[i])
		if !strings.HasPrefix(trimmed, "#") {
			continue
		}
		if !parseExclusionCommentLine(trimmed).valid {
			failures = append(failures,
				formatExclusionFailure(fileName, i+1, nil, "", fmt.Sprintf("unrecognised comment in generated block: %s", trimmed)))
		}
	}
	return failures
}

func readGolangciExclusionConfig(cfg *config.GolangciConfigFile) (*golangciExclusionConfig, error) {
	data, err := util.ReadFileLimited(cfg.Path, maxLintConfigBytes)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", cfg.Name, err)
	}
	var root yaml.Node
	if err := yaml.Unmarshal(data, &root); err != nil {
		return nil, fmt.Errorf("parse %s: %w", cfg.Name, err)
	}
	if len(root.Content) == 0 {
		return &golangciExclusionConfig{}, nil
	}
	lines := strings.Split(string(data), "\n")
	beginLine, endLine := locateGeneratedBlock(lines)
	parsed := parseExclusionsNode(cfg.Name, findExclusionsMapping(root.Content[0]), beginLine, endLine)
	parsed.blockFailures = validateGeneratedBlockComments(cfg.Name, lines, beginLine, endLine)
	return parsed, nil
}

func hasAnyDeclaredLinter(linters []string, declaredMap map[string]bool) bool {
	for j := 0; j < len(linters) && j < maxLintersPerRule; j++ {
		if declaredMap[strings.ToLower(linters[j])] {
			return true
		}
	}
	return false
}

func filterDeclaredExclusionRules(rules []golangciExclusionRule, declared []config.DeclaredLinter) []golangciExclusionRule {
	declaredMap := make(map[string]bool, len(declared))
	for i := 0; i < len(declared) && i < maxDeclaredLinters; i++ {
		declaredMap[strings.ToLower(declared[i].Name)] = true
	}
	var filtered []golangciExclusionRule
	for i := 0; i < len(rules) && i < maxExclusionRules; i++ {
		r := rules[i]
		if len(r.linters) == 0 || r.inGeneratedBlock || hasAnyDeclaredLinter(r.linters, declaredMap) {
			filtered = append(filtered, r)
		}
	}
	return filtered
}

func checkStaleExclusionRules(rules []golangciExclusionRule, logPath string) ([]string, error) {
	data, err := util.ReadFileLimited(logPath, maxLintReportBytes)
	if err != nil {
		return nil, fmt.Errorf("read lint log %s: %w", logPath, err)
	}
	content := string(data)
	var staleFailures []string
	for i := 0; i < len(rules) && i < maxExclusionRules; i++ {
		r := rules[i]
		ruleStr := r.String()
		needle := fmt.Sprintf("Skipped 0 issues by rules: [%s]", ruleStr)
		needleEscaped := fmt.Sprintf("Skipped 0 issues by rules: [%s]", strings.ReplaceAll(ruleStr, `"`, `\"`))
		if strings.Contains(content, needle) || strings.Contains(content, needleEscaped) {
			staleFailures = append(staleFailures,
				formatExclusionFailure(r.file, r.line, r.linters, r.targetPath(), "matches no finding: remove it"))
		}
	}
	return staleFailures, nil
}

func formatExclusionFailure(file string, line int, linters []string, path, problem string) string {
	lintersStr := strings.Join(linters, ", ")
	if lintersStr == "" {
		lintersStr = "all"
	}
	return fmt.Sprintf("[FAIL] %s: %s:%d: linters: %s, path: %s: %s",
		declaredLintersGate, file, line, lintersStr, path, problem)
}

func validateBaselineRule(r golangciExclusionRule) string {
	if !r.inGeneratedBlock {
		return "baseline comment is only valid inside generated block"
	}
	if len(r.linters) != 1 || unquoteRuleText(r.linters[0], r.text) == "" {
		return "baseline rule requires complexity linter and function text regexp"
	}
	return ""
}

func validateRulesExpiry(rules []golangciExclusionRule, today time.Time) ([]golangciExclusionRule, int, []string) {
	var valid []golangciExclusionRule
	var failures []string
	baselineCount := 0
	for i := 0; i < len(rules) && i < maxExclusionRules; i++ {
		r := rules[i]
		if !r.hasComment {
			failures = append(failures,
				formatExclusionFailure(r.file, r.line, r.linters, r.targetPath(), "missing category and expiry comment"))
			continue
		}
		if r.isBaseline {
			if prob := validateBaselineRule(r); prob != "" {
				failures = append(failures, formatExclusionFailure(r.file, r.line, r.linters, r.targetPath(), prob))
				continue
			}
			baselineCount++
			valid = append(valid, r)
			continue
		}
		if r.exception.Expired(today) {
			failures = append(failures,
				formatExclusionFailure(r.file, r.line, r.linters, r.targetPath(), fmt.Sprintf("expired %s", r.exception.Expires)))
			continue
		}
		if prob := r.exception.Problem(today); prob != "" {
			failures = append(failures,
				formatExclusionFailure(r.file, r.line, r.linters, r.targetPath(), prob))
			continue
		}
		valid = append(valid, r)
	}
	return valid, baselineCount, failures
}

func auditStaleExclusions(rules []golangciExclusionRule, warnUnused bool, logPath string) ([]string, []string, error) {
	if !warnUnused {
		return []string{"[SKIP] " + declaredLintersGate + ": stale check not possible: linters.exclusions.warn-unused is off"}, nil, nil
	}
	if logPath == "" {
		return []string{"[SKIP] " + declaredLintersGate + ": stale check not run: no lint log given"}, nil, nil
	}
	staleFailures, err := checkStaleExclusionRules(rules, logPath)
	if err != nil {
		return nil, nil, fmt.Errorf("[FAIL] %s: %w", declaredLintersGate, err)
	}
	return nil, staleFailures, nil
}

func auditDeclaredLinterExclusions(opts DeclaredLintersOptions, declared []config.DeclaredLinter, today time.Time) ([]string, int, []string, error) {
	cfg, err := config.LocateGolangciConfig(opts.RootDir)
	if err != nil {
		return nil, 0, nil, fmt.Errorf("[FAIL] %s: %w", declaredLintersGate, err)
	}
	if cfg == nil {
		return nil, 0, nil, nil
	}
	if cfg.Format != "yaml" {
		return []string{"[SKIP] " + declaredLintersGate + ": exclusions not read (format)"}, 0, nil, nil
	}
	parsedCfg, err := readGolangciExclusionConfig(cfg)
	if err != nil {
		return nil, 0, nil, fmt.Errorf("[FAIL] %s: %w", declaredLintersGate, err)
	}
	rules := filterDeclaredExclusionRules(parsedCfg.rules, declared)
	if len(rules) == 0 && len(parsedCfg.blockFailures) == 0 {
		return nil, 0, nil, nil
	}

	validRules, baselineCount, failureLines := validateRulesExpiry(rules, today)
	failureLines = append(failureLines, parsedCfg.blockFailures...)
	if len(rules) == 0 {
		return nil, baselineCount, failureLines, nil
	}
	skipLines, staleFailures, err := auditStaleExclusions(validRules, parsedCfg.warnUnused, opts.LintLogPath)
	if err != nil {
		return nil, baselineCount, nil, err
	}
	failureLines = append(failureLines, staleFailures...)
	return skipLines, baselineCount, failureLines, nil
}
