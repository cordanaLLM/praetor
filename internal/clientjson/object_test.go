package clientjson

import (
	"encoding/json/jsontext"
	"errors"
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

// Positive: ReplaceMember swaps only the value bytes of the named top-level member, so a compact
// layout, CRLF line endings, < escapes and a nested member of the same name stay as written.
func TestReplaceMember_Positive_KeepsEveryOtherByte(t *testing.T) {
	raw := "{\"notes\":\"a \\u003c= b\",\r\n \"inner\": {\"platform\": \"x\"},\r\n\"platform\" :\t\"old\\/name\" }\r\n"
	got, err := ReplaceMember([]byte(raw), "platform", jsontext.Value(`"new"`))
	if err != nil {
		t.Fatal(err)
	}
	want := "{\"notes\":\"a \\u003c= b\",\r\n \"inner\": {\"platform\": \"x\"},\r\n\"platform\" :\t\"new\" }\r\n"
	if string(got) != want {
		t.Fatalf("ReplaceMember = %q, want %q", got, want)
	}
}

// Negative: an absent member, a duplicate name, trailing text, a non-object and an invalid
// replacement value are refused, with nothing returned.
func TestReplaceMember_Negative_RefusesWhatItCannotReplace(t *testing.T) {
	for name, tc := range map[string]struct{ raw, value string }{
		"absent member":  {`{"version": 1}`, `"new"`},
		"nested only":    {`{"inner": {"platform": "x"}}`, `"new"`},
		"duplicate name": {`{"platform": "a", "platform": "b"}`, `"new"`},
		"trailing text":  {`{"platform": "a"} {}`, `"new"`},
		"not an object":  {`["platform"]`, `"new"`},
		"invalid value":  {`{"platform": "a"}`, `"unterminated`},
		"two values":     {`{"platform": "a"}`, `"a" "b"`},
	} {
		got, err := ReplaceMember([]byte(tc.raw), "platform", jsontext.Value(tc.value))
		if err == nil || got != nil {
			t.Errorf("%s: accepted: %q, %v", name, got, err)
		}
	}
	if _, err := ReplaceMember([]byte(`{"version": 1}`), "platform", jsontext.Value(`"new"`)); !errors.Is(err, errNoMember) {
		t.Errorf("absent member error = %v, want errNoMember", err)
	}
}

// Boundary: the first and the last member are replaced in place, a value of another type and
// length fits, and the empty string is a value like any other.
func TestReplaceMember_Boundary_FirstLastAndOtherTypes(t *testing.T) {
	for name, tc := range map[string]struct{ raw, member, value, want string }{
		"only member":  {`{"a":1}`, "a", `""`, `{"a":""}`},
		"first member": {`{"a": [1, 2], "b": 2}`, "a", `{"k": null}`, `{"a": {"k": null}, "b": 2}`},
		"last member":  {"{\n  \"a\": 1,\n  \"b\": \"x\"\n}\n", "b", `12345678901234567890`, "{\n  \"a\": 1,\n  \"b\": 12345678901234567890\n}\n"},
	} {
		got, err := ReplaceMember([]byte(tc.raw), tc.member, jsontext.Value(tc.value))
		if err != nil || string(got) != tc.want {
			t.Errorf("%s: ReplaceMember = %q, %v; want %q", name, got, err, tc.want)
		}
	}
}
