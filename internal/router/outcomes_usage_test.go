package router

import (
	"encoding/json"
	"path/filepath"
	"testing"
)

func TestOutcomeUsageValidation(t *testing.T) {
	for _, tc := range []struct {
		name, fields string
		valid        bool
	}{
		{"legacy", `{}`, true},
		{"zero", `{"tokens":0,"effort":""}`, true},
		{"cap", `{"tokens":1000000000000,"effort":"max"}`, true},
		{"negative", `{"tokens":-1}`, false},
		{"over_cap", `{"tokens":1000000000001}`, false},
		{"effort", `{"effort":"turbo"}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			o := sampleOutcome("stubs", OutcomeOK)
			if err := json.Unmarshal([]byte(tc.fields), &o); err != nil {
				t.Fatal(err)
			}
			if err := ValidateOutcome(o); (err == nil) != tc.valid {
				t.Fatalf("valid=%v: %v", tc.valid, err)
			}
		})
	}
}

func TestOutcomeUsageRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "outcomes.jsonl")
	for _, fields := range []string{`{}`, `{"tokens":1234,"effort":"low"}`} {
		o := sampleOutcome("stubs", OutcomeOK)
		if err := json.Unmarshal([]byte(fields), &o); err != nil {
			t.Fatal(err)
		}
		if err := AppendOutcome(t.Context(), path, o); err != nil {
			t.Fatal(err)
		}
	}
	rows, err := ReadOutcomes(t.Context(), path)
	if err != nil || len(rows) != 2 {
		t.Fatalf("rows=%v: %v", rows, err)
	}
	data, err := json.Marshal(rows[1])
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	if got["tokens"] != float64(1234) || got["effort"] != "low" {
		t.Fatalf("usage lost: %s", data)
	}
}
