package forge

import (
	"fmt"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/config"
)

func TestRenderRepositoryRulesetPolicyAndEmptyChecks(t *testing.T) {
	policy := config.BranchProtectionPolicy{RequireSignedCommits: true, RequiredApprovingReviewers: 3}
	data, err := RenderRepositoryRuleset(policy, nil)
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	for _, required := range []string{"required_signatures", `"required_approving_review_count": 3`, `"require_code_owner_review": true`, `"dismiss_stale_reviews_on_push": false`} {
		if !strings.Contains(text, required) {
			t.Fatalf("selected policy missing %s: %s", required, text)
		}
	}
	if strings.Contains(text, "required_linear_history") || strings.Contains(text, "required_status_checks") {
		t.Fatal("unselected protections or phantom contexts were rendered")
	}
}

func TestRenderRepositoryRulesetSingleMaintainerReviewMode(t *testing.T) {
	policy := config.DefaultPolicy().BranchProtection
	policy.RequireSignedCommits = true
	policy.ReviewMode = config.BranchReviewModeSingleMaintainer
	data, err := RenderRepositoryRuleset(policy, []string{"Required Checks Aggregator"})
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	for _, required := range []string{
		"required_linear_history", "required_signatures", "required_status_checks",
		`"required_approving_review_count": 0`, `"require_code_owner_review": false`,
	} {
		if !strings.Contains(text, required) {
			t.Fatalf("single-maintainer ruleset missing %s: %s", required, text)
		}
	}
	if policy.RequiredApprovingReviewers != 1 {
		t.Fatalf("rendering mutated the configured reviewer minimum: %+v", policy)
	}
}

func TestRenderRepositoryRulesetRejectsUnknownReviewMode(t *testing.T) {
	policy := config.DefaultPolicy().BranchProtection
	policy.ReviewMode = "unreviewed"
	if _, err := RenderRepositoryRuleset(policy, nil); err == nil || !strings.Contains(err.Error(), "unsupported branch protection review mode") {
		t.Fatalf("unknown review mode was not rejected: %v", err)
	}
}

func TestRenderRepositoryRulesetContextBoundsAndInvalidSelections(t *testing.T) {
	policy := config.DefaultPolicy().BranchProtection
	contexts := make([]string, maxRulesetContexts)
	for i := range contexts {
		contexts[i] = fmt.Sprintf("check-%d", i)
	}
	if _, err := RenderRepositoryRuleset(policy, contexts); err != nil {
		t.Fatal(err)
	}
	invalid := [][]string{append(contexts, "overflow"), {""}, {" spaced "}, {"duplicate", "duplicate"}, {"line\nbreak"}}
	for _, names := range invalid {
		if _, err := RenderRepositoryRuleset(policy, names); err == nil {
			t.Fatal("invalid check selection accepted")
		}
	}
	policy.RequiredApprovingReviewers = -1
	if _, err := RenderRepositoryRuleset(policy, nil); err == nil {
		t.Fatal("negative approval count accepted")
	}
}
