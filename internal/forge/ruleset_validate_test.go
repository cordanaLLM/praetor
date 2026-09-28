package forge

import (
	"errors"
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
		`"required_approving_review_count":1}}],"name":"praetor-main-protection","enforcement":"active","target":"branch",` +
		`"conditions":{"ref_name":{"exclude":[],"include":["refs/heads/main","refs/heads/lts-*"]}}}`
	if err := ValidateRepositoryRuleset([]byte(reordered), "main", validatePolicy(), nil); err != nil {
		t.Fatalf("key order differences must validate: %v", err)
	}
}
