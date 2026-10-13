package clientjson

import (
	"encoding/json/jsontext"
	"errors"
	"strings"
	"testing"
)

// tabbed is a tab-indented document such as a formatter set to tabs writes (#1251).
const tabbed = "{\n\t\"$schema\": \"https://docs.renovatebot.com/renovate-schema.json\",\n\t\"extends\": [\n\t\t\"config:recommended\"\n\t]\n}\n"

// Positive, negative and boundary: the unit is a tab, the measured spaces, or none for a
// document on one line or written flush left.
func TestIndent(t *testing.T) {
	for raw, want := range map[string]string{
		tabbed:               "\t",
		"{\n  \"a\": 1\n}\n": "  ",
		"{\n    \"a\": {\n        \"b\": 1\n    }\n}": "    ",
		"{\r\n   \"a\": 1\r\n}\r\n":                   "   ",
		`{"a": 1}`:                                    "",
		"{\n\"a\": 1\n}\n":                            "",
		"":                                            "",
	} {
		if got := Indent([]byte(raw)); got != want {
			t.Errorf("Indent(%q) = %q, want %q", raw, got, want)
		}
	}
}

// Positive: a member appended to a tab-indented document and one replaced two levels down in a
// four-space document are written in that document's indentation; every other byte stays.
func TestSetAt_Positive_KeepsTheDocumentLayout(t *testing.T) {
	got, err := SetAt([]byte(tabbed), []string{"packageRules"}, jsontext.Value(`[{"enabled":false,"matchFileNames":["a.yml"]}]`))
	if err != nil {
		t.Fatal(err)
	}
	want := strings.TrimSuffix(tabbed, "\n}\n") + ",\n\t\"packageRules\": [\n\t\t{\n\t\t\t\"enabled\": false,\n" +
		"\t\t\t\"matchFileNames\": [\n\t\t\t\t\"a.yml\"\n\t\t\t]\n\t\t}\n\t]\n}\n"
	if string(got) != want {
		t.Fatalf("SetAt on tabs =\n%q\nwant\n%q", got, want)
	}

	spaced := "{\n    \"name\": \"x\",\n    \"hooks\": {\n        \"Stop\": [],\n        \"PreToolUse\": [1]\n    },\n    \"last\": true\n}"
	got, err = SetAt([]byte(spaced), []string{"hooks", "PreToolUse"}, jsontext.Value(`[1,{"k":"v"}]`))
	if err != nil {
		t.Fatal(err)
	}
	want = "{\n    \"name\": \"x\",\n    \"hooks\": {\n        \"Stop\": [],\n        \"PreToolUse\": [\n            1,\n" +
		"            {\n                \"k\": \"v\"\n            }\n        ]\n    },\n    \"last\": true\n}"
	if string(got) != want {
		t.Fatalf("nested SetAt =\n%q\nwant\n%q", got, want)
	}
}

// Negative: a bad path, an invalid document or value, a path through a non-object or a missing
// object is refused with nothing returned.
func TestSetAt_Negative_RefusesBadEdits(t *testing.T) {
	long := make([]string, MaxPath+1)
	for name, c := range map[string]struct {
		raw   string
		path  []string
		value string
	}{
		"empty path":       {tabbed, nil, `1`},
		"path past bound":  {tabbed, long, `1`},
		"duplicate member": {`{"a": 1, "a": 2}`, []string{"b"}, `1`},
		"not an object":    {`[1]`, []string{"a"}, `1`},
		"invalid value":    {tabbed, []string{"a"}, `{`},
		"through a string": {tabbed, []string{"$schema", "a"}, `1`},
		"missing object":   {tabbed, []string{"absent", "a"}, `1`},
	} {
		if out, err := SetAt([]byte(c.raw), c.path, jsontext.Value(c.value)); err == nil || out != nil {
			t.Errorf("%s: SetAt = %q, %v; want a refusal", name, out, err)
		}
	}
}

// Boundary: an empty one-line object comes back in two spaces with its final newline, a
// document without one stays without, CRLF stays CRLF, an object written on one line inside a
// multi-line document stays on one line, and setting a present name replaces it.
func TestSetAt_Boundary_LayoutEdges(t *testing.T) {
	cases := []struct {
		name, raw string
		path      []string
		value     string
		want      string
	}{
		{"empty one-line object", "{}\n", []string{"a"}, `[1]`, "{\n  \"a\": [\n    1\n  ]\n}\n"},
		{"no final newline", "{\n\t\"a\": 1\n}", []string{"b"}, `2`, "{\n\t\"a\": 1,\n\t\"b\": 2\n}"},
		{"CRLF", "{\r\n  \"a\": 1\r\n}\r\n", []string{"b"}, `{"c":1}`, "{\r\n  \"a\": 1,\r\n  \"b\": {\r\n    \"c\": 1\r\n  }\r\n}\r\n"},
		{"inline object", "{\n  \"o\": {\"x\": 1}\n}\n", []string{"o", "y"}, `[2]`, "{\n  \"o\": {\"x\": 1, \"y\": [2]}\n}\n"},
		{"present name", "{\n\t\"a\": 1,\n\t\"b\": 2\n}\n", []string{"a"}, `3`, "{\n\t\"a\": 3,\n\t\"b\": 2\n}\n"},
	}
	for _, c := range cases {
		got, err := SetAt([]byte(c.raw), c.path, jsontext.Value(c.value))
		if err != nil || string(got) != c.want {
			t.Errorf("%s: SetAt = %q, %v; want %q", c.name, got, err, c.want)
			continue
		}
		if err := Validate(t.Context(), got); err != nil {
			t.Errorf("%s: the result does not load with the duplicate-refusing reader: %v", c.name, err)
		}
	}
}

// listed is a tab-indented document whose array holds an adopter entry written on one line.
const listed = "{\n\t\"packageRules\": [\n\t\t{\"matchPackageNames\": [\"x\"], \"enabled\": false},\n\t\t{\n\t\t\t\"description\": \"b\"\n\t\t}\n\t]\n}\n"

// Positive: an element is appended after the last one and one is replaced in place, each in the
// document's indentation, while the adopter's one-line entry keeps its form.
func TestSetElement_Positive_EditsOneElement(t *testing.T) {
	got, err := SetElement([]byte(listed), []string{"packageRules"}, 2, jsontext.Value(`{"description":"c"}`))
	if err != nil {
		t.Fatal(err)
	}
	want := strings.Replace(listed, "\t\t}\n\t]", "\t\t},\n\t\t{\n\t\t\t\"description\": \"c\"\n\t\t}\n\t]", 1)
	if string(got) != want {
		t.Fatalf("append =\n%q\nwant\n%q", got, want)
	}
	got, err = SetElement([]byte(listed), []string{"packageRules"}, 1, jsontext.Value(`{"description":"d"}`))
	if err != nil || string(got) != strings.Replace(listed, `"b"`, `"d"`, 1) {
		t.Fatalf("replace = %q, %v", got, err)
	}
}

// Negative: an index past the array, a negative one, a removal at the length, a member that is
// no array, a missing member and an invalid value are refused.
func TestSetElement_Negative_RefusesBadEdits(t *testing.T) {
	path := []string{"packageRules"}
	for name, c := range map[string]struct {
		path  []string
		index int
		value jsontext.Value
	}{
		"past the end":    {path, 3, jsontext.Value(`1`)},
		"negative":        {path, -1, jsontext.Value(`1`)},
		"remove past end": {path, 2, nil},
		"not an array":    {[]string{"packageRules", "x"}, 0, jsontext.Value(`1`)},
		"missing member":  {[]string{"absent"}, 0, jsontext.Value(`1`)},
		"invalid value":   {path, 0, jsontext.Value(`[`)},
		"no path":         {nil, 0, jsontext.Value(`1`)},
	} {
		if out, err := SetElement([]byte(listed), c.path, c.index, c.value); err == nil || out != nil {
			t.Errorf("%s: SetElement = %q, %v; want a refusal", name, out, err)
		}
	}
	if _, err := SetElement([]byte(`{"a": "x"}`), []string{"a"}, 0, jsontext.Value(`1`)); !errors.Is(err, errNoArray) {
		t.Errorf("a string member was edited as an array: %v", err)
	}
}

// Boundary: removing an appended element gives back the document byte for byte, removing the
// first and the only element leaves well-formed arrays, and an append to an empty array is
// written across lines in the document's indentation.
func TestSetElement_Boundary_RemoveAndEmpty(t *testing.T) {
	path := []string{"packageRules"}
	added, err := SetElement([]byte(listed), path, 2, jsontext.Value(`{"description":"c"}`))
	if err != nil {
		t.Fatal(err)
	}
	if back, err := SetElement(added, path, 2, nil); err != nil || string(back) != listed {
		t.Fatalf("removing the appended element = %q, %v; want the original", back, err)
	}
	first, err := SetElement([]byte(listed), path, 0, nil)
	if err != nil || string(first) != "{\n\t\"packageRules\": [\n\t\t{\n\t\t\t\"description\": \"b\"\n\t\t}\n\t]\n}\n" {
		t.Fatalf("removing the first element = %q, %v", first, err)
	}
	only, err := SetElement(first, path, 0, nil)
	if err != nil || string(only) != "{\n\t\"packageRules\": []\n}\n" {
		t.Fatalf("removing the only element = %q, %v", only, err)
	}
	refilled, err := SetElement(only, path, 0, jsontext.Value(`"x"`))
	if err != nil || string(refilled) != "{\n\t\"packageRules\": [\n\t\t\"x\"\n\t]\n}\n" {
		t.Fatalf("append to an empty array = %q, %v", refilled, err)
	}
}
