package clientjson

import (
	"bytes"
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

func TestAppendMember_PreservesExistingBytesAndOrder(t *testing.T) {
	raw := []byte("{\"note\":\"\\u003c\",\r\n \"n\":9007199254740993 }\r\n")
	got, err := AppendMember(raw, "added", jsontext.Value(`{"ok":true}`))
	if err != nil {
		t.Fatal(err)
	}
	want := "{\"note\":\"\\u003c\",\r\n \"n\":9007199254740993 ,\"added\":{\"ok\":true}}\r\n"
	if string(got) != want {
		t.Fatalf("AppendMember = %q, want %q", got, want)
	}
	empty, err := AppendMember([]byte("{ }"), "first", jsontext.Value(`1`))
	if err != nil || string(empty) != "{ \"first\":1}" {
		t.Fatalf("empty append = %q, %v", empty, err)
	}
}

func TestAppendMember_RejectsDuplicateAndInvalidInputs(t *testing.T) {
	for _, raw := range []string{`{"a":1}`, `{"a":1,"a":2}`, `[]`, `{} {}`} {
		if got, err := AppendMember([]byte(raw), "a", jsontext.Value(`2`)); err == nil || got != nil {
			t.Errorf("AppendMember(%q) = %q, %v", raw, got, err)
		}
	}
	if got, err := AppendMember([]byte(`{}`), "a", jsontext.Value(`bad`)); err == nil || got != nil {
		t.Errorf("invalid value accepted: %q, %v", got, err)
	}
}

// Negative and boundary: a member is appended to the outer object even when the first closing
// brace in the text belongs to a nested object, empty or not, and the nested values stay as
// they were.
func TestAppendMember_Negative_NestedObjectsKeepTheirMembers(t *testing.T) {
	for name, raw := range map[string]string{
		"nested object first": `{"a":{"type":"http","url":"https://x"}}`,
		"empty nested object": `{"a":{"env":{}},"b":1}`,
		"indented, deep":      "{\n  \"a\": {\n    \"env\": {\n      \"K\": \"v\"\n    }\n  }\n}\n",
	} {
		out, err := AppendMember([]byte(raw), "new", jsontext.Value(`{"command":"/usr/bin/true"}`))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		object, err := DecodeObject(out)
		if err != nil {
			t.Fatalf("%s: result %s: %v", name, out, err)
		}
		if _, ok := object.Get("new"); !ok || len(object) < 2 {
			t.Fatalf("%s: new member not at the top level: %s", name, out)
		}
		before, err := DecodeObject([]byte(raw))
		if err != nil {
			t.Fatalf("%s: fixture: %v", name, err)
		}
		if a, _ := object.Get("a"); !bytes.Equal(a, mustGet(t, before, "a")) {
			t.Fatalf("%s: nested member changed: %s", name, out)
		}
	}
}

func mustGet(t *testing.T, object Object, name string) jsontext.Value {
	t.Helper()
	value, ok := object.Get(name)
	if !ok {
		t.Fatalf("no member %q", name)
	}
	return value
}
