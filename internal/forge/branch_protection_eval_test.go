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
				"require_last_push_approval": false, "required_review_thread_resolution": true,
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

// Positive (#159): the ruleset praetor writes, listed in active enforcement, is compared and
// enforced by itself.
func TestEvaluateBranchProtection_Positive_ActiveManagedRulesetIsEnforced(t *testing.T) {
	live := matchingRuleset(t)
	live.RulesetEnforcement = map[int]string{7: "active"}
	findings, err := EvaluateBranchProtection(declaredHighSecurity(), []string{"CI"}, live)
	if err != nil {
		t.Fatal(err)
	}
	finding := findingOf(t, findings, "Ruleset enforcement")
	want := `ruleset "praetor-main-protection" #7`
	if finding.Verdict != ProtectionEnforced || finding.Declared != "active" || finding.Live != "active" ||
		!slices.Equal(finding.EnforcedBy, []string{want}) {
		t.Fatalf("ruleset enforcement finding = %+v", finding)
	}
}

// Negative (#159): the ruleset praetor writes, listed in evaluate or disabled enforcement, applies
// no rule; the drift names the enforcement and the ruleset beside every property it would carry.
func TestEvaluateBranchProtection_Negative_InactiveManagedRulesetDrifts(t *testing.T) {
	for _, enforcement := range []string{"evaluate", "disabled"} {
		live := &LiveBranchProtection{Branch: "main", RulesetNames: map[int]string{7: RepositoryRulesetName},
			RulesetEnforcement: map[int]string{7: enforcement}}
		findings, err := EvaluateBranchProtection(declaredHighSecurity(), []string{"CI"}, live)
		if err != nil {
			t.Fatal(err)
		}
		finding := findingOf(t, findings, "Ruleset enforcement")
		if finding.Verdict != ProtectionDrift || finding.Live != enforcement+` (ruleset "praetor-main-protection" #7)` ||
			len(finding.EnforcedBy) != 0 {
			t.Errorf("%s: ruleset enforcement finding = %+v", enforcement, finding)
		}
		drifted := ProtectionDrifts(findings)
		if !slices.Contains(drifted, "Ruleset enforcement") || !slices.Contains(drifted, "Signed commits") {
			t.Errorf("%s: drifted = %v", enforcement, drifted)
		}
	}
}

// Boundary (#159): a branch protected by the legacy protection object alone, with no ruleset of
// praetor's name, is compared on what that object enforces and not on a ruleset; a listing that
// does not report the enforcement is not judged on it; of two rulesets of that name the lowest id
// is compared.
func TestEvaluateBranchProtection_Boundary_ManagedRulesetAbsentOrUnreported(t *testing.T) {
	classic := &LiveBranchProtection{Branch: "main", RulesetNames: map[int]string{3: "operator-ruleset"},
		RulesetEnforcement: map[int]string{3: "disabled"}, Legacy: &LegacyBranchProtection{
			RequiredPullRequestReviews: &LegacyPullRequestReviews{RequiredApprovingReviewCount: 1, RequireCodeOwnerReviews: true},
			RequiredStatusChecks:       &LegacyStatusChecks{Checks: []StatusCheckRef{{Context: "CI"}}},
		}}
	policy := config.BranchProtectionPolicy{RequiredApprovingReviewers: 1}
	findings, err := EvaluateBranchProtection(policy, []string{"CI"}, classic)
	if err != nil {
		t.Fatal(err)
	}
	if drifted := ProtectionDrifts(findings); len(drifted) != 0 {
		t.Fatalf("classic protection matching the policy drifts on %v: %+v", drifted, findings)
	}
	for _, finding := range findings {
		if finding.Property == "Ruleset enforcement" {
			t.Fatalf("a repository without praetor's ruleset was judged on it: %+v", finding)
		}
	}

	unreported := &LiveBranchProtection{Branch: "main", RulesetNames: map[int]string{7: RepositoryRulesetName}}
	if findings, err = EvaluateBranchProtection(policy, nil, unreported); err != nil {
		t.Fatal(err)
	}
	if slices.Contains(ProtectionDrifts(findings), "Ruleset enforcement") {
		t.Fatalf("an unreported enforcement was judged: %+v", findings)
	}

	twice := &LiveBranchProtection{Branch: "main", RulesetNames: map[int]string{9: RepositoryRulesetName, 4: RepositoryRulesetName},
		RulesetEnforcement: map[int]string{9: "active", 4: "evaluate"}}
	if id, enforcement, found := twice.managedRuleset(); !found || id != 4 || enforcement != "evaluate" {
		t.Fatalf("managed ruleset = %d %q %t, want the lowest id 4", id, enforcement, found)
	}
}

// Positive: declared allowed_merge_methods [squash] renders and matches a live [squash].
func TestEvaluateBranchProtection_Positive_DeclaredAllowedMergeMethodsMatchesLive(t *testing.T) {
	policy := declaredHighSecurity()
	policy.AllowedMergeMethods = []string{"squash"}
	live := matchingRuleset(t)
	live.Rules[4] = liveRule(t, "pull_request", 7, map[string]any{
		"required_approving_review_count": 2, "dismiss_stale_reviews_on_push": true, "require_code_owner_review": true,
		"require_last_push_approval": false, "required_review_thread_resolution": true,
		"allowed_merge_methods": []string{"squash"},
	})
	findings, err := EvaluateBranchProtection(policy, []string{"CI"}, live)
	if err != nil {
		t.Fatal(err)
	}
	if drifted := ProtectionDrifts(findings); len(drifted) != 0 {
		t.Fatalf("expected no drift, got: %v", drifted)
	}
	finding := findingOf(t, findings, "allowed_merge_methods")
	if finding.Verdict != ProtectionEnforced || finding.Declared != "[squash]" || finding.Live != "[squash]" {
		t.Fatalf("unexpected finding: %+v", finding)
	}
}

// Positive: planted live-only require_extra_approval_for_unattributed_changes true with
// nothing declared passes as stricter finding, not drift.
func TestEvaluateBranchProtection_Positive_LiveOnlyExtraApprovalStricter(t *testing.T) {
	policy := declaredHighSecurity()
	live := matchingRuleset(t)
	live.Rules[4] = liveRule(t, "pull_request", 7, map[string]any{
		"required_approving_review_count": 2, "dismiss_stale_reviews_on_push": true, "require_code_owner_review": true,
		"require_last_push_approval": false, "required_review_thread_resolution": true,
		"require_extra_approval_for_unattributed_changes": true,
	})
	findings, err := EvaluateBranchProtection(policy, []string{"CI"}, live)
	if err != nil {
		t.Fatal(err)
	}
	drifted := ProtectionDrifts(findings)
	if slices.Contains(drifted, "require_extra_approval_for_unattributed_changes") {
		t.Fatalf("unexpected drift on require_extra_approval_for_unattributed_changes, got: %v", drifted)
	}
	finding := findingOf(t, findings, "require_extra_approval_for_unattributed_changes")
	if finding.Verdict != ProtectionStricter || finding.Live != "true" || finding.Declared != "false" {
		t.Fatalf("unexpected finding: %+v", finding)
	}
}

// Negative: declared [squash] vs live [merge,squash,rebase] fails.
func TestEvaluateBranchProtection_Negative_DeclaredMergeMethodsMismatch(t *testing.T) {
	policy := declaredHighSecurity()
	policy.AllowedMergeMethods = []string{"squash"}
	live := matchingRuleset(t)
	live.Rules[4] = liveRule(t, "pull_request", 7, map[string]any{
		"required_approving_review_count": 2, "dismiss_stale_reviews_on_push": true, "require_code_owner_review": true,
		"require_last_push_approval": false, "required_review_thread_resolution": true,
		"allowed_merge_methods": []string{"merge", "squash", "rebase"},
	})
	findings, err := EvaluateBranchProtection(policy, []string{"CI"}, live)
	if err != nil {
		t.Fatal(err)
	}
	drifted := ProtectionDrifts(findings)
	if !slices.Contains(drifted, "allowed_merge_methods") {
		t.Fatalf("expected drift on allowed_merge_methods, got: %v", drifted)
	}
	finding := findingOf(t, findings, "allowed_merge_methods")
	if finding.Verdict != ProtectionDrift || finding.Declared != "[squash]" || finding.Live != "[merge, squash, rebase]" {
		t.Fatalf("unexpected finding: %+v", finding)
	}
}

// Boundary: live default values with nothing declared pass without drift findings.
func TestEvaluateBranchProtection_Boundary_LiveDefaultsWithNothingDeclaredPass(t *testing.T) {
	policy := declaredHighSecurity()
	live := matchingRuleset(t)
	live.Rules[4] = liveRule(t, "pull_request", 7, map[string]any{
		"required_approving_review_count": 2, "dismiss_stale_reviews_on_push": true, "require_code_owner_review": true,
		"require_last_push_approval": false, "required_review_thread_resolution": true,
		"allowed_merge_methods":                           []string{"merge", "squash", "rebase"},
		"require_extra_approval_for_unattributed_changes": false,
		"required_reviewers":                              []any{},
		"dismissal_restriction":                           map[string]any{"enabled": false, "allowed_actors": []any{}},
	})
	findings, err := EvaluateBranchProtection(policy, []string{"CI"}, live)
	if err != nil {
		t.Fatal(err)
	}
	if drifted := ProtectionDrifts(findings); len(drifted) != 0 {
		t.Fatalf("expected no drift on defaults, got: %v", drifted)
	}
}

// Positive: live squash-only with declared merge+squash+rebase, and extra approval live true
// with declared false pass as stricter findings, not drift.
func TestEvaluateBranchProtection_Positive_StricterMergeMethodsAndExtraApproval(t *testing.T) {
	policy := declaredHighSecurity()
	policy.AllowedMergeMethods = []string{"merge", "squash", "rebase"}
	disabled := false
	policy.RequireExtraApprovalForUnattributedChanges = &disabled
	live := matchingRuleset(t)
	live.Rules[4] = liveRule(t, "pull_request", 7, map[string]any{
		"required_approving_review_count": 2, "dismiss_stale_reviews_on_push": true, "require_code_owner_review": true,
		"require_last_push_approval": false, "required_review_thread_resolution": true,
		"allowed_merge_methods":                           []string{"squash"},
		"require_extra_approval_for_unattributed_changes": true,
	})
	findings, err := EvaluateBranchProtection(policy, []string{"CI"}, live)
	if err != nil {
		t.Fatal(err)
	}
	methods := findingOf(t, findings, "allowed_merge_methods")
	if methods.Verdict != ProtectionStricter || methods.Declared != "[merge, squash, rebase]" || methods.Live != "[squash]" {
		t.Errorf("allowed_merge_methods finding = %+v, want stricter", methods)
	}
	extra := findingOf(t, findings, "require_extra_approval_for_unattributed_changes")
	if extra.Verdict != ProtectionStricter || extra.Declared != "false" || extra.Live != "true" {
		t.Errorf("require_extra_approval finding = %+v, want stricter", extra)
	}
	if drifted := ProtectionDrifts(findings); len(drifted) != 0 {
		t.Fatalf("expected no drift on stricter settings, got: %v", drifted)
	}
}

// Negative: live allows rebase while declared squash-only fails (looser); extra approval live
// false with declared true fails.
func TestEvaluateBranchProtection_Negative_LooserMergeMethodsAndExtraApproval(t *testing.T) {
	policy := declaredHighSecurity()
	policy.AllowedMergeMethods = []string{"squash"}
	enabled := true
	policy.RequireExtraApprovalForUnattributedChanges = &enabled
	live := matchingRuleset(t)
	live.Rules[4] = liveRule(t, "pull_request", 7, map[string]any{
		"required_approving_review_count": 2, "dismiss_stale_reviews_on_push": true, "require_code_owner_review": true,
		"require_last_push_approval": false, "required_review_thread_resolution": true,
		"allowed_merge_methods":                           []string{"rebase"},
		"require_extra_approval_for_unattributed_changes": false,
	})
	findings, err := EvaluateBranchProtection(policy, []string{"CI"}, live)
	if err != nil {
		t.Fatal(err)
	}
	drifted := ProtectionDrifts(findings)
	if !slices.Contains(drifted, "allowed_merge_methods") {
		t.Errorf("expected drift on allowed_merge_methods, got: %v", drifted)
	}
	if !slices.Contains(drifted, "require_extra_approval_for_unattributed_changes") {
		t.Errorf("expected drift on require_extra_approval_for_unattributed_changes, got: %v", drifted)
	}
}
