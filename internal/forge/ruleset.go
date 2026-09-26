package forge

import (
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"

	"github.com/cordanaLLM/praetor/internal/config"
	"gopkg.in/yaml.v3"
)

const (
	maxRulesetContexts = 64 * 64
	maxRulesetRules    = 64
)

type statusRuleset struct {
	Rules []struct {
		Type       string `json:"type"`
		Parameters struct {
			RequiredStatusChecks []struct {
				Context string `json:"context"`
			} `json:"required_status_checks"`
		} `json:"parameters"`
	} `json:"rules"`
}

// RulesetRequiresStatusContext inspects only semantic required-status-check
// entries. Unrelated metadata or prose containing the same text is ignored.
func RulesetRequiresStatusContext(data []byte, target string) (bool, error) {
	if target == "" || strings.TrimSpace(target) != target {
		return false, fmt.Errorf("required status context selector must be nonempty and trimmed")
	}
	ruleset, err := parseStatusRuleset(data)
	if err != nil {
		return false, err
	}
	for ruleIndex := 0; ruleIndex < len(ruleset.Rules) && ruleIndex < maxRulesetRules; ruleIndex++ {
		rule := ruleset.Rules[ruleIndex]
		if rule.Type == "required_status_checks" {
			found, findErr := statusChecksContain(rule.Parameters.RequiredStatusChecks, target)
			if findErr != nil || found {
				return found, findErr
			}
		}
	}
	return false, nil
}

func parseStatusRuleset(data []byte) (*statusRuleset, error) {
	if !json.Valid(data) {
		return nil, fmt.Errorf("ruleset must be a single valid JSON document")
	}
	var ruleset statusRuleset
	if err := json.Unmarshal(data, &ruleset); err != nil {
		return nil, fmt.Errorf("parse ruleset status contexts: %w", err)
	}
	if len(ruleset.Rules) > maxRulesetRules {
		return nil, fmt.Errorf("ruleset exceeds %d rules", maxRulesetRules)
	}
	return &ruleset, nil
}

func statusChecksContain(checks []struct {
	Context string `json:"context"`
}, target string) (bool, error) {
	if len(checks) > maxRulesetContexts {
		return false, fmt.Errorf("required status checks exceed %d contexts", maxRulesetContexts)
	}
	for checkIndex := 0; checkIndex < len(checks) && checkIndex < maxRulesetContexts; checkIndex++ {
		if checks[checkIndex].Context == target {
			return true, nil
		}
	}
	return false, nil
}

// RepositoryRulesetName names the praetor branch protection ruleset, in
// .github/rulesets/main.json and on the forge alike.
const RepositoryRulesetName = "praetor-main-protection"

// RepositoryRulesetRefs returns the refs the praetor ruleset protects: main and every
// lts-* branch. The local file and a remote sync share it, so neither narrows the other.
func RepositoryRulesetRefs() []string {
	return []string{"refs/heads/main", "refs/heads/lts-*"}
}

// RenderRepositoryRuleset renders local branch protection from the selected policy
// and actual required check contexts. An empty selection omits the status rule.
func RenderRepositoryRuleset(policy config.BranchProtectionPolicy, contexts []string) ([]byte, error) {
	doc, err := protectionRuleset(RepositoryRulesetName, RepositoryRulesetRefs(), policy, contexts, true)
	if err != nil {
		return nil, err
	}
	return json.MarshalIndent(doc, "", "  ")
}

// ErrRulesetDrift reports a committed ruleset whose content differs from the one the declared
// branch protection policy renders.
var ErrRulesetDrift = errors.New("ruleset differs from declared branch protection policy; review the existing file before reconciliation")

// ValidateRepositoryRuleset checks a committed ruleset against the one RenderRepositoryRuleset
// produces for policy and contexts. It is the single content check behind sync, the branch
// protection audit and the documentation audit; the audit used to check only that the file
// existed, so "{}" and a ruleset with zero approvals and no signature rule both passed it.
//
// The data must be JSON syntax, and is then parsed through YAML because yaml.v3 rejects
// duplicate keys, nested JSON objects included. Comparing parsed documents ignores whitespace
// and object key order while preserving every declared rule, parameter and condition.
func ValidateRepositoryRuleset(data []byte, policy config.BranchProtectionPolicy, contexts []string) error {
	if !json.Valid(data) {
		return errors.New("ruleset must be a single valid JSON document")
	}
	var observed map[string]any
	if err := yaml.Unmarshal(data, &observed); err != nil {
		return fmt.Errorf("parse ruleset: %w", err)
	}
	expectedBytes, err := RenderRepositoryRuleset(policy, contexts)
	if err != nil {
		return err
	}
	var expected map[string]any
	if err := yaml.Unmarshal(expectedBytes, &expected); err != nil {
		return fmt.Errorf("parse rendered ruleset: %w", err)
	}
	if !reflect.DeepEqual(observed, expected) {
		return ErrRulesetDrift
	}
	return nil
}

func protectionRuleset(name string, refs []string, policy config.BranchProtectionPolicy, contexts []string, strict bool) (map[string]any, error) {
	reviewCount, requireCodeOwner, err := policy.EffectiveReviewRequirements()
	if err != nil {
		return nil, err
	}
	if err := validateRulesetInputs(contexts); err != nil {
		return nil, err
	}
	rules := protectionRules(policy, reviewCount, requireCodeOwner)
	if len(contexts) > 0 {
		checks := make([]map[string]string, 0, len(contexts))
		for i := 0; i < len(contexts) && i < maxRulesetContexts; i++ {
			checks = append(checks, map[string]string{"context": contexts[i]})
		}
		rules = append(rules, map[string]any{"type": "required_status_checks", "parameters": map[string]any{
			"strict_required_status_checks_policy": strict,
			"required_status_checks":               checks,
		}})
	}
	return map[string]any{
		"name": name, "target": "branch", "enforcement": "active",
		"conditions": map[string]any{"ref_name": map[string]any{"include": refs, "exclude": []string{}}},
		"rules":      rules,
	}, nil
}

func validateRulesetInputs(contexts []string) error {
	if len(contexts) > maxRulesetContexts {
		return fmt.Errorf("required status checks exceed %d contexts", maxRulesetContexts)
	}
	seen := make(map[string]bool, len(contexts))
	for i := 0; i < len(contexts) && i < maxRulesetContexts; i++ {
		name := contexts[i]
		if name == "" || strings.TrimSpace(name) != name || seen[name] || strings.ContainsAny(name, "\r\n\x00") {
			return errors.New("required status check contexts must be nonempty, trimmed and unique")
		}
		seen[name] = true
	}
	return nil
}

func protectionRules(policy config.BranchProtectionPolicy, reviewCount int, requireCodeOwner bool) []map[string]any {
	rules := []map[string]any{{"type": "deletion"}, {"type": "non_fast_forward"}}
	if policy.EnforceLinearHistory {
		rules = append(rules, map[string]any{"type": "required_linear_history"})
	}
	if policy.RequireSignedCommits {
		rules = append(rules, map[string]any{"type": "required_signatures"})
	}
	return append(rules, map[string]any{"type": "pull_request", "parameters": map[string]any{
		"required_approving_review_count":   reviewCount,
		"dismiss_stale_reviews_on_push":     policy.DismissStaleReviews,
		"require_code_owner_review":         requireCodeOwner,
		"require_last_push_approval":        false,
		"required_review_thread_resolution": true,
	}})
}
