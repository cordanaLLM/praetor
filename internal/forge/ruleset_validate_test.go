package forge

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/config"
)

func validatePolicy() config.BranchProtectionPolicy {
	return config.BranchProtectionPolicy{
		EnforceLinearHistory:       true,
		RequireSignedCommits:       true,
		RequiredApprovingReviewers: 1,
		DismissStaleReviews:        true,
		ReviewMode:                 config.BranchReviewModeIndependent,
	}
}

// TestValidateRepositoryRuleset_Positive asserts the rendered ruleset validates against the
// policy and contexts that rendered it.
func TestValidateRepositoryRuleset_Positive(t *testing.T) {
	contexts := []string{"Unit Tests", "Lint"}
	data, err := RenderRepositoryRuleset("main", validatePolicy(), contexts)
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateRepositoryRuleset(data, "main", validatePolicy(), contexts); err != nil {
		t.Fatalf("rendered ruleset must validate: %v", err)
	}
}

// TestValidateRepositoryRuleset_Negative asserts content that weakens or omits a declared
// protection is drift, and that malformed input is refused before comparison.
func TestValidateRepositoryRuleset_Negative(t *testing.T) {
	weaker := validatePolicy()
	weaker.RequiredApprovingReviewers = 0
	unsigned := validatePolicy()
	unsigned.RequireSignedCommits = false
	render := func(p config.BranchProtectionPolicy, contexts []string) []byte {
		data, err := RenderRepositoryRuleset("main", p, contexts)
		if err != nil {
			t.Fatal(err)
		}
		return data
	}
	drift := map[string][]byte{
		"empty object":          []byte("{}"),
		"zero approvals":        render(weaker, nil),
		"no signature rule":     render(unsigned, nil),
		"missing status checks": render(validatePolicy(), nil),
	}
	for name, data := range drift {
		t.Run(name, func(t *testing.T) {
			err := ValidateRepositoryRuleset(data, "main", validatePolicy(), []string{"Unit Tests"})
			if name != "missing status checks" {
				err = ValidateRepositoryRuleset(data, "main", validatePolicy(), nil)
			}
			if !errors.Is(err, ErrRulesetDrift) {
				t.Fatalf("want ErrRulesetDrift, got %v", err)
			}
		})
	}
	for name, data := range map[string]string{"not JSON": "rules: []\n", "truncated": "{", "duplicate keys": `{"rules": [], "rules": []}`} {
		t.Run(name, func(t *testing.T) {
			err := ValidateRepositoryRuleset([]byte(data), "main", validatePolicy(), nil)
			if err == nil || errors.Is(err, ErrRulesetDrift) {
				t.Fatalf("malformed input must be refused as malformed, got %v", err)
			}
		})
	}
	bad := validatePolicy()
	bad.ReviewMode = "nobody"
	if err := ValidateRepositoryRuleset(render(validatePolicy(), nil), "main", bad, nil); err == nil || !strings.Contains(err.Error(), "review mode") {
		t.Fatalf("an unrenderable policy must be refused, got %v", err)
	}
}

// TestValidateRepositoryRuleset_Boundary asserts key order and whitespace are not content.
func TestValidateRepositoryRuleset_Boundary(t *testing.T) {
	data, err := RenderRepositoryRuleset("main", validatePolicy(), nil)
	if err != nil {
		t.Fatal(err)
	}
	// Collapsing the indentation changes every byte offset but no parsed value.
	compact := strings.Join(strings.Fields(string(data)), " ")
	if err := ValidateRepositoryRuleset([]byte(compact), "main", validatePolicy(), nil); err != nil {
		t.Fatalf("whitespace differences must validate: %v", err)
	}
	reordered := `{"rules":[{"type":"deletion"},{"type":"non_fast_forward"},{"type":"required_linear_history"},` +
		`{"type":"required_signatures"},{"type":"pull_request","parameters":{"required_review_thread_resolution":true,` +
		`"require_last_push_approval":false,"require_code_owner_review":true,"dismiss_stale_reviews_on_push":true,` +
		`"required_approving_review_count":1}},{"type":"copilot_code_review","parameters":{` +
		`"review_on_push":true,"review_draft_pull_requests":true}}],"name":"praetor-main-protection","enforcement":"active","target":"branch",` +
		`"conditions":{"ref_name":{"exclude":[],"include":["refs/heads/main","refs/heads/lts-*"]}}}`
	if err := ValidateRepositoryRuleset([]byte(reordered), "main", validatePolicy(), nil); err != nil {
		t.Fatalf("key order differences must validate: %v", err)
	}
}

func TestValidateRepositoryRuleset_StatusChecksApp_PositiveAndBoundary(t *testing.T) {
	contexts := []string{"Unit Tests", "Lint"}
	unpinnedPolicy := validatePolicy()
	unpinnedData, err := RenderRepositoryRuleset("main", unpinnedPolicy, contexts)
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateRepositoryRuleset(unpinnedData, "main", unpinnedPolicy, contexts); err != nil {
		t.Fatalf("unset key must accept ruleset without integration_id: %v", err)
	}
	pinnedPolicy := validatePolicy()
	pinnedPolicy.StatusChecksApp = "github-actions"
	pinnedData := strings.Replace(string(unpinnedData), `"context": "Unit Tests"`, `"context": "Unit Tests", "integration_id": 15368`, 1)
	pinnedData = strings.Replace(pinnedData, `"context": "Lint"`, `"context": "Lint", "integration_id": 15368`, 1)
	if err := ValidateRepositoryRuleset([]byte(pinnedData), "main", pinnedPolicy, contexts); err != nil {
		t.Fatalf("matching pinned ruleset must validate: %v", err)
	}
}

func TestValidateRepositoryRuleset_StatusChecksApp_Negative_MissingOrWrongIntegrationIDNamesContext(t *testing.T) {
	contexts := []string{"Unit Tests", "Lint"}
	pinnedPolicy := validatePolicy()
	pinnedPolicy.StatusChecksApp = "github-actions"
	missingSecond := strings.Replace(string(mustRenderPinnedFixture(t, validatePolicy(), contexts)),
		"\"context\": \"Unit Tests\"", "\"context\": \"Unit Tests\",\n            \"integration_id\": 15368", 1)
	err := ValidateRepositoryRuleset([]byte(missingSecond), "main", pinnedPolicy, contexts)
	if err == nil || !errors.Is(err, ErrRulesetDrift) || !strings.Contains(err.Error(), "Lint") {
		t.Fatalf("check lacking integration_id must fail naming context Lint, got %v", err)
	}
	wrongID := strings.Replace(missingSecond, "\"context\": \"Lint\"", "\"context\": \"Lint\",\n            \"integration_id\": 999", 1)
	err = ValidateRepositoryRuleset([]byte(wrongID), "main", pinnedPolicy, contexts)
	if err == nil || !errors.Is(err, ErrRulesetDrift) || !strings.Contains(err.Error(), "Lint") {
		t.Fatalf("check with wrong integration_id must fail naming context Lint, got %v", err)
	}
}

func mustRenderPinnedFixture(t *testing.T, policy config.BranchProtectionPolicy, contexts []string) []byte {
	t.Helper()
	data, err := RenderRepositoryRuleset("main", policy, contexts)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// Drift diagnostics retain the sentinel and name both directions in stable order.
func TestValidateRepositoryRulesetDriftDetails(t *testing.T) {
	data := mustRenderPinnedFixture(t, validatePolicy(), []string{"Old Z", "Old A"})
	err := ValidateRepositoryRuleset(data, "main", validatePolicy(), []string{"New Z", "New A"})
	if !errors.Is(err, ErrRulesetDrift) {
		t.Fatalf("sentinel lost: %v", err)
	}
	text := err.Error()
	for _, want := range []string{`missing required status context "New A" (workflow unknown)`, `missing required status context "New Z" (workflow unknown)`, `extra required status context "Old A" (workflow unknown)`, `extra required status context "Old Z" (workflow unknown)`} {
		if !strings.Contains(text, want) {
			t.Errorf("missing %q in %s", want, text)
		}
	}
	if strings.Index(text, `"New A"`) > strings.Index(text, `"New Z"`) || strings.Index(text, `"Old A"`) > strings.Index(text, `"Old Z"`) {
		t.Errorf("unsorted diagnostic: %s", text)
	}
}

// TestValidateRepositoryRulesetDriftKeys names each differing ruleset key.
func TestValidateRepositoryRulesetDriftKeys(t *testing.T) {
	data := mustRenderPinnedFixture(t, validatePolicy(), nil)
	weaker := validatePolicy()
	weaker.RequiredApprovingReviewers = 0
	weakData := mustRenderPinnedFixture(t, weaker, nil)
	for name, tc := range map[string]struct {
		data []byte
		want string
	}{
		"top level":      {[]byte(strings.Replace(string(data), `"enforcement": "active"`, `"enforcement": "disabled"`, 1)), "enforcement"},
		"rule parameter": {weakData, "rules.pull_request.parameters.required_approving_review_count"},
		"missing rule":   {[]byte(strings.Replace(string(data), `"type": "required_signatures"`, `"type": "other"`, 1)), "rules.required_signatures"},
	} {
		t.Run(name, func(t *testing.T) {
			err := ValidateRepositoryRuleset(tc.data, "main", validatePolicy(), nil)
			if !errors.Is(err, ErrRulesetDrift) || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("want key %s, got %v", tc.want, err)
			}
		})
	}
}

// TestValidateRepositoryRulesetDriftBoundary bounds the drift summary at the context limit.
func TestValidateRepositoryRulesetDriftBoundary(t *testing.T) {
	contexts := make([]string, maxRulesetContexts)
	for i := 0; i < len(contexts) && i < maxRulesetContexts; i++ {
		contexts[i] = fmt.Sprintf("Check %03d", i)
	}
	data := mustRenderPinnedFixture(t, validatePolicy(), nil)
	err := ValidateRepositoryRuleset(data, "main", validatePolicy(), contexts)
	if !errors.Is(err, ErrRulesetDrift) || !strings.Contains(err.Error(), `missing required status context "Check 4095"`) {
		t.Fatalf("bounded diagnostic must name every allowed context: %v", err)
	}
	var drift *RulesetDrift
	if !errors.As(err, &drift) || len(drift.MissingContexts) != len(contexts) {
		t.Fatalf("classification must retain full context list: %v", err)
	}
	for _, malformed := range []string{`{"rules":[{"type":"required_status_checks","parameters":{"required_status_checks":[{"context":{}}]}}]}`, `{"rules":[{"type":"required_status_checks","parameters":{"required_status_checks":[{"context":"X"},{"context":"X"}]}}]}`} {
		err := ValidateRepositoryRuleset([]byte(malformed), "main", validatePolicy(), nil)
		if !errors.Is(err, ErrRulesetDrift) || !strings.Contains(err.Error(), "invalid or duplicate context") {
			t.Errorf("malformed check must fail with detail, got %v", err)
		}
	}
}

// TestValidateRepositoryRulesetDriftCombinesMetadataAndContexts reports metadata keys and
// context drift together.
func TestValidateRepositoryRulesetDriftCombinesMetadataAndContexts(t *testing.T) {
	policy := validatePolicy()
	policy.StatusChecksApp = "github-actions"
	data := mustRenderPinnedFixture(t, validatePolicy(), []string{"Existing", "Old"})
	err := ValidateRepositoryRuleset(data, "main", policy, []string{"Existing", "New"})
	if !errors.Is(err, ErrRulesetDrift) {
		t.Fatalf("want drift: %v", err)
	}
	for _, want := range []string{`missing required status context "New"`, `extra required status context "Old"`, `["Existing"].integration_id`} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("missing %s in %v", want, err)
		}
	}
}

// TestValidateRepositoryRulesetWorkflowSources names the workflow reporting each context.
func TestValidateRepositoryRulesetWorkflowSources(t *testing.T) {
	root := t.TempDir()
	const workflow = "name: CI\non: pull_request\njobs:\n  test:\n    name: Shared Check\n    runs-on: ubuntu-latest\n    steps:\n      - run: echo ok\n"
	writeWorkflowFixture(t, root, "z.yml", workflow)
	writeWorkflowFixture(t, root, "a.yml", workflow)
	data := mustRenderPinnedFixture(t, validatePolicy(), []string{"Removed Check"})
	err := ValidateRepositoryRulesetIn(t.Context(), root, data, "main", validatePolicy(), []string{"Shared Check", "External Check"})
	if !errors.Is(err, ErrRulesetDrift) {
		t.Fatal(err)
	}
	for _, want := range []string{`missing required status context "External Check" (workflow unknown)`, `extra required status context "Removed Check" (workflow unknown)`, `.github/workflows/a.yml, .github/workflows/z.yml`} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("missing %q in %v", want, err)
		}
	}
	// A committed extra context still names its known reporting workflow.
	extra := mustRenderPinnedFixture(t, validatePolicy(), []string{"Shared Check"})
	extraErr := ValidateRepositoryRulesetIn(t.Context(), root, extra, "main", validatePolicy(), nil)
	if extraErr == nil || !strings.Contains(extraErr.Error(), `extra required status context "Shared Check" (workflow .github/workflows/a.yml, .github/workflows/z.yml)`) {
		t.Fatalf("extra context lost known provenance: %v", extraErr)
	}
	// No drift: no workflow read, even without a usable context or root.
	canceled, cancel := context.WithCancel(t.Context())
	cancel()
	if err := ValidateRepositoryRulesetIn(canceled, "", data, "main", validatePolicy(), []string{"Removed Check"}); err != nil {
		t.Fatal(err)
	}
	err = ValidateRepositoryRulesetIn(canceled, root, data, "main", validatePolicy(), []string{"New Check"})
	if !errors.Is(err, ErrRulesetDrift) || !errors.Is(err, context.Canceled) || !strings.Contains(err.Error(), "workflow source discovery failed") {
		t.Fatalf("unavailable source discovery must remain explicit: %v", err)
	}
}

// TestValidateRepositoryRulesetDriftKeySummaryBound bounds the differing-key summary.
func TestValidateRepositoryRulesetDriftKeySummaryBound(t *testing.T) {
	data := mustRenderPinnedFixture(t, validatePolicy(), nil)
	document, err := jsonObject(data)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i <= maxDriftItems; i++ {
		document[fmt.Sprintf("extra_%03d", i)] = true
	}
	data, err = marshalRulesetJSON(document)
	if err != nil {
		t.Fatal(err)
	}
	err = ValidateRepositoryRuleset(data, "main", validatePolicy(), nil)
	if !errors.Is(err, ErrRulesetDrift) || !strings.Contains(err.Error(), "1 more differing keys") {
		t.Fatalf("key diagnostic must disclose truncation: %v", err)
	}
}

// TestValidateRepositoryRulesetDriftRetainsOtherKeysWithContextDrift keeps other differing
// keys when contexts also drift.
func TestValidateRepositoryRulesetDriftRetainsOtherKeysWithContextDrift(t *testing.T) {
	data := mustRenderPinnedFixture(t, validatePolicy(), []string{"Existing"})
	document, err := jsonObject(data)
	if err != nil {
		t.Fatal(err)
	}
	rules, err := objectList(document["rules"], "rules", maxRulesetRules)
	if err != nil {
		t.Fatal(err)
	}
	rules[0]["parameters"] = map[string]any{}
	rules[0], rules[len(rules)-1] = rules[len(rules)-1], rules[0]
	document["rules"] = rules
	data, err = marshalRulesetJSON(document)
	if err != nil {
		t.Fatal(err)
	}
	err = ValidateRepositoryRuleset(data, "main", validatePolicy(), []string{"Existing", "New"})
	if !errors.Is(err, ErrRulesetDrift) {
		t.Fatal(err)
	}
	for _, want := range []string{`missing required status context "New"`, "rules.deletion.parameters", "rules (order)"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("missing %q in %v", want, err)
		}
	}
}
