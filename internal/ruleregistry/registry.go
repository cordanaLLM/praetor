// Package ruleregistry loads the declared enforcement classification for repository rules.
package ruleregistry

import (
	"context"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/cordanaLLM/praetor/internal/contextopt"
	"github.com/cordanaLLM/praetor/internal/hisscatalog"
	"go.yaml.in/yaml/v3"
)

const (
	// File is the enforcement registry path relative to a repository root.
	File                = ".config/rules/enforcement.yaml"
	maxOperationalRules = 64
	maxOperationalLines = 4096
	maxPointerLength    = 512
)

// Class describes how a rule is enforced.
type Class string

const (
	ClassCIGate     Class = "ci-gate"
	ClassHook       Class = "hook"
	ClassReviewGate Class = "review-gate"
	ClassGuidance   Class = "guidance"
	ClassStaged     Class = "staged"
)

// Entry is one rule's declared enforcement and its resolver pointers.
type Entry struct {
	ID       string   `yaml:"id" json:"id"`
	Class    Class    `yaml:"class" json:"class"`
	Evidence []string `yaml:"evidence" json:"evidence,omitempty"`
}

// Registry is the strict YAML document stored at File.
type Registry struct {
	Version int     `yaml:"version" json:"version"`
	Rules   []Entry `yaml:"rules" json:"rules"`
}

// Rule is known source text that audit can classify and inspect for prohibitions.
type Rule struct {
	ID   string
	Text string
}

var operationalItem = regexp.MustCompile(`^([1-9][0-9]?)\.\s+(.+)$`)

// Load reads and validates a registry from root.
func Load(ctx context.Context, root string) (*Registry, error) {
	data, err := readFile(ctx, root, File)
	if err != nil {
		return nil, fmt.Errorf("read rule registry: %w", err)
	}
	registry, err := decode(data)
	if err != nil {
		return nil, err
	}
	return registry, nil
}

// KnownRules returns the HISS catalog and the bounded Operational Rules list in ID order.
func KnownRules(ctx context.Context, root string) ([]Rule, error) {
	rules := hissRules()
	data, err := readFile(ctx, root, "AGENTS.md")
	if err != nil {
		return nil, fmt.Errorf("read AGENTS.md: %w", err)
	}
	operational, err := ParseOperationalRules(data)
	if err != nil {
		return nil, err
	}
	return append(rules, operational...), nil
}

// ParseOperationalRules extracts the numbered list below the exact Operational Rules heading.
func ParseOperationalRules(data []byte) ([]Rule, error) {
	lines := strings.Split(string(data), "\n")
	start := findOperationalHeading(lines)
	if start < 0 {
		return nil, errors.New("AGENTS.md: Operational Rules heading not found")
	}
	return parseOperationalList(lines[start+1:])
}

func decode(data []byte) (*Registry, error) {
	decoder := yaml.NewDecoder(strings.NewReader(string(data)))
	decoder.KnownFields(true)
	var registry Registry
	if err := decoder.Decode(&registry); err != nil {
		if errors.Is(err, io.EOF) {
			return nil, errors.New("rule registry: document is empty")
		}
		return nil, fmt.Errorf("decode rule registry: %w", err)
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return nil, errors.New("rule registry: exactly one document is required")
	}
	return &registry, nil
}

func hissRules() []Rule {
	ids := hisscatalog.RuleIDs()
	rules := make([]Rule, 0, len(ids))
	for i := 0; i < len(ids); i++ {
		rule, ok := hisscatalog.LookupRule(ids[i])
		if ok {
			rules = append(rules, Rule{ID: rule.ID, Text: rule.Specification})
		}
	}
	return rules
}

func findOperationalHeading(lines []string) int {
	for i := 0; i < len(lines) && i < maxOperationalLines; i++ {
		if strings.TrimSpace(lines[i]) == "## Operational Rules" {
			return i
		}
	}
	return -1
}

func parseOperationalList(lines []string) ([]Rule, error) {
	rules := []Rule{}
	current := -1
	for i := 0; i < len(lines) && i < maxOperationalLines; i++ {
		line := lines[i]
		if strings.HasPrefix(line, "## ") {
			break
		}
		match := operationalItem.FindStringSubmatch(line)
		if len(match) == 0 {
			appendOperationalText(rules, current, line)
			continue
		}
		if len(rules) == maxOperationalRules {
			return nil, fmt.Errorf("AGENTS.md: Operational Rules exceeds %d entries", maxOperationalRules)
		}
		want := len(rules) + 1
		if match[1] != fmt.Sprintf("%d", want) {
			return nil, fmt.Errorf("AGENTS.md: Operational Rules expected %d, got %s", want, match[1])
		}
		rules = append(rules, Rule{ID: "AGENTS-OP-" + match[1], Text: match[2]})
		current = len(rules) - 1
	}
	if len(rules) == 0 {
		return nil, errors.New("AGENTS.md: Operational Rules list is empty")
	}
	return rules, nil
}

func appendOperationalText(rules []Rule, current int, line string) {
	text := strings.TrimSpace(line)
	if current >= 0 && text != "" {
		rules[current].Text += "\n" + text
	}
}

func readFile(ctx context.Context, root, relative string) ([]byte, error) {
	if root == "" || filepath.IsAbs(relative) || !filepath.IsLocal(filepath.FromSlash(relative)) {
		return nil, errors.New("repository root and local relative path required")
	}
	return contextopt.ReadSnapshot(ctx, filepath.Join(root, filepath.FromSlash(relative)))
}

func validClass(class Class) bool {
	return class == ClassCIGate || class == ClassHook || class == ClassReviewGate || class == ClassGuidance || class == ClassStaged
}

// instructionOnlyPhrases is the allow-list of prohibition wording reported for conversion.
// Adding broad tokens here would turn ordinary descriptive prose into false positives.
var instructionOnlyPhrases = []string{"is told not to", "must not", "never"}

func prohibited(text string) bool {
	lower := strings.ToLower(text)
	for i := 0; i < len(instructionOnlyPhrases); i++ {
		if strings.Contains(lower, instructionOnlyPhrases[i]) {
			return true
		}
	}
	return false
}
