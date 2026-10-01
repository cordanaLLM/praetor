package repairrun

import (
	"strings"
	"testing"
)

// longS is U+017F LATIN SMALL LETTER LONG S, which strings.EqualFold and therefore
// encoding/json match to s, while strings.ToLower leaves it alone.
const longS = "\u017f"

type strictRecord struct {
	S string `json:"s"`
}

// TestDecodeConfigJSON_Negative_RefusesEveryEncodingJSONAlias covers issue #310: lower-casing
// missed the long s, so the alias reached the decoder and only the round-trip caught it.
func TestDecodeConfigJSON_Negative_RefusesEveryEncodingJSONAlias(t *testing.T) {
	for _, input := range []string{`{"s":"a","S":"b"}`, `{"s":"a","` + longS + `":"b"}`} {
		var value strictRecord
		if err := decodeConfigJSON([]byte(input), &value); err == nil || err.Error() != "duplicate or aliased repair JSON field" {
			t.Errorf("%s returned %v", input, err)
		}
	}
	var value strictRecord
	if err := decodeConfigJSON([]byte(`{"s":null}`), &value); err == nil || err.Error() != "invalid or null repair JSON value" {
		t.Errorf("null returned %v", err)
	}
	if err := decodeExactJSON([]byte(`{"s":"a"} {}`), &value, true); err == nil || err.Error() != "invalid UTF-8 JSON" {
		t.Errorf("second document returned %v", err)
	}
}

func TestDecodeConfigJSON_Boundary_ByteBound(t *testing.T) {
	exact := `{"s":"` + strings.Repeat("x", maxRepairJSONBytes-8) + `"}`
	var value strictRecord
	if err := decodeExactJSON([]byte(exact), &value, true); err != nil || len(value.S) != maxRepairJSONBytes-8 {
		t.Fatalf("document of exactly %d bytes refused: %v", maxRepairJSONBytes, err)
	}
	if err := decodeExactJSON([]byte(exact+" "), &value, true); err == nil || err.Error() != "invalid UTF-8 JSON" {
		t.Errorf("document one byte over the bound returned %v", err)
	}
	if err := decodeExactJSON([]byte(`{"s":"`+longS+`"}`), &value, false); err != nil || value.S != longS {
		t.Errorf("the long s as a value refused: %q, %v", value.S, err)
	}
}

func TestProviderValidateJSON_Negative_RefusesEveryEncodingJSONAlias(t *testing.T) {
	for _, raw := range []string{`{"summary":"x","Summary":"y","edits":[]}`, `{"s":"x","` + longS + `":"y"}`} {
		if err := providerValidateJSON([]byte(raw)); err == nil || err.Error() != "repair provider JSON contains duplicate or case-aliased fields" {
			t.Errorf("%s returned %v", raw, err)
		}
	}
	if err := providerValidateJSON([]byte(`{"summary":"x","edits":[]}`)); err != nil {
		t.Errorf("valid proposal refused: %v", err)
	}
}
