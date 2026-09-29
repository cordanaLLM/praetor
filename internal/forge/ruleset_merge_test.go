package forge

import (
	"encoding/json"
	"fmt"
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
		if _, err := mergeRuleset(mustJSONObject(t, live), desired); err == nil || !strings.Contains(err.Error(), needle) {
			t.Errorf("want error containing %q, got %v", needle, err)
		}
	}
	// An empty live ruleset merges to the desired document itself.
	merged, err := mergeRuleset(map[string]any{}, desired)
	if err != nil {
		t.Fatalf("empty live ruleset: %v", err)
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

// mergedReviewParameters merges desired into live and returns the pull_request parameters.
func mergedReviewParameters(t *testing.T, live, desired map[string]any) map[string]any {
	t.Helper()
	merged, err := mergeRuleset(live, desired)
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
	return params
}

// Positive: a live setting stricter than the rendered one is kept, so a merge never lowers a
// review count or switches a live review requirement off (#154).
func TestMergeRuleset_Positive_KeepsStricterLiveParameters(t *testing.T) {
	live := reviewRuleset(t, `{"required_approving_review_count":3,"dismiss_stale_reviews_on_push":true,
	 "require_code_owner_review":true,"require_last_push_approval":true,"required_review_thread_resolution":true}`)
	desired := reviewRuleset(t, `{"required_approving_review_count":1,"dismiss_stale_reviews_on_push":false,
	 "require_code_owner_review":false,"require_last_push_approval":false,"required_review_thread_resolution":true}`)
	params := mergedReviewParameters(t, live, desired)
	for key, want := range map[string]string{
		"required_approving_review_count": "3", "dismiss_stale_reviews_on_push": "true",
		"require_code_owner_review": "true", "require_last_push_approval": "true", "required_review_thread_resolution": "true",
	} {
		if got := fmt.Sprint(params[key]); got != want {
			t.Errorf("%s = %s, want the live %s", key, got, want)
		}
	}
}

// Negative: a rendered setting stricter than the live one still tightens the live ruleset, and
// a live value of another type than the rendered one cannot be ordered, so the rendered wins.
func TestMergeRuleset_Negative_StricterRenderedParametersApply(t *testing.T) {
	live := reviewRuleset(t, `{"required_approving_review_count":0,"dismiss_stale_reviews_on_push":false,"require_code_owner_review":"yes"}`)
	desired := reviewRuleset(t, `{"required_approving_review_count":2,"dismiss_stale_reviews_on_push":true,"require_code_owner_review":false}`)
	params := mergedReviewParameters(t, live, desired)
	for key, want := range map[string]string{
		"required_approving_review_count": "2", "dismiss_stale_reviews_on_push": "true", "require_code_owner_review": "false",
	} {
		if got := fmt.Sprint(params[key]); got != want {
			t.Errorf("%s = %s, want the rendered %s", key, got, want)
		}
	}
}

// Boundary: a parameter only one side carries is kept, a non-integer count is not ordered, and
// a live strict status check policy equal to the rendered one converges.
func TestMergeRuleset_Boundary_EqualAbsentAndUnorderedParameters(t *testing.T) {
	live := reviewRuleset(t, `{"required_approving_review_count":1.5,"allowed_merge_methods":["squash"]}`)
	desired := reviewRuleset(t, `{"required_approving_review_count":1,"dismiss_stale_reviews_on_push":false}`)
	params := mergedReviewParameters(t, live, desired)
	if fmt.Sprint(params["required_approving_review_count"]) != "1" || fmt.Sprint(params["allowed_merge_methods"]) != "[squash]" ||
		fmt.Sprint(params["dismiss_stale_reviews_on_push"]) != "false" {
		t.Errorf("merged parameters = %v", params)
	}
	checks := mustJSONObject(t, `{"rules":[{"type":"required_status_checks","parameters":{"strict_required_status_checks_policy":true,"required_status_checks":[]}}]}`)
	merged, err := mergeRuleset(checks, mustJSONObject(t, convergedWant))
	if err != nil {
		t.Fatal(err)
	}
	if err := rulesetConverged(merged, mustJSONObject(t, convergedWant)); err != nil {
		t.Errorf("a live strict policy equal to the rendered one must converge: %v", err)
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
