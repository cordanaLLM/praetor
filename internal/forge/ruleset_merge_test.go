package forge

import (
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
