package forge

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/config"
)

// declaredHighSecurity is a resolved policy as a security:high repository declares it.
func declaredHighSecurity() config.BranchProtectionPolicy {
	return config.BranchProtectionPolicy{
		EnforceLinearHistory: true, RequireSignedCommits: true, RequiredApprovingReviewers: 2, DismissStaleReviews: true,
	}
}

// liveRule builds one active rule of ruleset id, with parameters encoded from params.
func liveRule(t *testing.T, ruleType string, id int, params any) LiveBranchRule {
	t.Helper()
	rule := LiveBranchRule{Type: ruleType, RulesetID: id, RulesetSourceType: "Repository", RulesetSource: "acme/widgets"}
	if params != nil {
		raw, err := json.Marshal(params)
		if err != nil {
			t.Fatal(err)
		}
		rule.Parameters = raw
	}
	return rule
}

// matchingRuleset is the live rules of the ruleset RenderRepositoryRuleset renders for
// declaredHighSecurity and the check CI, as GitHub lists them for main.
func matchingRuleset(t *testing.T) *LiveBranchProtection {
	t.Helper()
	return &LiveBranchProtection{
		Branch:       "main",
		RulesetNames: map[int]string{7: RepositoryRulesetName},
		Rules: []LiveBranchRule{
			liveRule(t, "deletion", 7, nil), liveRule(t, "non_fast_forward", 7, nil),
			liveRule(t, "required_linear_history", 7, nil), liveRule(t, "required_signatures", 7, nil),
			liveRule(t, "pull_request", 7, map[string]any{
				"required_approving_review_count": 2, "dismiss_stale_reviews_on_push": true, "require_code_owner_review": true,
			}),
			liveRule(t, "required_status_checks", 7, map[string]any{"required_status_checks": []any{map[string]any{"context": "CI"}}}),
		},
	}
}

// findingOf returns the finding for property, failing when there is none.
func findingOf(t *testing.T, findings []ProtectionFinding, property string) ProtectionFinding {
	t.Helper()
	for _, finding := range findings {
		if finding.Property == property {
			return finding
		}
	}
	t.Fatalf("no finding for %q in %+v", property, findings)
	return ProtectionFinding{}
}

// Positive: a branch whose ruleset enforces the resolved policy has no drift, and every
// property names the ruleset that enforces it.
func TestEvaluateBranchProtection_Positive_MatchingRulesetIsEnforced(t *testing.T) {
	findings, err := EvaluateBranchProtection(declaredHighSecurity(), []string{"CI"}, matchingRuleset(t))
	if err != nil {
		t.Fatal(err)
	}
	if drifted := ProtectionDrifts(findings); len(drifted) != 0 {
		t.Fatalf("a matching ruleset drifts on %v: %+v", drifted, findings)
	}
	want := `ruleset "praetor-main-protection" #7`
	for _, finding := range findings {
		if finding.Verdict != ProtectionEnforced || !slices.Equal(finding.EnforcedBy, []string{want}) {
			t.Errorf("%s: verdict %s by %v, want enforced by %s", finding.Property, finding.Verdict, finding.EnforcedBy, want)
		}
	}
}

// Negative: signed commits and two reviewers declared, a legacy protection object with
// required_signatures off and no approving reviews: both drift, and the mechanism is named.
func TestEvaluateBranchProtection_Negative_LegacyProtectionDrifts(t *testing.T) {
	live := &LiveBranchProtection{Branch: "main", Legacy: &LegacyBranchProtection{
		RequiredPullRequestReviews: &LegacyPullRequestReviews{DismissStaleReviews: true},
		RequiredSignatures:         &EnabledSetting{Enabled: false},
		RequiredLinearHistory:      &EnabledSetting{Enabled: true},
		RequiredStatusChecks:       &LegacyStatusChecks{Contexts: []string{"Lint"}, Checks: []StatusCheckRef{{Context: "CI"}}},
	}}
	findings, err := EvaluateBranchProtection(declaredHighSecurity(), []string{"CI", "Lint"}, live)
	if err != nil {
		t.Fatal(err)
	}
	if drifted := ProtectionDrifts(findings); !slices.Equal(drifted, []string{"Approving reviews", "Code owner review", "Signed commits"}) {
		t.Fatalf("drifted = %v, want approving reviews, code owner review and signed commits: %+v", drifted, findings)
	}
	signed := findingOf(t, findings, "Signed commits")
	if signed.Declared != "required" || signed.Live != "not enforced" || len(signed.EnforcedBy) != 0 {
		t.Errorf("signed commits finding = %+v", signed)
	}
	reviews := findingOf(t, findings, "Approving reviews")
	if reviews.Declared != "2" || reviews.Live != "0" {
		t.Errorf("approving reviews finding = %+v", reviews)
	}
	linear := findingOf(t, findings, "Linear history")
	if linear.Verdict != ProtectionEnforced || !slices.Equal(linear.EnforcedBy, []string{LegacyProtectionMechanism}) {
		t.Errorf("linear history must be enforced by %s: %+v", LegacyProtectionMechanism, linear)
	}
	// Legacy protection blocks deletion and force pushes unless it allows them.
	for _, property := range []string{"Deletion blocked", "Force pushes blocked"} {
		if finding := findingOf(t, findings, property); finding.Verdict != ProtectionEnforced {
			t.Errorf("%s: %+v", property, finding)
		}
	}
	if got := live.Mechanisms(); !slices.Equal(got, []string{LegacyProtectionMechanism}) {
		t.Errorf("mechanisms = %v", got)
	}
}

// Boundary: a branch with no ruleset rule and no protection object at all drifts on every
// declared property instead of being skipped, and names no mechanism; a property declared off
// is not required.
func TestEvaluateBranchProtection_Boundary_UnprotectedBranchDriftsOnEveryDeclaredProperty(t *testing.T) {
	policy := declaredHighSecurity()
	policy.DismissStaleReviews = false
	live := &LiveBranchProtection{Branch: "main"}
	findings, err := EvaluateBranchProtection(policy, []string{"CI", "Lint"}, live)
	if err != nil {
		t.Fatal(err)
	}
	for _, finding := range findings {
		want := ProtectionDrift
		if finding.Property == "Dismiss stale reviews" {
			want = ProtectionNotRequired
		}
		if finding.Verdict != want || len(finding.EnforcedBy) != 0 {
			t.Errorf("%s: verdict %s by %v, want %s by nothing", finding.Property, finding.Verdict, finding.EnforcedBy, want)
		}
	}
	if checks := findingOf(t, findings, "Required status checks"); checks.Live != "0 of 2 required; missing: CI, Lint" {
		t.Errorf("status checks finding = %+v", checks)
	}
	if got := live.Mechanisms(); len(got) != 0 {
		t.Errorf("an unprotected branch names mechanisms %v", got)
	}
}

// Boundary: a live setting stricter than declared is reported as stricter, not drift; the
// mechanisms of both kinds are named; review_mode single_maintainer declares zero reviews.
func TestEvaluateBranchProtection_Boundary_StricterLiveAndBothMechanisms(t *testing.T) {
	live := matchingRuleset(t)
	live.Rules = append(live.Rules, LiveBranchRule{
		Type: "required_signatures", RulesetID: 9, RulesetSourceType: "Organization", RulesetSource: "acme",
	})
	live.Legacy = &LegacyBranchProtection{
		RequiredPullRequestReviews: &LegacyPullRequestReviews{RequiredApprovingReviewCount: 3},
		RequiredSignatures:         &EnabledSetting{Enabled: true},
	}
	policy := config.BranchProtectionPolicy{RequiredApprovingReviewers: 1, ReviewMode: config.BranchReviewModeSingleMaintainer}
	findings, err := EvaluateBranchProtection(policy, nil, live)
	if err != nil {
		t.Fatal(err)
	}
	reviews := findingOf(t, findings, "Approving reviews")
	if reviews.Verdict != ProtectionStricter || reviews.Declared != "0 (review_mode single_maintainer)" || reviews.Live != "3" ||
		!slices.Equal(reviews.EnforcedBy, []string{LegacyProtectionMechanism}) {
		t.Errorf("approving reviews finding = %+v", reviews)
	}
	signed := findingOf(t, findings, "Signed commits")
	wantSigned := []string{`ruleset "praetor-main-protection" #7`, "ruleset #9 of organization acme", LegacyProtectionMechanism}
	if signed.Verdict != ProtectionStricter || !slices.Equal(signed.EnforcedBy, wantSigned) {
		t.Errorf("signed commits finding = %+v, want stricter by %v", signed, wantSigned)
	}
	if checks := findingOf(t, findings, "Required status checks"); checks.Verdict != ProtectionNotRequired {
		t.Errorf("no declared check with live checks must not be judged: %+v", checks)
	}
	if drifted := ProtectionDrifts(findings); len(drifted) != 0 {
		t.Errorf("stricter settings reported as drift: %v", drifted)
	}
}

// Negative: no live read, an invalid review mode and malformed rule parameters are errors.
func TestEvaluateBranchProtection_Negative_InvalidInputs(t *testing.T) {
	if _, err := EvaluateBranchProtection(declaredHighSecurity(), nil, nil); err == nil {
		t.Error("a nil live protection must be an error")
	}
	if _, err := EvaluateBranchProtection(config.BranchProtectionPolicy{ReviewMode: "solo"}, nil, &LiveBranchProtection{}); err == nil {
		t.Error("an invalid review mode must be an error")
	}
	broken := &LiveBranchProtection{Branch: "main", Rules: []LiveBranchRule{{Type: "pull_request", RulesetID: 1, Parameters: json.RawMessage(`{"required_approving_review_count":"two"}`)}}}
	_, err := EvaluateBranchProtection(declaredHighSecurity(), nil, broken)
	if err == nil || !strings.Contains(err.Error(), `rule "pull_request" of ruleset #1`) {
		t.Errorf("malformed parameters: got %v", err)
	}
	checks := make([]string, maxRulesetContexts+1)
	if _, err := EvaluateBranchProtection(declaredHighSecurity(), checks, &LiveBranchProtection{}); err == nil {
		t.Error("an oversized check list must be an error")
	}
}
