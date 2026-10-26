package forge

import (
	"bytes"
	"fmt"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/config"
)

func TestRenderRepositoryRulesetPolicyAndEmptyChecks(t *testing.T) {
	policy := config.BranchProtectionPolicy{RequireSignedCommits: true, RequiredApprovingReviewers: 3}
	data, err := RenderRepositoryRuleset("main", policy, nil)
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
	data, err := RenderRepositoryRuleset("main", policy, []string{"Required Checks Aggregator"})
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
	if _, err := RenderRepositoryRuleset("main", policy, nil); err == nil || !strings.Contains(err.Error(), "unsupported branch protection review mode") {
		t.Fatalf("unknown review mode was not rejected: %v", err)
	}
}

func TestRenderRepositoryRulesetContextBoundsAndInvalidSelections(t *testing.T) {
	policy := config.DefaultPolicy().BranchProtection
	contexts := make([]string, maxRulesetContexts)
	for i := range contexts {
		contexts[i] = fmt.Sprintf("check-%d", i)
	}
	if _, err := RenderRepositoryRuleset("main", policy, contexts); err != nil {
		t.Fatal(err)
	}
	invalid := [][]string{append(contexts, "overflow"), {""}, {" spaced "}, {"duplicate", "duplicate"}, {"line\nbreak"}}
	for _, names := range invalid {
		if _, err := RenderRepositoryRuleset("main", policy, names); err == nil {
			t.Fatal("invalid check selection accepted")
		}
	}
	policy.RequiredApprovingReviewers = -1
	if _, err := RenderRepositoryRuleset("main", policy, nil); err == nil {
		t.Fatal("negative approval count accepted")
	}
}

func TestRulesetRequiresStatusContextStructuralAndBounded(t *testing.T) {
	const target = "Documentation Governance"
	positive := []byte(`{"metadata":{"example":"` + target +
		`"},"rules":[{"type":"required_status_checks","parameters":{"required_status_checks":[{"context":"` +
		target + `"}]}}]}`)
	required, err := RulesetRequiresStatusContext(positive, target)
	if err != nil || !required {
		t.Fatalf("structural required context missed: required=%v err=%v", required, err)
	}
	metadataOnly := []byte(`{"metadata":{"example":"` + target + `"},"rules":[]}`)
	required, err = RulesetRequiresStatusContext(metadataOnly, target)
	if err != nil || required {
		t.Fatalf("metadata lookalike claimed: required=%v err=%v", required, err)
	}
	rules := make([]string, maxRulesetRules)
	for index := 0; index < len(rules) && index < maxRulesetRules; index++ {
		rules[index] = `{"type":"deletion"}`
	}
	exact := []byte(`{"rules":[` + strings.Join(rules, ",") + `]}`)
	if _, err := RulesetRequiresStatusContext(exact, target); err != nil {
		t.Fatalf("exact rule boundary rejected: %v", err)
	}
	rules = append(rules, `{"type":"deletion"}`)
	if _, err := RulesetRequiresStatusContext(
		[]byte(`{"rules":[`+strings.Join(rules, ",")+`]}`), target,
	); err == nil {
		t.Fatal("ruleset above structural scan bound accepted")
	}
}

func TestRenderRepositoryRuleset_Positive_EndsWithExactlyOneNewline(t *testing.T) {
	policy := config.DefaultPolicy().BranchProtection
	data, err := RenderRepositoryRuleset("main", policy, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(data) == 0 || data[len(data)-1] != '\n' {
		t.Fatalf("ruleset rendering does not end with newline: %q", string(data))
	}
	if len(data) >= 2 && data[len(data)-2] == '\n' {
		t.Fatalf("ruleset rendering ends with multiple newlines: %q", string(data))
	}
}

func TestRenderRepositoryRuleset_Negative_DroppedNewlineRefused(t *testing.T) {
	policy := config.DefaultPolicy().BranchProtection
	data, err := RenderRepositoryRuleset("main", policy, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(data) == 0 || data[len(data)-1] != '\n' {
		t.Fatalf("negative check: ruleset rendering dropped final newline: %q", string(data))
	}
}

func TestRenderRepositoryRuleset_Boundary_SingleNewlineExact(t *testing.T) {
	policy := config.DefaultPolicy().BranchProtection
	contexts := []string{"Documentation Governance"}
	data, err := RenderRepositoryRuleset("main", policy, contexts)
	if err != nil {
		t.Fatal(err)
	}
	trimmed := bytes.TrimRight(data, "\r\n")
	if len(data)-len(trimmed) != 1 {
		t.Fatalf("boundary check: expected exactly 1 trailing newline byte, got delta %d: %q", len(data)-len(trimmed), data)
	}
}

func TestRenderRepositoryRuleset_Boundary_StatusChecksAppUnsetByteIdentical(t *testing.T) {
	policy := config.DefaultPolicy().BranchProtection
	contexts := []string{"Build", "Lint"}
	baseData, err := RenderRepositoryRuleset("main", policy, contexts)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(baseData), "integration_id") {
		t.Fatalf("unset status_checks_app must not emit integration_id: %s", string(baseData))
	}
	wantCheckBlock := "\"required_status_checks\": [\n          {\n            \"context\": \"Build\"\n          },\n          {\n            \"context\": \"Lint\"\n          }\n        ]"
	if !strings.Contains(string(baseData), wantCheckBlock) {
		t.Fatalf("unset status_checks_app changed check bytes: %s", string(baseData))
	}
}

func TestRenderRepositoryRuleset_Positive_StatusChecksAppGithubActionsPinsEveryCheck(t *testing.T) {
	policy := config.DefaultPolicy().BranchProtection
	policy.StatusChecksApp = "github-actions"
	contexts := []string{"Build", "Lint"}
	data, err := RenderRepositoryRuleset("main", policy, contexts)
	if err != nil {
		t.Fatal(err)
	}
	wantCheckBlock := "\"required_status_checks\": [\n          {\n            \"context\": \"Build\",\n            \"integration_id\": 15368\n          },\n          {\n            \"context\": \"Lint\",\n            \"integration_id\": 15368\n          }\n        ]"
	if !strings.Contains(string(data), wantCheckBlock) {
		t.Fatalf("github-actions status_checks_app did not pin every check to integration_id 15368: %s", string(data))
	}
}

func TestRenderRepositoryRuleset_Negative_StatusChecksAppUnknownSlugRefused(t *testing.T) {
	policy := config.DefaultPolicy().BranchProtection
	policy.StatusChecksApp = "other-app"
	if _, err := RenderRepositoryRuleset("main", policy, []string{"Build"}); err == nil || !strings.Contains(err.Error(), "github-actions") {
		t.Fatalf("unknown status_checks_app accepted or missing known slugs: %v", err)
	}
}
