package clientjson

import (
	"context"
	"strings"
	"testing"
)

// Positive: one object, nested and with every value kind, validates.
func TestValidate_Positive_OneObject(t *testing.T) {
	raw := []byte(`{"a": [1, 2.5, "x", true, null, {"b": {}}], "c": 12345678901234567890}`)
	if err := Validate(t.Context(), raw); err != nil {
		t.Fatal(err)
	}
}

// Negative: empty input, a non-object root, two roots, a duplicate name, invalid JSON, a nil
// and a cancelled context are refused.
func TestValidate_Negative_RefusesAmbiguousInput(t *testing.T) {
	for name, raw := range map[string]string{
		"empty":     "",
		"array":     "[]",
		"two roots": "{} {}",
		"duplicate": `{"a": 1, "a": 2}`,
		"invalid":   `{"a": }`,
	} {
		if err := Validate(t.Context(), []byte(raw)); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	//nolint:staticcheck // SA1012: a nil context is the input under test.
	if err := Validate(nil, []byte("{}")); err == nil {
		t.Error("nil context accepted")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := Validate(ctx, []byte("{}")); err == nil {
		t.Error("cancelled context accepted")
	}
}

// Boundary: MaxDepth levels validate and one more is refused; MaxBytes validates and one byte
// more is refused.
func TestValidate_Boundary_DepthAndSize(t *testing.T) {
	nested := func(depth int) []byte {
		return []byte(strings.Repeat(`{"a":`, depth-1) + "{}" + strings.Repeat("}", depth-1))
	}
	if err := Validate(t.Context(), nested(MaxDepth)); err != nil {
		t.Fatalf("depth %d: %v", MaxDepth, err)
	}
	if err := Validate(t.Context(), nested(MaxDepth+1)); err == nil {
		t.Fatalf("depth %d accepted", MaxDepth+1)
	}
	padded := func(size int) []byte {
		return []byte(`{"a": "` + strings.Repeat("x", size-len(`{"a": ""}`)) + `"}`)
	}
	if err := Validate(t.Context(), padded(MaxBytes)); err != nil {
		t.Fatalf("%d bytes: %v", MaxBytes, err)
	}
	if err := Validate(t.Context(), padded(MaxBytes+1)); err == nil {
		t.Fatalf("%d bytes accepted", MaxBytes+1)
	}
}
