package clientjson

import (
	"encoding/json/jsontext"
	"testing"
)

// Positive: decoding and encoding keeps member order and number literals, and With replaces a
// member in place and appends a new one last.
func TestObject_Positive_RoundTripKeepsOrderAndLiterals(t *testing.T) {
	object, err := DecodeObject([]byte(`{"z": 12345678901234567890, "a": {"y": 1.50, "b": 1e2}}`))
	if err != nil {
		t.Fatal(err)
	}
	object = object.With("z", jsontext.Value(`0`)).With("new", jsontext.Value(`"v"`))
	encoded, err := object.Encode()
	if err != nil {
		t.Fatal(err)
	}
	want := "{\n  \"z\": 0,\n  \"a\": {\n    \"y\": 1.50,\n    \"b\": 1e2\n  },\n  \"new\": \"v\"\n}\n"
	if string(encoded) != want {
		t.Fatalf("encoded %q, want %q", encoded, want)
	}
	if value, ok := object.Get("a"); !ok || StringValue(value) != "" {
		t.Fatalf("Get(a) = %s, %v", value, ok)
	}
	if value, ok := object.Get("new"); !ok || StringValue(value) != "v" {
		t.Fatalf("Get(new) = %s, %v", value, ok)
	}
}

// Negative: a non-object value and a duplicate name are refused; an absent member reads as
// absent and a non-string reads as "".
func TestObject_Negative_RefusesNonObjects(t *testing.T) {
	for _, raw := range []string{`[]`, `"x"`, `{"a": 1, "a": 2}`, ``, `{"a"`} {
		if _, err := DecodeObject([]byte(raw)); err == nil {
			t.Errorf("%q accepted", raw)
		}
	}
	object, err := DecodeObject([]byte(`{"n": 1}`))
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := object.Get("missing"); ok {
		t.Error("absent member found")
	}
	if value, _ := object.Get("n"); StringValue(value) != "" {
		t.Error("number read as a string")
	}
}

// Boundary: the empty object decodes to no members and encodes as {}.
func TestObject_Boundary_EmptyObject(t *testing.T) {
	object, err := DecodeObject([]byte(` {} `))
	if err != nil || len(object) != 0 {
		t.Fatalf("DecodeObject = %v, %v", object, err)
	}
	encoded, err := object.Encode()
	if err != nil || string(encoded) != "{}\n" {
		t.Fatalf("Encode = %q, %v", encoded, err)
	}
}
