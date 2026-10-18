package forge

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"testing"
)

func mustJSONObject(t *testing.T, raw string) map[string]any {
	t.Helper()
	object, err := jsonObject([]byte(raw))
	if err != nil {
		t.Fatalf("fixture %s: %v", raw, err)
	}
	return object
}

const convergedWant = `{"name":"p","target":"branch","enforcement":"active",
 "conditions":{"ref_name":{"include":["refs/heads/main","refs/heads/lts-*"],"exclude":[]}},
 "rules":[{"type":"deletion"},{"type":"required_status_checks","parameters":{
  "strict_required_status_checks_policy":true,"required_status_checks":[{"context":"CI"}]}}]}`

func TestRulesetConverged_Positive_ForgeExtrasAreNotAShortfall(t *testing.T) {
	got := mustJSONObject(t, `{"id":1,"name":"p","target":"branch","enforcement":"active","node_id":"x",
	 "conditions":{"ref_name":{"include":["refs/heads/lts-*","refs/heads/main","refs/heads/x"],"exclude":["refs/heads/y"]}},
	 "rules":[{"type":"code_scanning"},{"type":"deletion"},{"type":"required_status_checks","parameters":{
	  "strict_required_status_checks_policy":true,"do_not_enforce_on_create":false,
	  "required_status_checks":[{"context":"other","integration_id":9},{"context":"CI","integration_id":null}]}}]}`)
	if err := rulesetConverged(got, mustJSONObject(t, convergedWant)); err != nil {
		t.Fatalf("a superset readback must converge: %v", err)
	}
}

func TestRulesetConverged_Negative_Shortfalls(t *testing.T) {
	want := mustJSONObject(t, convergedWant)
	for needle, got := range map[string]string{
		"enforcement is evaluate": `{"name":"p","target":"branch","enforcement":"evaluate"}`,
		"does not include refs":   `{"name":"p","target":"branch","enforcement":"active","conditions":{"ref_name":{"include":["refs/heads/main"]}}}`,
		"excludes protected refs": `{"name":"p","target":"branch","enforcement":"active","conditions":{"ref_name":{"include":["refs/heads/main","refs/heads/lts-*"],"exclude":["refs/heads/lts-*"]}}}`,
		`lacks rule "deletion"`:   `{"name":"p","target":"branch","enforcement":"active","conditions":{"ref_name":{"include":["refs/heads/main","refs/heads/lts-*"]}},"rules":[]}`,
		"does not require status check CI": `{"name":"p","target":"branch","enforcement":"active","conditions":{"ref_name":{"include":["refs/heads/main","refs/heads/lts-*"]}},
		 "rules":[{"type":"deletion"},{"type":"required_status_checks","parameters":{"strict_required_status_checks_policy":true,"required_status_checks":[]}}]}`,
		`parameter "strict_required_status_checks_policy" is false`: `{"name":"p","target":"branch","enforcement":"active","conditions":{"ref_name":{"include":["refs/heads/main","refs/heads/lts-*"]}},
		 "rules":[{"type":"deletion"},{"type":"required_status_checks","parameters":{"strict_required_status_checks_policy":false,"required_status_checks":[{"context":"CI"}]}}]}`,
		"is not an object": `{"name":"p","target":"branch","enforcement":"active","conditions":[]}`,
	} {
		err := rulesetConverged(mustJSONObject(t, got), want)
		if err == nil || !strings.Contains(err.Error(), needle) {
			t.Errorf("want error containing %q, got %v", needle, err)
		}
	}
}

func TestMergeRuleset_Boundary_MalformedAndOversizedLiveRulesets(t *testing.T) {
	desired := mustJSONObject(t, convergedWant)
	for needle, live := range map[string]string{
		"conditions is not an object":            `{"conditions":"x"}`,
		"include entry 0 is not a string":        `{"conditions":{"ref_name":{"include":[1]}}}`,
		"rules is not an array":                  `{"rules":{}}`,
		"rules entry 0 is not an object":         `{"rules":["deletion"]}`,
		"parameters is not an object":            `{"rules":[{"type":"required_status_checks","parameters":[]}]}`,
		"required_status_checks is not an array": `{"rules":[{"type":"required_status_checks","parameters":{"required_status_checks":"CI"}}]}`,
		"exceeds 256 entries":                    `{"conditions":{"ref_name":{"include":[` + strings.Repeat(`"r",`, maxRulesetRefs) + `"r"]}}}`,
	} {
		if _, _, _, err := mergeRuleset(mustJSONObject(t, live), desired); err == nil || !strings.Contains(err.Error(), needle) {
			t.Errorf("want error containing %q, got %v", needle, err)
		}
	}
	// An empty live ruleset merges to the desired document itself.
	merged, lowered, kept, err := mergeRuleset(map[string]any{}, desired)
	if err != nil || len(lowered) != 0 || len(kept) != 0 {
		t.Fatalf("empty live ruleset: %v, lowered %v, kept %v", err, lowered, kept)
	}
	if err := rulesetConverged(merged, desired); err != nil {
		t.Fatalf("merge of an empty live ruleset does not converge: %v", err)
	}
}

// reviewRuleset is a ruleset whose only rule is a pull_request rule with params.
func reviewRuleset(t *testing.T, params string) map[string]any {
	t.Helper()
	return mustJSONObject(t, `{"name":"p","target":"branch","enforcement":"active","rules":[{"type":"pull_request","parameters":`+params+`}]}`)
}

// mergedReviewParameters merges desired into live and returns the pull_request parameters,
// the lowered parameters, and the kept parameters.
func mergedReviewParameters(t *testing.T, live, desired map[string]any) (map[string]any, []string, []string) {
	t.Helper()
	merged, lowered, kept, err := mergeRuleset(live, desired)
	if err != nil {
		t.Fatalf("merge: %v", err)
	}
	rules, err := objectList(merged["rules"], "rules", maxRulesetRules)
	if err != nil || len(rules) != 1 {
		t.Fatalf("merged rules = %v (%v)", merged["rules"], err)
	}
	params, err := optionalObject(rules[0]["parameters"], "parameters")
	if err != nil {
		t.Fatal(err)
	}
	names := make([]string, 0, len(lowered))
	for _, p := range lowered {
		names = append(names, fmt.Sprintf("%s.%s=%v->%v", p.Rule, p.Parameter, p.Live, p.Declared))
	}
	keptNames := make([]string, 0, len(kept))
	for _, p := range kept {
		keptNames = append(keptNames, fmt.Sprintf("%s.%s=%v->%v", p.Rule, p.Parameter, p.Live, p.Declared))
	}
	return params, names, keptNames
}

// Positive: a declared relaxation applies, and each live setting stricter than the rendered one
// is reported as lowered, while kept stricter settings stay and are reported as kept.
func TestMergeRuleset_Positive_AppliesAndReportsLoweredParameters(t *testing.T) {
	live := reviewRuleset(t, `{"required_approving_review_count":3,"dismiss_stale_reviews_on_push":true,
	 "require_code_owner_review":true,"require_last_push_approval":true,"required_review_thread_resolution":true}`)
	desired := reviewRuleset(t, `{"required_approving_review_count":0,"dismiss_stale_reviews_on_push":false,
	 "require_code_owner_review":false,"require_last_push_approval":false,"required_review_thread_resolution":true}`)
	params, lowered, kept := mergedReviewParameters(t, live, desired)
	for key, want := range map[string]string{
		"required_approving_review_count": "0", "dismiss_stale_reviews_on_push": "false",
		"require_code_owner_review": "false", "require_last_push_approval": "true", "required_review_thread_resolution": "true",
	} {
		if got := fmt.Sprint(params[key]); got != want {
			t.Errorf("%s = %s, want %s", key, got, want)
		}
	}
	wantLowered := []string{
		"pull_request.required_approving_review_count=3->0", "pull_request.dismiss_stale_reviews_on_push=true->false",
		"pull_request.require_code_owner_review=true->false",
	}
	if fmt.Sprint(lowered) != fmt.Sprint(wantLowered) {
		t.Errorf("lowered = %v, want %v", lowered, wantLowered)
	}
	wantKept := []string{"pull_request.require_last_push_approval=true->false"}
	if fmt.Sprint(kept) != fmt.Sprint(wantKept) {
		t.Errorf("kept = %v, want %v", kept, wantKept)
	}
}

// Negative: a rendered setting stricter than the live one tightens the live ruleset and lowers
// nothing, and a live value of another type than the rendered one cannot be ordered, so the
// rendered value applies without a lowered report.
func TestMergeRuleset_Negative_TighteningLowersNothing(t *testing.T) {
	live := reviewRuleset(t, `{"required_approving_review_count":0,"dismiss_stale_reviews_on_push":false,"require_code_owner_review":"yes"}`)
	desired := reviewRuleset(t, `{"required_approving_review_count":2,"dismiss_stale_reviews_on_push":true,"require_code_owner_review":false}`)
	params, lowered, kept := mergedReviewParameters(t, live, desired)
	for key, want := range map[string]string{
		"required_approving_review_count": "2", "dismiss_stale_reviews_on_push": "true", "require_code_owner_review": "false",
	} {
		if got := fmt.Sprint(params[key]); got != want {
			t.Errorf("%s = %s, want the rendered %s", key, got, want)
		}
	}
	if len(lowered) != 0 || len(kept) != 0 {
		t.Errorf("tightening a ruleset lowered %v, kept %v", lowered, kept)
	}
}

// Boundary: a parameter only one side carries is kept and not reported, a non-integer count is
// not ordered, an equal value is not lowered, and a live strict status check policy lowered to
// the rendered false is reported under its rule.
func TestMergeRuleset_Boundary_EqualAbsentAndUnorderedParameters(t *testing.T) {
	live := reviewRuleset(t, `{"required_approving_review_count":1.5,"require_code_owner_review":true,"allowed_merge_methods":["squash"]}`)
	desired := reviewRuleset(t, `{"required_approving_review_count":1,"require_code_owner_review":true,"dismiss_stale_reviews_on_push":false}`)
	params, lowered, kept := mergedReviewParameters(t, live, desired)
	if fmt.Sprint(params["required_approving_review_count"]) != "1" || fmt.Sprint(params["allowed_merge_methods"]) != "[squash]" ||
		fmt.Sprint(params["dismiss_stale_reviews_on_push"]) != "false" || len(lowered) != 0 || len(kept) != 0 {
		t.Errorf("merged parameters = %v, lowered %v, kept %v", params, lowered, kept)
	}
	checks := mustJSONObject(t, `{"rules":[{"type":"required_status_checks","parameters":{"strict_required_status_checks_policy":true,"required_status_checks":[]}}]}`)
	relaxed := mustJSONObject(t, strings.Replace(convergedWant, `"strict_required_status_checks_policy":true`, `"strict_required_status_checks_policy":false`, 1))
	merged, checkLowered, _, err := mergeRuleset(checks, relaxed)
	if err != nil {
		t.Fatal(err)
	}
	if err := rulesetConverged(merged, relaxed); err != nil {
		t.Errorf("a relaxed status check policy must converge: %v", err)
	}
	if len(checkLowered) != 1 || checkLowered[0].Rule != statusChecksParameter || checkLowered[0].Parameter != "strict_required_status_checks_policy" {
		t.Errorf("lowered strict policy = %+v", checkLowered)
	}
	for _, pair := range [][2]any{{true, true}, {false, true}, {2, 2}, {"2", 1}} {
		if parameterStricter(pair[0], pair[1]) {
			t.Errorf("parameterStricter(%v, %v) = true", pair[0], pair[1])
		}
	}
	if !parameterStricter(3, json.Number("2")) {
		t.Error("an int above a json.Number must be stricter")
	}
}

// Positive: require_extra_approval_for_unattributed_changes live true over desired false is kept and reported.
func TestMergeRuleset_Positive_ExtraApprovalKeptIsReported(t *testing.T) {
	live := reviewRuleset(t, `{"require_extra_approval_for_unattributed_changes":true}`)
	desired := reviewRuleset(t, `{"require_extra_approval_for_unattributed_changes":false}`)
	params, lowered, kept := mergedReviewParameters(t, live, desired)
	if got := fmt.Sprint(params["require_extra_approval_for_unattributed_changes"]); got != "true" {
		t.Fatalf("expected true, got %s", got)
	}
	if len(lowered) != 0 {
		t.Fatalf("expected no lowered parameters, got %v", lowered)
	}
	want := []string{"pull_request.require_extra_approval_for_unattributed_changes=true->false"}
	if !slices.Equal(kept, want) {
		t.Fatalf("kept = %v, want %v", kept, want)
	}
}
