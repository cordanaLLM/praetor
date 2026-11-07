// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package forge

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/config"
)

// TestRulesetCopilotReviewsDraftsAndPushes covers minimal and protected rulesets alike.
func TestRulesetCopilotReviewsDraftsAndPushes(t *testing.T) {
	policies := []config.BranchProtectionPolicy{{}, validatePolicy()}
	for i := 0; i < len(policies) && i < 2; i++ {
		data, err := RenderRepositoryRuleset("main", policies[i], nil)
		if err != nil {
			t.Fatal(err)
		}
		parameters := copilotRuleParameters(t, data)
		if len(parameters) != 2 || !parameters["review_draft_pull_requests"] || !parameters["review_on_push"] {
			t.Fatalf("missing draft/push Copilot rule: %s", data)
		}
	}
}

// copilotRuleParameters decodes exactly one Copilot rule without assuming other parameter types.
func copilotRuleParameters(t *testing.T, data []byte) map[string]bool {
	t.Helper()
	var doc struct {
		Rules []struct {
			Type       string          `json:"type"`
			Parameters json.RawMessage `json:"parameters"`
		} `json:"rules"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatal(err)
	}
	var parameters map[string]bool
	count := 0
	for i := 0; i < len(doc.Rules) && i < 16; i++ {
		if doc.Rules[i].Type != "copilot_code_review" {
			continue
		}
		count++
		if err := json.Unmarshal(doc.Rules[i].Parameters, &parameters); err != nil {
			t.Fatal(err)
		}
	}
	if count != 1 {
		t.Fatalf("Copilot rules = %d, want exactly one: %s", count, data)
	}
	return parameters
}

// TestRulesetCopilotRefusesDisabledDraftReview plants each disabled review parameter in turn.
func TestRulesetCopilotRefusesDisabledDraftReview(t *testing.T) {
	data, err := RenderRepositoryRuleset("main", validatePolicy(), nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range [2]string{"review_draft_pull_requests", "review_on_push"} {
		mutated := strings.Replace(string(data), `"`+key+`": true`, `"`+key+`": false`, 1)
		if mutated == string(data) {
			t.Fatalf("rendering has no %s parameter to mutate", key)
		}
		if err := ValidateRepositoryRuleset([]byte(mutated), "main", validatePolicy(), nil); !errors.Is(err, ErrRulesetDrift) {
			t.Fatalf("disabled %s accepted: %v", key, err)
		}
	}
}
