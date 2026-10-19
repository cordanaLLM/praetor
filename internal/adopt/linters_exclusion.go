// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package adopt

import (
	"fmt"
	"os"
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

var exclusionCommentPattern = regexp.MustCompile(
	`(?i)^\s*category\s+([ABCD])\s*,\s*(?:entered\s+(\d{4}-\d{2}-\d{2})\s*,\s*)?expires\s+(\d{4}-\d{2}-\d{2})\s*,\s*#(\d+)\s*$`,
)

type golangciExclusionRule struct {
	file       string
	line       int
	path       string
	pathExcept string
	linters    []string
	text       string
	source     string
	hasComment bool
	exception  config.Exception
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

func parseExclusionComment(comment string) (bool, config.Exception) {
	lines := strings.Split(comment, "\n")
	for i := 0; i < len(lines) && i < maxCommentLines; i++ {
		clean := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(lines[i]), "#"))
		matches := exclusionCommentPattern.FindStringSubmatch(clean)
		if len(matches) == 5 {
			return true, config.Exception{
				Rule:     config.ExceptionRuleDeclaredLinters,
				Tool:     "exclusion",
				Category: strings.ToUpper(matches[1]),
				Entered:  matches[2],
				Expires:  matches[3],
				Reason:   "#" + matches[4],
			}
		}
	}
	return false, config.Exception{}
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

func parseRuleItemNode(file string, item *yaml.Node) golangciExclusionRule {
	rule := golangciExclusionRule{file: file, line: item.Line}
	if item.Kind != yaml.MappingNode {
		return rule
	}
	for i := 0; i+1 < len(item.Content) && i < maxYamlMappingKeys; i += 2 {
		applyRuleField(&rule, item.Content[i].Value, item.Content[i+1])
	}
	rawComment := extractRuleComment(item)
	rule.hasComment, rule.exception = parseExclusionComment(rawComment)
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
	rules      []golangciExclusionRule
	warnUnused bool
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

func parseExclusionRulesSeq(fileName string, seq *yaml.Node) []golangciExclusionRule {
	if seq == nil || seq.Kind != yaml.SequenceNode {
		return nil
	}
	var rules []golangciExclusionRule
	for i := 0; i < len(seq.Content) && i < maxExclusionRules; i++ {
		rules = append(rules, parseRuleItemNode(fileName, seq.Content[i]))
	}
	return rules
}

func parseExclusionsNode(fileName string, node *yaml.Node) *golangciExclusionConfig {
	if node == nil {
		return &golangciExclusionConfig{}
	}
	warnNode := findYAMLMappingKey(node, "warn-unused")
	warnUnused := warnNode != nil && strings.EqualFold(warnNode.Value, "true")
	rules := parseExclusionRulesSeq(fileName, findYAMLMappingKey(node, "rules"))
	return &golangciExclusionConfig{rules: rules, warnUnused: warnUnused}
}

func readGolangciExclusionConfig(cfg *config.GolangciConfigFile) (*golangciExclusionConfig, error) {
	data, err := os.ReadFile(cfg.Path)
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
	return parseExclusionsNode(cfg.Name, findExclusionsMapping(root.Content[0])), nil
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
		if len(r.linters) == 0 {
			filtered = append(filtered, r)
			continue
		}
		if hasAnyDeclaredLinter(r.linters, declaredMap) {
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

func validateRulesExpiry(rules []golangciExclusionRule, today time.Time) ([]golangciExclusionRule, []string) {
	var valid []golangciExclusionRule
	var failures []string
	for i := 0; i < len(rules) && i < maxExclusionRules; i++ {
		r := rules[i]
		if !r.hasComment {
			failures = append(failures,
				formatExclusionFailure(r.file, r.line, r.linters, r.targetPath(), "missing category and expiry comment"))
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
	return valid, failures
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

func auditDeclaredLinterExclusions(opts DeclaredLintersOptions, declared []config.DeclaredLinter, today time.Time) ([]string, []string, error) {
	cfg, err := config.LocateGolangciConfig(opts.RootDir)
	if err != nil {
		return nil, nil, fmt.Errorf("[FAIL] %s: %w", declaredLintersGate, err)
	}
	if cfg == nil {
		return nil, nil, nil
	}
	if cfg.Format != "yaml" {
		return []string{"[SKIP] " + declaredLintersGate + ": exclusions not read (format)"}, nil, nil
	}
	parsedCfg, err := readGolangciExclusionConfig(cfg)
	if err != nil {
		return nil, nil, fmt.Errorf("[FAIL] %s: %w", declaredLintersGate, err)
	}
	rules := filterDeclaredExclusionRules(parsedCfg.rules, declared)
	if len(rules) == 0 {
		return nil, nil, nil
	}

	validRules, failureLines := validateRulesExpiry(rules, today)
	skipLines, staleFailures, err := auditStaleExclusions(validRules, parsedCfg.warnUnused, opts.LintLogPath)
	if err != nil {
		return nil, nil, err
	}
	failureLines = append(failureLines, staleFailures...)
	return skipLines, failureLines, nil
}
