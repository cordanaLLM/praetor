package wirejson

import (
	"encoding/json"
	"strings"
	"testing"
)

type sample struct {
	Name  string                     `json:"name"`
	Count *int                       `json:"count,omitempty"`
	Skip  string                     `json:"-"`
	Extra map[string]json.RawMessage `json:"-"`
}

func (s *sample) UnmarshalJSON(data []byte) error {
	type plain sample
	return Decode(data, (*plain)(s), &s.Extra)
}

func (s sample) MarshalJSON() ([]byte, error) {
	type plain sample
	return Encode(plain(s), s.Extra)
}

// Positive: members the struct does not name survive a round trip, nested values verbatim.
func TestRoundTripKeepsUnnamedMembers(t *testing.T) {
	const in = `{"additionalProperties":false,"count":2,"name":"n","nested":{"a":[1,2,{"b":null}]}}`
	var got sample
	if err := json.Unmarshal([]byte(in), &got); err != nil {
		t.Fatal(err)
	}
	if got.Name != "n" || got.Count == nil || *got.Count != 2 || len(got.Extra) != 2 {
		t.Fatalf("decoded %+v", got)
	}
	out, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != in {
		t.Fatalf("round trip = %s, want %s", out, in)
	}
}

// Negative: invalid JSON, a wrong member type and a document that is no object are refused; a
// named member in Extra never overrides the field that writes it.
func TestRefusalsAndPrecedence(t *testing.T) {
	var got sample
	for _, bad := range []string{`{`, `{"name":3}`, `[1]`, ``} {
		if err := json.Unmarshal([]byte(bad), &got); err == nil {
			t.Errorf("accepted %q", bad)
		}
	}
	out, err := json.Marshal(sample{Name: "field", Extra: map[string]json.RawMessage{"name": json.RawMessage(`"extra"`), "other": json.RawMessage(`1`)}})
	if err != nil || string(out) != `{"name":"field","other":1}` {
		t.Fatalf("Marshal = %s %v", out, err)
	}
}

// Boundary: no unnamed member leaves Extra nil and the output unchanged, a null document decodes
// to the zero value, and a key matches a field ignoring case like encoding/json.
func TestBoundaries(t *testing.T) {
	var got sample
	if err := json.Unmarshal([]byte(`{"NAME":"x"}`), &got); err != nil || got.Name != "x" || got.Extra != nil {
		t.Fatalf("case-insensitive field: %+v %v", got, err)
	}
	got = sample{}
	if err := json.Unmarshal([]byte(`null`), &got); err != nil || got.Extra != nil {
		t.Fatalf("null: %+v %v", got, err)
	}
	out, err := json.Marshal(sample{Name: "a"})
	if err != nil || strings.Contains(string(out), "Extra") || string(out) != `{"name":"a"}` {
		t.Fatalf("Marshal = %s %v", out, err)
	}
	if names := fieldNames(nil); names != nil {
		t.Fatalf("fieldNames(nil) = %v", names)
	}
}
