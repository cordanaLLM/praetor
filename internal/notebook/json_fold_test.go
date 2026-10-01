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

// TestDecode_Boundary_KeepsItsBoundsAndWording pins the notebook's own bounds and wording,
// which it passes to the shared strictjson reader: 1 MiB, 32 nesting levels.
func TestDecode_Boundary_KeepsItsBoundsAndWording(t *testing.T) {
	var value any
	exact := []byte(`"` + strings.Repeat("x", 1<<20-2) + `"`)
	if err := Decode(exact, &value); err != nil {
		t.Errorf("artifact of exactly 1 MiB refused: %v", err)
	}
	if err := Decode(append(exact, ' '), &value); err == nil || err.Error() != "JSON requires 1..1048576 UTF-8 bytes" {
		t.Errorf("artifact one byte over 1 MiB returned %v", err)
	}
	if err := Decode([]byte(strings.Repeat("[", 32)+strings.Repeat("]", 32)), &value); err != nil {
		t.Errorf("32 nesting levels refused: %v", err)
	}
	if err := Decode([]byte(strings.Repeat("[", 33)+strings.Repeat("]", 33)), &value); err == nil || err.Error() != "JSON nesting exceeds 32" {
		t.Errorf("33 nesting levels returned %v", err)
	}
	var obs observation
	if err := Decode([]byte(`{"quality": 1, "extra": 2}`), &obs); err == nil || !strings.HasPrefix(err.Error(), "decode artifact: ") {
		t.Errorf("unknown field returned %v", err)
	}
	if err := Decode([]byte(`{"quality": 1, "notes": "\ud800"}`), &obs); err == nil {
		t.Error("unpaired surrogate escape decoded; encoding/json would have replaced it with U+FFFD")
	}
}
