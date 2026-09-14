package planning

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestCompileRequiresExactCompleteJSONShape(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*testing.T, map[string]any)
	}{
		{"case-folded semantic duplicate", func(_ *testing.T, d map[string]any) { d["ID"] = "shadow-draft" }},
		{"missing required boolean", func(t *testing.T, d map[string]any) {
			delete(jsonObjectAt(t, d, "sources", 0), "verified")
		}},
		{"missing empty collection", func(t *testing.T, d map[string]any) {
			delete(jsonObjectAt(t, d, "milestones", 0), "depends_on")
		}},
		{"null empty collection", func(t *testing.T, d map[string]any) {
			jsonObjectAt(t, d, "milestones", 0)["depends_on"] = nil
		}},
		{"wrong primitive type", func(t *testing.T, d map[string]any) {
			jsonObjectField(t, d, "project")["title"] = true
		}},
		{"wrong collection member type", func(t *testing.T, d map[string]any) {
			jsonObjectAt(t, d, "steps", 0)["depends_on"] = []any{true}
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			draft := fixtureJSONDocument(t)
			test.mutate(t, draft)
			raw, err := json.Marshal(draft)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := Compile(t.Context(), raw); err == nil {
				t.Fatal("non-exact planning JSON shape accepted")
			}
		})
	}
}

func TestCompileRejectsDeepMalformedJSONShape(t *testing.T) {
	draft := fixtureJSONDocument(t)
	var nested any = "invalid acceptance case"
	for depth := 0; depth < 40; depth++ {
		nested = []any{nested}
	}
	step := jsonObjectAt(t, draft, "steps", 0)
	acceptance, ok := step["acceptance"].(map[string]any)
	if !ok {
		t.Fatal("fixture step acceptance is not an object")
	}
	acceptance["positive"] = []any{nested}
	raw, err := json.Marshal(draft)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Compile(t.Context(), raw); err == nil || !strings.Contains(err.Error(), "nesting exceeds 32") {
		t.Fatalf("deep malformed planning shape returned %v", err)
	}
}

func TestCompileCanonicalizesEmptyDependenciesAsArrays(t *testing.T) {
	result, err := Compile(t.Context(), fixtureBytes(t))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(result.Files["plan.json"], []byte(`"depends_on":null`)) ||
		!bytes.Contains(result.Files["plan.json"], []byte(`"depends_on":[]`)) {
		t.Fatalf("canonical dependencies lost their explicit empty-set shape: %s", result.Files["plan.json"])
	}
}

func TestCompileRequiresEachStepToReferenceARequirement(t *testing.T) {
	draft := fixtureDraft(t)
	draft.Steps[0].RequirementIDs = []string{}
	if _, err := compileDraft(t, draft); err == nil || !strings.Contains(err.Error(), "requirement references") {
		t.Fatalf("step without a requirement reference returned %v", err)
	}
}

func TestAcceptanceValidationHasDeterministicOrder(t *testing.T) {
	draft := fixtureDraft(t)
	draft.Steps[0].Acceptance = Acceptance{Positive: []string{}, Negative: []string{}, Boundary: []string{}}
	for iteration := 0; iteration < 32; iteration++ {
		_, err := compileDraft(t, draft)
		if err == nil || !strings.Contains(err.Error(), "positive acceptance") {
			t.Fatalf("acceptance validation order changed: %v", err)
		}
	}
}

func fixtureJSONDocument(t *testing.T) map[string]any {
	t.Helper()
	var document map[string]any
	if err := json.Unmarshal(fixtureBytes(t), &document); err != nil {
		t.Fatal(err)
	}
	return document
}

func jsonObjectAt(t *testing.T, document map[string]any, collection string, index int) map[string]any {
	t.Helper()
	values, ok := document[collection].([]any)
	if !ok || index < 0 || index >= len(values) {
		t.Fatalf("fixture collection %s[%d] is unavailable", collection, index)
	}
	value, ok := values[index].(map[string]any)
	if !ok {
		t.Fatalf("fixture collection %s[%d] is not an object", collection, index)
	}
	return value
}

func jsonObjectField(t *testing.T, document map[string]any, name string) map[string]any {
	t.Helper()
	value, ok := document[name].(map[string]any)
	if !ok {
		t.Fatalf("fixture field %s is not an object", name)
	}
	return value
}
