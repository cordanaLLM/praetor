// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package adopt

import (
	"bytes"
	"cmp"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"go.yaml.in/yaml/v3"

	"github.com/cordanaLLM/praetor/internal/config"

	"github.com/cordanaLLM/praetor/internal/util"
)

// maxLintConfigBytes bounds the golangci-lint configuration the generator reads (HISS-02); the
// engine's own, generated exceptions included, is about 100 KiB.
const maxLintConfigBytes = 8 << 20

// maxLintReportBytes bounds the JSON report of a full lint run the generator reads.
const maxLintReportBytes = 256 << 20

const (
	// GeneratedExceptionsBegin marks the start of the generated exclusion rules block.
	GeneratedExceptionsBegin = "# praetor:generated-exceptions begin (praetorctl linters exceptions; never edit by hand)"
	// GeneratedExceptionsEnd marks the end of the generated exclusion rules block.
	GeneratedExceptionsEnd = "# praetor:generated-exceptions end"

	maxReportIssues  = 50000
	maxGolangciLines = 20000
	maxKeyPathDepth  = 16
)

// LinterPair is one (file, linter) finding target.
type LinterPair struct {
	File   string
	Linter string
}

// GenerateExceptionsOptions configures the exception generator and ratchet.
type GenerateExceptionsOptions struct {
	ConfigPath string
	ReportPath string
	Issue      int
	Category   string
	Today      time.Time
	Write      bool
	Stdout     io.Writer
	Stderr     io.Writer
}

// GenerateResult is the verdict returned by GenerateExceptions.
type GenerateResult struct {
	ExitCode int
	Message  string
	Written  bool
	Count    int
}

type golangciReport struct {
	Issues []golangciIssue `json:"Issues"`
}

type golangciIssue struct {
	FromLinter string           `json:"FromLinter"`
	Pos        golangciPosition `json:"Pos"`
}

type golangciPosition struct {
	Filename string `json:"Filename"`
}

type recordedExceptionRule struct {
	Pair      LinterPair
	Exception config.Exception
	Rule      golangciExclusionRule
}

type ratchetAction int

const (
	ratchetUnchanged ratchetAction = iota
	ratchetRenew
	ratchetNewFinding
	ratchetCountDidNotDrop
)

type ratchetVerdict struct {
	Action         ratchetAction
	NewPair        LinterPair
	EarliestExpiry string
}

// StripGolangciConfig writes .golangci.yml without the generated block and with
// run.relative-path-mode: gitroot and issues limits disabled.
func StripGolangciConfig(configPath, outPath string) error {
	if configPath == "" {
		configPath = ".golangci.yml"
	}
	data, err := util.ReadFileLimited(configPath, maxLintConfigBytes)
	if err != nil {
		return fmt.Errorf("read %s: %w", configPath, err)
	}
	content := string(data)
	stripped := stripGeneratedBlockLines(content)

	var node yaml.Node
	if err := yaml.Unmarshal([]byte(stripped), &node); err != nil {
		return fmt.Errorf("parse %s: %w", configPath, err)
	}
	if err := applyStrippedConfigDefaults(&node); err != nil {
		return err
	}
	outBytes, err := marshalYAMLNode(&node)
	if err != nil {
		return fmt.Errorf("marshal %s: %w", outPath, err)
	}
	if err := os.WriteFile(outPath, outBytes, 0o600); err != nil {
		return fmt.Errorf("write %s: %w", outPath, err)
	}
	return nil
}

func applyStrippedConfigDefaults(root *yaml.Node) error {
	if err := setScalar(root, "gitroot", "run", "relative-path-mode"); err != nil {
		return fmt.Errorf("set run.relative-path-mode: %w", err)
	}
	if err := setScalar(root, 0, "issues", "max-issues-per-linter"); err != nil {
		return fmt.Errorf("set issues.max-issues-per-linter: %w", err)
	}
	if err := setScalar(root, 0, "issues", "max-same-issues"); err != nil {
		return fmt.Errorf("set issues.max-same-issues: %w", err)
	}
	if err := setScalar(root, false, "issues", "uniq-by-line"); err != nil {
		return fmt.Errorf("set issues.uniq-by-line: %w", err)
	}
	return nil
}

func stripGeneratedBlockLines(content string) string {
	lines := strings.Split(content, "\n")
	var result []string
	inBlock := false
	for i := 0; i < len(lines) && i < maxGolangciLines; i++ {
		line := lines[i]
		trimmed := strings.TrimSpace(line)
		if trimmed == GeneratedExceptionsBegin {
			inBlock = true
			continue
		}
		if trimmed == GeneratedExceptionsEnd {
			inBlock = false
			continue
		}
		if inBlock {
			continue
		}
		result = append(result, line)
	}
	return strings.Join(result, "\n")
}

func createScalarNode(value any) (*yaml.Node, error) {
	switch v := value.(type) {
	case string:
		switch v {
		case "false":
			return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!bool", Value: "false"}, nil
		case "true":
			return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!bool", Value: "true"}, nil
		case "0":
			return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!int", Value: "0"}, nil
		default:
			return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: v}, nil
		}
	case bool:
		valStr := "false"
		if v {
			valStr = "true"
		}
		return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!bool", Value: valStr}, nil
	case int:
		return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!int", Value: fmt.Sprintf("%d", v)}, nil
	default:
		var n yaml.Node
		if err := n.Encode(value); err != nil {
			return nil, fmt.Errorf("encode scalar: %w", err)
		}
		return &n, nil
	}
}

func rootMappingNode(root *yaml.Node) (*yaml.Node, error) {
	if root == nil {
		return nil, errors.New("root node is nil")
	}
	if root.Kind == yaml.DocumentNode {
		if len(root.Content) == 0 {
			root.Content = append(root.Content, &yaml.Node{
				Kind: yaml.MappingNode,
				Tag:  "!!map",
			})
		}
		if root.Content[0].Kind != yaml.MappingNode {
			root.Content[0] = &yaml.Node{
				Kind: yaml.MappingNode,
				Tag:  "!!map",
			}
		}
		return root.Content[0], nil
	}
	if root.Kind != yaml.MappingNode {
		root.Kind = yaml.MappingNode
		root.Tag = "!!map"
		root.Content = nil
	}
	return root, nil
}

func findMappingKeyIndex(node *yaml.Node, key string) int {
	for i := 0; i+1 < len(node.Content) && i < maxYamlMappingKeys; i += 2 {
		if node.Content[i].Value == key {
			return i
		}
	}
	return -1
}

func descendMappingKey(curr *yaml.Node, key string) *yaml.Node {
	idx := findMappingKeyIndex(curr, key)
	if idx != -1 {
		next := curr.Content[idx+1]
		if next.Kind != yaml.MappingNode {
			next.Kind = yaml.MappingNode
			next.Tag = "!!map"
			next.Content = nil
			next.Value = ""
		}
		return next
	}
	keyNode := &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key}
	next := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	curr.Content = append(curr.Content, keyNode, next)
	return next
}

func applyMappingScalar(curr *yaml.Node, key string, valNode *yaml.Node) {
	idx := findMappingKeyIndex(curr, key)
	if idx != -1 {
		curr.Content[idx+1] = valNode
		return
	}
	keyNode := &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key}
	curr.Content = append(curr.Content, keyNode, valNode)
}

func setScalar(root *yaml.Node, value any, keys ...string) error {
	if len(keys) == 0 {
		return errors.New("setScalar: at least one key is required")
	}
	curr, err := rootMappingNode(root)
	if err != nil {
		return err
	}
	valNode, err := createScalarNode(value)
	if err != nil {
		return err
	}
	for k := 0; k < len(keys)-1 && k < maxKeyPathDepth; k++ {
		curr = descendMappingKey(curr, keys[k])
	}
	applyMappingScalar(curr, keys[len(keys)-1], valNode)
	return nil
}

func marshalYAMLNode(node *yaml.Node) ([]byte, error) {
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(node); err != nil {
		return nil, fmt.Errorf("encode yaml: %w", err)
	}
	if err := enc.Close(); err != nil {
		return nil, fmt.Errorf("close yaml encoder: %w", err)
	}
	return buf.Bytes(), nil
}

func readEnabledLinters(configPath string) map[string]bool {
	data, err := util.ReadFileLimited(configPath, maxLintConfigBytes)
	if err != nil {
		return nil
	}
	var root yaml.Node
	if err := yaml.Unmarshal(data, &root); err != nil || len(root.Content) == 0 {
		return nil
	}
	lintersNode := findYAMLMappingKey(root.Content[0], "linters")
	if lintersNode == nil {
		return nil
	}
	enableNode := findYAMLMappingKey(lintersNode, "enable")
	if enableNode == nil || enableNode.Kind != yaml.SequenceNode {
		return nil
	}
	enabled := make(map[string]bool, len(enableNode.Content))
	for i := 0; i < len(enableNode.Content) && i < maxLintersPerRule; i++ {
		val := strings.TrimSpace(enableNode.Content[i].Value)
		if val != "" {
			enabled[strings.ToLower(val)] = true
		}
	}
	return enabled
}

func cleanIssueFilename(name string) string {
	clean := filepath.ToSlash(filepath.Clean(name))
	clean = strings.TrimPrefix(clean, "./")
	clean = strings.TrimPrefix(clean, "/")
	return clean
}

func readReportPairs(reportPath, configPath string) ([]LinterPair, error) {
	data, err := util.ReadFileLimited(reportPath, maxLintReportBytes)
	if err != nil {
		return nil, fmt.Errorf("read report %s: %w", reportPath, err)
	}
	var rep golangciReport
	if err := json.Unmarshal(data, &rep); err != nil {
		return nil, fmt.Errorf("parse report %s: %w", reportPath, err)
	}

	enabled := readEnabledLinters(configPath)
	pairSet := make(map[LinterPair]bool)
	var pairs []LinterPair

	for i := 0; i < len(rep.Issues) && i < maxReportIssues; i++ {
		issue := rep.Issues[i]
		linter := strings.TrimSpace(issue.FromLinter)
		if linter == "" {
			continue
		}
		if len(enabled) > 0 && !enabled[strings.ToLower(linter)] {
			continue
		}
		file := cleanIssueFilename(issue.Pos.Filename)
		if file == "" {
			continue
		}
		p := LinterPair{File: file, Linter: linter}
		if !pairSet[p] {
			pairSet[p] = true
			pairs = append(pairs, p)
		}
	}

	slices.SortFunc(pairs, func(a, b LinterPair) int {
		if c := cmp.Compare(a.File, b.File); c != 0 {
			return c
		}
		return cmp.Compare(a.Linter, b.Linter)
	})
	return pairs, nil
}

func unquoteRulePath(path string) string {
	trimmed := strings.TrimPrefix(path, "^")
	trimmed = strings.TrimSuffix(trimmed, "$")
	return strings.ReplaceAll(trimmed, `\.`, `.`)
}

func parseBlockRules(configPath string, blockLines []string) []recordedExceptionRule {
	if len(blockLines) == 0 {
		return nil
	}
	wrapped := "rules:\n" + strings.Join(blockLines, "\n")
	var root yaml.Node
	if err := yaml.Unmarshal([]byte(wrapped), &root); err != nil || len(root.Content) == 0 {
		return nil
	}
	rulesSeq := findYAMLMappingKey(root.Content[0], "rules")
	rules := parseExclusionRulesSeq(configPath, rulesSeq)
	var recorded []recordedExceptionRule
	for i := 0; i < len(rules) && i < maxExclusionRules; i++ {
		r := rules[i]
		linter := ""
		if len(r.linters) > 0 {
			linter = r.linters[0]
		}
		recorded = append(recorded, recordedExceptionRule{
			Pair:      LinterPair{File: unquoteRulePath(r.path), Linter: linter},
			Exception: r.exception,
			Rule:      r,
		})
	}
	return recorded
}

func locateGeneratedBlock(lines []string) (int, int) {
	beginLine, endLine := -1, -1
	for i := 0; i < len(lines) && i < maxGolangciLines; i++ {
		trimmed := strings.TrimSpace(lines[i])
		if trimmed == GeneratedExceptionsBegin {
			beginLine = i
		}
		if trimmed == GeneratedExceptionsEnd && beginLine != -1 {
			endLine = i
			break
		}
	}
	return beginLine, endLine
}

func readExistingGeneratedBlock(configPath string) ([]recordedExceptionRule, int, int, error) {
	data, err := util.ReadFileLimited(configPath, maxLintConfigBytes)
	if err != nil {
		return nil, -1, -1, fmt.Errorf("read %s: %w", configPath, err)
	}
	lines := strings.Split(string(data), "\n")
	beginLine, endLine := locateGeneratedBlock(lines)
	if beginLine == -1 || endLine == -1 {
		return nil, -1, -1, nil
	}
	blockLines := lines[beginLine+1 : endLine]
	return parseBlockRules(configPath, blockLines), beginLine, endLine, nil
}

func findNewFinding(recordedMap map[LinterPair]recordedExceptionRule, measured []LinterPair) (LinterPair, bool) {
	for i := 0; i < len(measured) && i < maxExclusionRules; i++ {
		if _, ok := recordedMap[measured[i]]; !ok {
			return measured[i], true
		}
	}
	return LinterPair{}, false
}

func checkRecordedExpiry(recorded []recordedExceptionRule, today time.Time) (bool, string) {
	hasExpired := false
	earliestExpiry := ""
	for i := 0; i < len(recorded) && i < maxExclusionRules; i++ {
		r := recorded[i]
		if r.Exception.Expired(today) {
			hasExpired = true
		}
		if earliestExpiry == "" || r.Exception.Expires < earliestExpiry {
			earliestExpiry = r.Exception.Expires
		}
	}
	return hasExpired, earliestExpiry
}

func evaluateRatchet(recorded []recordedExceptionRule, measured []LinterPair, today time.Time) ratchetVerdict {
	recordedMap := make(map[LinterPair]recordedExceptionRule, len(recorded))
	for i := 0; i < len(recorded) && i < maxExclusionRules; i++ {
		recordedMap[recorded[i].Pair] = recorded[i]
	}
	if newPair, found := findNewFinding(recordedMap, measured); found {
		return ratchetVerdict{Action: ratchetNewFinding, NewPair: newPair}
	}
	if len(measured) < len(recorded) {
		return ratchetVerdict{Action: ratchetRenew}
	}
	hasExpired, earliest := checkRecordedExpiry(recorded, today)
	if hasExpired {
		return ratchetVerdict{Action: ratchetCountDidNotDrop}
	}
	return ratchetVerdict{Action: ratchetUnchanged, EarliestExpiry: earliest}
}

func formatGeneratedBlock(pairs []LinterPair, category string, today time.Time, issue int) (string, error) {
	days, ok := config.ExceptionCategoryDays(category)
	if !ok {
		return "", fmt.Errorf("invalid category %q: must be A, B, C or D", category)
	}
	entered := today.Format(config.ExceptionDateLayout)
	expires := today.AddDate(0, 0, days).Format(config.ExceptionDateLayout)

	var b strings.Builder
	b.WriteString("      " + GeneratedExceptionsBegin + "\n")
	for i := 0; i < len(pairs) && i < maxExclusionRules; i++ {
		p := pairs[i]
		fmt.Fprintf(&b, "      # category %s, entered %s, expires %s, #%d\n", category, entered, expires, issue)
		fmt.Fprintf(&b, "      - path: ^%s$\n", regexp.QuoteMeta(p.File))
		b.WriteString("        linters:\n")
		fmt.Fprintf(&b, "          - %s\n", p.Linter)
	}
	b.WriteString("      " + GeneratedExceptionsEnd)
	return b.String(), nil
}

func findRulesInsertionIndex(lines []string) int {
	rulesIdx := -1
	rulesIndent := 0
	for i := 0; i < len(lines) && i < maxGolangciLines; i++ {
		line := lines[i]
		trimmed := strings.TrimSpace(line)
		if trimmed == "rules:" {
			rulesIdx = i
			rulesIndent = len(line) - len(strings.TrimLeft(line, " "))
			break
		}
	}
	if rulesIdx == -1 {
		return len(lines)
	}
	for i := rulesIdx + 1; i < len(lines) && i < maxGolangciLines; i++ {
		line := lines[i]
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		currIndent := len(line) - len(strings.TrimLeft(line, " "))
		if currIndent <= rulesIndent {
			return i
		}
	}
	return len(lines)
}

func writeGeneratedBlockToFile(configPath, block string, beginLine, endLine int) error {
	data, err := util.ReadFileLimited(configPath, maxLintConfigBytes)
	if err != nil {
		return fmt.Errorf("read %s: %w", configPath, err)
	}
	lines := strings.Split(string(data), "\n")
	var newLines []string
	if beginLine != -1 && endLine != -1 {
		newLines = append(newLines, lines[:beginLine]...)
		newLines = append(newLines, block)
		newLines = append(newLines, lines[endLine+1:]...)
	} else {
		insertIdx := findRulesInsertionIndex(lines)
		newLines = append(newLines, lines[:insertIdx]...)
		newLines = append(newLines, block)
		newLines = append(newLines, lines[insertIdx:]...)
	}
	result := strings.Join(newLines, "\n")
	var node yaml.Node
	if err := yaml.Unmarshal([]byte(result), &node); err != nil {
		return fmt.Errorf("result config is not valid YAML: %w", err)
	}
	return util.WriteFileAtomic(configPath, []byte(result), 0o644)
}

func resolveGenerateDefaults(opts *GenerateExceptionsOptions) (string, time.Time, error) {
	if opts.ConfigPath == "" {
		opts.ConfigPath = ".golangci.yml"
	}
	if opts.ReportPath == "" {
		return "", time.Time{}, errors.New("--report is required")
	}
	if opts.Issue <= 0 {
		return "", time.Time{}, errors.New("--issue is required")
	}
	category := opts.Category
	if category == "" {
		category = "C"
	}
	category = strings.ToUpper(category)
	today := opts.Today
	if today.IsZero() {
		today = time.Now()
	}
	return category, today, nil
}

func emitResult(out io.Writer, msg string) error {
	if out != nil && msg != "" {
		if _, err := fmt.Fprintln(out, msg); err != nil {
			return fmt.Errorf("write output: %w", err)
		}
	}
	return nil
}

// GenerateExceptions evaluates the measured report against existing exclusions and applies the ratchet.
func GenerateExceptions(opts GenerateExceptionsOptions) (GenerateResult, error) {
	category, today, err := resolveGenerateDefaults(&opts)
	if err != nil {
		return GenerateResult{ExitCode: 1, Message: err.Error()}, err
	}
	measured, err := readReportPairs(opts.ReportPath, opts.ConfigPath)
	if err != nil {
		return GenerateResult{ExitCode: 1, Message: err.Error()}, err
	}
	recorded, beginLine, endLine, err := readExistingGeneratedBlock(opts.ConfigPath)
	if err != nil {
		return GenerateResult{ExitCode: 1, Message: err.Error()}, err
	}
	if len(recorded) == 0 {
		return handleFirstRunGenerate(opts, measured, category, today, beginLine, endLine)
	}
	verdict := evaluateRatchet(recorded, measured, today)
	return handleRatchetVerdict(opts, recorded, measured, verdict, category, today, beginLine, endLine)
}

func handleFirstRunGenerate(opts GenerateExceptionsOptions, measured []LinterPair, category string, today time.Time, beginLine, endLine int) (GenerateResult, error) {
	block, err := formatGeneratedBlock(measured, category, today, opts.Issue)
	if err != nil {
		return GenerateResult{ExitCode: 1, Message: err.Error()}, err
	}
	if opts.Write {
		if err := writeGeneratedBlockToFile(opts.ConfigPath, block, beginLine, endLine); err != nil {
			return GenerateResult{ExitCode: 1, Message: err.Error()}, err
		}
		return GenerateResult{ExitCode: 0, Written: true, Count: len(measured)}, nil
	}
	if err := emitResult(opts.Stdout, block); err != nil {
		return GenerateResult{ExitCode: 1, Message: err.Error()}, err
	}
	return GenerateResult{ExitCode: 0, Written: false, Count: len(measured)}, nil
}

func handleRatchetRenew(opts GenerateExceptionsOptions, measured []LinterPair, category string, today time.Time, beginLine, endLine int) (GenerateResult, error) {
	block, err := formatGeneratedBlock(measured, category, today, opts.Issue)
	if err != nil {
		return GenerateResult{ExitCode: 1, Message: err.Error()}, err
	}
	if opts.Write {
		if err := writeGeneratedBlockToFile(opts.ConfigPath, block, beginLine, endLine); err != nil {
			return GenerateResult{ExitCode: 1, Message: err.Error()}, err
		}
		return GenerateResult{ExitCode: 0, Written: true, Count: len(measured)}, nil
	}
	if err := emitResult(opts.Stdout, block); err != nil {
		return GenerateResult{ExitCode: 1, Message: err.Error()}, err
	}
	return GenerateResult{ExitCode: 0, Written: false, Count: len(measured)}, nil
}

func handleRatchetVerdict(opts GenerateExceptionsOptions, recorded []recordedExceptionRule, measured []LinterPair,
	verdict ratchetVerdict, category string, today time.Time, beginLine, endLine int) (GenerateResult, error) {
	switch verdict.Action {
	case ratchetNewFinding:
		msg := fmt.Sprintf("new finding in %s (%s): fix it, a generated exception never grows", verdict.NewPair.File, verdict.NewPair.Linter)
		if err := emitResult(opts.Stderr, msg); err != nil {
			return GenerateResult{ExitCode: 1, Message: err.Error()}, err
		}
		return GenerateResult{ExitCode: 3, Message: msg}, nil
	case ratchetRenew:
		return handleRatchetRenew(opts, measured, category, today, beginLine, endLine)
	case ratchetUnchanged:
		msg := fmt.Sprintf("unchanged: %d entries live until %s", len(recorded), verdict.EarliestExpiry)
		if err := emitResult(opts.Stdout, msg); err != nil {
			return GenerateResult{ExitCode: 1, Message: err.Error()}, err
		}
		return GenerateResult{ExitCode: 0, Message: msg, Written: false, Count: len(recorded)}, nil
	case ratchetCountDidNotDrop:
		msg := fmt.Sprintf("count did not drop: %d recorded, %d measured; renewal needs a lower count", len(recorded), len(measured))
		if err := emitResult(opts.Stderr, msg); err != nil {
			return GenerateResult{ExitCode: 1, Message: err.Error()}, err
		}
		return GenerateResult{ExitCode: 3, Message: msg}, nil
	default:
		return GenerateResult{ExitCode: 1, Message: "unknown ratchet action"}, nil
	}
}
