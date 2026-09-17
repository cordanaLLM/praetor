package notebook

import (
	"strings"
	"testing"
)

type observation struct {
	Quality float64 `json:"quality"`
	Notes   string  `json:"notes,omitempty"`
}

// TestDecode_Positive_SingleSpellingStillDecodes keeps the fold from refusing valid
// documents: one spelling of a field, in any case, decodes as it always did.
func TestDecode_Positive_SingleSpellingStillDecodes(t *testing.T) {
	cases := []struct {
		name string
		raw  string
	}{
		{"declared spelling", `{"quality": 1.5}`},
		{"upper-case spelling", `{"Quality": 1.5}`},
		{"shouting spelling", `{"QUALITY": 1.5}`},
	}
	for _, tc := range cases {
		var obs observation
		if err := Decode([]byte(tc.raw), &obs); err != nil {
			t.Errorf("%s: %s must decode, got %v", tc.name, tc.raw, err)
			continue
		}
		if obs.Quality != 1.5 {
			t.Errorf("%s: expected quality 1.5, got %v", tc.name, obs.Quality)
		}
	}
}

// TestDecode_Negative_CaseVariantDuplicateIsRefused is the defect: encoding/json matches
// field names case-insensitively, so two spellings of one field silently let the last one
// win. The strict decoder must refuse the document instead.
func TestDecode_Negative_CaseVariantDuplicateIsRefused(t *testing.T) {
	cases := []struct {
		name string
		raw  string
	}{
		{"exact duplicate", `{"quality": 1, "quality": 2}`},
		{"case variant", `{"quality": 1, "Quality": 2}`},
		{"shouting variant", `{"QUALITY": 1, "quality": 2}`},
		{"nested case variant", `{"notes": "x", "inner": {"a": 1, "A": 2}}`},
	}
	for _, tc := range cases {
		var value map[string]any
		err := Decode([]byte(tc.raw), &value)
		if err == nil {
			t.Errorf("%s: %s must be refused as ambiguous", tc.name, tc.raw)
			continue
		}
		if !strings.Contains(err.Error(), "duplicate JSON key") {
			t.Errorf("%s: expected a duplicate-key refusal, got %v", tc.name, err)
		}
	}
}

// TestDecode_Boundary_NonASCIIFoldMatchesEncodingJSON covers the edge of the fold: the
// Kelvin sign folds to "k" in encoding/json's field matching, so a key spelled with it is
// the same key. Distinct letters must stay distinct.
func TestDecode_Boundary_NonASCIIFoldMatchesEncodingJSON(t *testing.T) {
	var value map[string]any
	if err := Decode([]byte("{\"k\": 1, \"K\": 2}"), &value); err == nil {
		t.Error("the Kelvin sign folds to k, so the document is ambiguous and must be refused")
	}
	if err := Decode([]byte(`{"a": 1, "b": 2}`), &value); err != nil {
		t.Errorf("distinct keys must decode, got %v", err)
	}
	if err := Decode([]byte("{\"é\": 1, \"É\": 2}"), &value); err == nil {
		t.Error("e-acute and its capital fold together and must be refused")
	}
	// Keys are not values: a repeated string as a value is not a duplicate key.
	if err := Decode([]byte(`{"a": ["x", "x"], "b": {"c": "x"}}`), &value); err != nil {
		t.Errorf("repeated values are not duplicate keys, got %v", err)
	}
}

// TestFoldKey_MatchesTheEncodingJSONRule documents the fold itself: ASCII upper-cased,
// other runes folded to the smallest rune in their orbit.
func TestFoldKey_MatchesTheEncodingJSONRule(t *testing.T) {
	cases := map[string]string{
		"quality": "QUALITY",
		"Quality": "QUALITY",
		"QUALITY": "QUALITY",
		"":        "",
		"snake_1": "SNAKE_1",
		"K":       "K",
		"é":       "É",
		"sigma-σ": "SIGMA-Σ",
	}
	for in, want := range cases {
		if got := foldKey(in); got != want {
			t.Errorf("foldKey(%q) = %q, want %q", in, got, want)
		}
	}
}
