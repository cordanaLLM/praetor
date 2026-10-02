// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package managedasset

import (
	"encoding/json"
	"errors"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/strictjson"
)

// renovateConfig is the part of renovate.json that keeps a hosted workflow's SHA pins current.
type renovateConfig struct {
	GitHubActions struct {
		ManagerFilePatterns []string `json:"managerFilePatterns"`
	} `json:"github-actions"`
	PackageRules []struct {
		MatchManagers  []string `json:"matchManagers"`
		MatchFileNames []string `json:"matchFileNames"`
		PinDigests     bool     `json:"pinDigests"`
		GroupName      string   `json:"groupName"`
	} `json:"packageRules"`
}

func readRenovateConfig(t *testing.T) renovateConfig {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", "renovate.json"))
	if err != nil {
		t.Fatal(err)
	}
	var config renovateConfig
	if err := json.Unmarshal(raw, &config); err != nil {
		t.Fatal(err)
	}
	return config
}

// renovateJSONBounds bound the strict scan of renovate.json (HISS-02).
var renovateJSONBounds = strictjson.Options{MaxBytes: 1 << 20, MaxDepth: 16, Names: strictjson.ExactNames}

// Negative, a tripwire for merges: two branches that each add a top-level list such as
// customManagers merge without a conflict into one file holding the key twice, and a JSON
// reader then keeps the last list and drops the other without a word (Renovate's validator
// included). renovate.json repeats no member name in any object.
func TestRenovateConfigRepeatsNoMemberName(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "renovate.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := strictjson.Validate(raw, renovateJSONBounds); err != nil {
		t.Fatalf("renovate.json: %v", err)
	}
	twice := []byte(`{"customManagers": [{"customType": "regex"}], "customManagers": []}`)
	if err := strictjson.Validate(twice, renovateJSONBounds); !errors.Is(err, strictjson.ErrDuplicate) {
		t.Fatalf("a repeated customManagers key was accepted: %v", err)
	}
}

// githubActionsReads reports whether one of the github-actions manager's added file patterns,
// a /re2/ literal as Renovate spells it, matches rel.
func githubActionsReads(t *testing.T, config renovateConfig, rel string) bool {
	t.Helper()
	for _, pattern := range config.GitHubActions.ManagerFilePatterns {
		expr, ok := strings.CutPrefix(pattern, "/")
		expr, closed := strings.CutSuffix(expr, "/")
		if !ok || !closed {
			t.Fatalf("github-actions file pattern %q is not a /regex/ literal", pattern)
		}
		if regexp.MustCompile(expr).MatchString(rel) {
			return true
		}
	}
	return false
}

// hostedFamilies returns the registered families with a hosted workflow; there must be one.
func hostedFamilies(t *testing.T) []Family {
	t.Helper()
	hosted := slices.DeleteFunc(Families(), func(f Family) bool { return f.WorkflowFile == "" })
	if len(hosted) == 0 {
		t.Fatal("no family declares a hosted workflow; the Renovate checks would pass vacuously")
	}
	return hosted
}

// Positive: for every family with a hosted workflow, Renovate's github-actions manager reads the
// template source as well as the repository's own copy, pins digests, and groups the two files
// into one branch, so an update never leaves them apart for the audit byte lock to reject. Every
// uses: line sits on a line of its own in the source, where the manager's line-based extractor
// finds it.
func TestRenovateUpdatesTemplatePinsWithWorkflowCopy(t *testing.T) {
	config := readRenovateConfig(t)
	for _, family := range hostedFamilies(t) {
		if !githubActionsReads(t, config, family.Source) {
			t.Fatalf("renovate.json github-actions.managerFilePatterns does not cover %s", family.Source)
		}
		pinned, grouped := false, false
		for _, rule := range config.PackageRules {
			if !slices.Contains(rule.MatchManagers, "github-actions") {
				continue
			}
			pinned = pinned || (rule.PinDigests && len(rule.MatchFileNames) == 0)
			grouped = grouped || (rule.GroupName != "" && slices.Contains(rule.MatchFileNames, family.WorkflowFile) &&
				slices.Contains(rule.MatchFileNames, family.Source))
		}
		if !pinned || !grouped {
			t.Fatalf("renovate.json github-actions rules: pinDigests=%v, one group over %s and %s=%v",
				pinned, family.WorkflowFile, family.Source, grouped)
		}
		source, err := os.ReadFile(filepath.Join("..", "..", filepath.FromSlash(family.Source)))
		if err != nil {
			t.Fatal(err)
		}
		lines := strings.Split(string(source), "\n")
		for _, line := range strings.Split(family.Workflow, "\n") {
			if strings.Contains(line, "uses:") && !slices.Contains(lines, line) {
				t.Fatalf("uses line %q is not a line of its own in %s", line, family.Source)
			}
		}
	}
}

// Negative and boundary: each added pattern names a template source alone, not its test or the
// neighbouring assets, which carry no workflow.
func TestRenovateTemplatePatternIsExact(t *testing.T) {
	config := readRenovateConfig(t)
	for _, family := range hostedFamilies(t) {
		neighbours := []string{path.Join(family.Directory, "assets_test.go"), "x" + family.Source, family.Source + ".bak"}
		neighbours = append(neighbours, family.AssetPaths()...)
		for _, rel := range neighbours {
			if githubActionsReads(t, config, rel) {
				t.Fatalf("renovate.json github-actions.managerFilePatterns also matches %s", rel)
			}
		}
	}
}
