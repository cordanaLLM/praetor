// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package adopt

import (
	"os"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
)

// reviveRulesFirst is a configuration whose first `rules:` key is revive's settings, not the
// exclusion list the generated block belongs in.
const reviveRulesFirst = "version: \"2\"\nlinters:\n  enable:\n    - funlen\n    - revive\n" +
	"  settings:\n    revive:\n      rules:\n        - name: exported\n" +
	"  exclusions:\n    rules:\n      # category A, expires 2026-12-01, #100\n      - path: internal/hand/\n        linters:\n          - funlen\n" +
	"formatters:\n  enable:\n    - gofmt\n"

// sequenceLengths returns the lengths of linters.settings.revive.rules and
// linters.exclusions.rules in the configuration at path.
func sequenceLengths(t *testing.T, path string) (revive, exclusions int) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var config struct {
		Linters struct {
			Settings struct {
				Revive struct {
					Rules []yaml.Node `yaml:"rules"`
				} `yaml:"revive"`
			} `yaml:"settings"`
			Exclusions struct {
				Rules []yaml.Node `yaml:"rules"`
			} `yaml:"exclusions"`
		} `yaml:"linters"`
	}
	if err := yaml.Unmarshal(data, &config); err != nil {
		t.Fatalf("result is not YAML: %v\n%s", err, data)
	}
	return len(config.Linters.Settings.Revive.Rules), len(config.Linters.Exclusions.Rules)
}

func generateInto(t *testing.T, cfgContent string) (string, error) {
	t.Helper()
	cfgPath, repPath := writeTempConfigAndReport(t, cfgContent, []LinterPair{{File: "internal/pkg/a_test.go", Linter: "funlen", Function: "TestOversized"}})
	_, err := GenerateExceptions(GenerateExceptionsOptions{
		ConfigPath: cfgPath, ReportPath: repPath, Issue: 1030, Category: "C",
		Today: parseTestDate(t, "2026-10-19"), Write: true, Stdout: &strings.Builder{},
	})
	return cfgPath, err
}

// Positive: a revive `rules:` key before linters.exclusions does not take the generated block;
// it lands in linters.exclusions.rules after the hand-written rule.
func TestGenerateExceptions_Positive_InsertsIntoExclusionRules(t *testing.T) {
	cfgPath, err := generateInto(t, reviveRulesFirst)
	if err != nil {
		t.Fatalf("GenerateExceptions: %v", err)
	}
	revive, exclusions := sequenceLengths(t, cfgPath)
	if revive != 1 || exclusions != 2 {
		t.Fatalf("revive rules = %d (want 1), exclusion rules = %d (want 2)", revive, exclusions)
	}
}

// Boundary: the exclusion list as the last key of the file takes the block at the end.
func TestGenerateExceptions_Boundary_ExclusionRulesLastInFile(t *testing.T) {
	cfg := strings.TrimSuffix(reviveRulesFirst, "formatters:\n  enable:\n    - gofmt\n")
	cfgPath, err := generateInto(t, cfg)
	if err != nil {
		t.Fatalf("GenerateExceptions: %v", err)
	}
	if revive, exclusions := sequenceLengths(t, cfgPath); revive != 1 || exclusions != 2 {
		t.Fatalf("revive rules = %d, exclusion rules = %d", revive, exclusions)
	}
}

// Negative: a configuration that is not YAML is refused, never searched line by line.
func TestFindRulesInsertionIndex_Negative_InvalidYAML(t *testing.T) {
	if _, err := findRulesInsertionIndex(strings.Split("linters:\n  exclusions:\n    rules: [\n", "\n")); err == nil {
		t.Fatal("invalid YAML found an insertion index")
	}
}
