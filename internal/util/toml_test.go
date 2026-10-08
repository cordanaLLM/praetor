// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package util_test

import (
	"slices"
	"testing"

	"github.com/cordanaLLM/praetor/internal/util"
)

// Positive: table and array-of-tables headers, padded or commented, normalize to their names.
func TestTOMLTableNamePositive(t *testing.T) {
	for header, want := range map[string]string{
		"[extend]":                 "extend",
		"[ extend ]":               "extend",
		"[[annotations]]":          "[annotations]",
		"  [[ annotations ]]  # x": "[annotations]",
		`["quoted"]`:               "quoted",
	} {
		if got := util.TOMLTableName(header); got != want {
			t.Errorf("TOMLTableName(%q) = %q, want %q", header, got, want)
		}
	}
}

// Negative: a key line or a comment is not a header of the table it mentions.
func TestTOMLTableNameNegative(t *testing.T) {
	for _, line := range []string{`path = ["[[annotations]]"]`, "# [[annotations]]"} {
		if got := util.TOMLTableName(line); got == "[annotations]" {
			t.Errorf("TOMLTableName(%q) read a header", line)
		}
	}
}

// Boundary: an empty line and an empty header yield the empty name.
func TestTOMLTableNameBoundary(t *testing.T) {
	for _, line := range []string{"", "[]", "  # only a comment"} {
		if got := util.TOMLTableName(line); got != "" {
			t.Errorf("TOMLTableName(%q) = %q, want empty", line, got)
		}
	}
}

// Positive: a key line splits into its key, dotted keys padded or not, and its trimmed value.
func TestTOMLKeyValuePositive(t *testing.T) {
	for line, want := range map[string][2]string{
		`edition = "2021"`:             {"edition", `"2021"`},
		"package . edition='2021' # c": {"package.edition", "'2021' # c"},
		"  useDefault=true  ":          {"useDefault", "true"},
		`edition.workspace = true`:     {"edition.workspace", "true"},
	} {
		key, value, ok := util.TOMLKeyValue(line)
		if !ok || key != want[0] || value != want[1] {
			t.Errorf("TOMLKeyValue(%q) = %q, %q, %v; want %q, %q", line, key, value, ok, want[0], want[1])
		}
	}
}

// Negative: a line without an equals sign, or with no key before it, assigns nothing.
func TestTOMLKeyValueNegative(t *testing.T) {
	for _, line := range []string{"[package]", "# a comment", `= "2021"`, "   =  "} {
		if key, value, ok := util.TOMLKeyValue(line); ok {
			t.Errorf("TOMLKeyValue(%q) = %q, %q, true; want no assignment", line, key, value)
		}
	}
}

// Boundary: an empty value is still an assignment, and only the first equals sign splits.
func TestTOMLKeyValueBoundary(t *testing.T) {
	if key, value, ok := util.TOMLKeyValue("name ="); !ok || key != "name" || value != "" {
		t.Errorf("empty value: got %q, %q, %v", key, value, ok)
	}
	if key, value, ok := util.TOMLKeyValue(`args = "a=b"`); !ok || key != "args" || value != `"a=b"` {
		t.Errorf("equals sign in the value: got %q, %q, %v", key, value, ok)
	}
}

// inlineFields collects what TOMLInlineTableFields visits, each field as "key=value".
func inlineFields(value string) ([]string, bool) {
	var fields []string
	opened := util.TOMLInlineTableFields(value, func(key, fieldValue string) {
		fields = append(fields, key+"="+fieldValue)
	})
	return fields, opened
}

// Positive: the fields of an inline table as Cargo dependencies and inherited editions are
// written, in order, keys padded or dotted, values raw and trimmed.
func TestTOMLInlineTableFieldsPositive(t *testing.T) {
	for value, want := range map[string][]string{
		`{ workspace = true }`:                     {"workspace=true"},
		`{ version = "1.0", path = "../core" }`:    {`version="1.0"`, `path="../core"`},
		`{version="1",default-features=false}`:     {`version="1"`, "default-features=false"},
		`{ "version" = '2', a . b = 1 } # comment`: {`"version"='2'`, "a.b=1"},
	} {
		if got, opened := inlineFields(value); !opened || !slices.Equal(got, want) {
			t.Errorf("TOMLInlineTableFields(%q) visited %q (opened %v), want %q", value, got, opened, want)
		}
	}
}

// Negative: a value that opens no inline table visits nothing, and a fragment with no equals
// sign or no key is no field.
func TestTOMLInlineTableFieldsNegative(t *testing.T) {
	for _, value := range []string{`"1.0"`, "true", `["a"]`, ` { workspace = true }`, ""} {
		if got, opened := inlineFields(value); opened || len(got) != 0 {
			t.Errorf("TOMLInlineTableFields(%q) visited %q (opened %v), want no inline table", value, got, opened)
		}
	}
	if got, opened := inlineFields(`{ optional, = 1, workspace = true }`); !opened || !slices.Equal(got, []string{"workspace=true"}) {
		t.Errorf("fragments without a key: visited %q (opened %v)", got, opened)
	}
}

// Boundary: an empty table visits nothing, the fields stop at the first closing brace, so a
// comment holding one is not read, an unclosed table runs to the end of the line, and an array
// field yields its first fragment only.
func TestTOMLInlineTableFieldsBoundary(t *testing.T) {
	for value, want := range map[string][]string{
		"{}":                                   nil,
		`{ workspace = true } # {x = 1}`:       {"workspace=true"},
		`{ version = "1",`:                     {`version="1"`},
		`{ features = ["a", "b"], x = 1 }`:     {`features=["a"`, "x=1"},
		`{ git = "https://host/r", tag = "" }`: {`git="https://host/r"`, `tag=""`},
	} {
		if got, opened := inlineFields(value); !opened || !slices.Equal(got, want) {
			t.Errorf("TOMLInlineTableFields(%q) visited %q (opened %v), want %q", value, got, opened, want)
		}
	}
}

// Positive: basic and literal single-line strings, with or without a trailing comment, and the
// escape sequences of a basic string decoded: a REUSE glob's escaped star, an escaped quote, the
// one-letter escapes and the hexadecimal codes, as tomlkit decodes them.
func TestTOMLStringValuePositive(t *testing.T) {
	for value, want := range map[string]string{
		`"2021"`:                 "2021",
		`'2021'`:                 "2021",
		`"2021" # workspace`:     "2021",
		`'C:\path'`:              `C:\path`,
		`"a\\*"`:                 `a\*`,
		`"20\"21"`:               `20"21`,
		`"\b\t\n\f\r\e"`:         "\b\t\n\f\r\x1b",
		`"\x41\u00e9\U0001F600"`: "A\u00e9\U0001F600",
		`"a\"#b" # c`:            `a"#b`,
	} {
		if got, ok := util.TOMLStringValue(value); !ok || got != want {
			t.Errorf("TOMLStringValue(%q) = %q, %v; want %q", value, got, ok, want)
		}
	}
}

// Negative: numbers, booleans, inline tables, unterminated and multi-line strings, text after
// the string, and basic strings holding an escape TOML does not define, a short or invalid code
// or a surrogate code included, are refused, never guessed at.
func TestTOMLStringValueNegative(t *testing.T) {
	for _, value := range []string{"2021", "true", "{ workspace = true }", `"2021`, `"""2021"""`,
		`"2021" extra`, `"a\q"`, `"a\/"`, `"\u12"`, `"\x4g"`, `"\uD800"`, `"\UFFFFFFFF"`, `"a\"`, `"a\`, ""} {
		if got, ok := util.TOMLStringValue(value); ok {
			t.Errorf("TOMLStringValue(%q) = %q, true; want refused", value, got)
		}
	}
}

// Boundary: the empty string is a string, a comment may follow without a space, an escape at the
// end of the text decodes, and the largest Unicode scalar value is a valid code.
func TestTOMLStringValueBoundary(t *testing.T) {
	for value, want := range map[string]string{`""`: "", `''`: "", `'x'#c`: "x", `"a\\"`: `a\`, `"\U0010FFFF"`: "\U0010FFFF"} {
		if got, ok := util.TOMLStringValue(value); !ok || got != want {
			t.Errorf("TOMLStringValue(%q) = %q, %v; want %q", value, got, ok, want)
		}
	}
}

// Positive: single- and multi-line arrays of basic and literal strings, with comments and a
// trailing comma, as Cargo workspace members are written.
func TestTOMLStringArrayPositive(t *testing.T) {
	for text, want := range map[string][]string{
		`["crates/*", 'tools/c']`:                                 {"crates/*", "tools/c"},
		"[\n    \"crates/*\", # every crate\n    'tools/c',\n]\n": {"crates/*", "tools/c"},
		"[\"a\"] # members\n":                                     {"a"},
		"[ # opened\r\n  \"a\" ,\"b\"\r\n]\r\n":                   {"a", "b"},
		`['C:\dir']`:                                              {`C:\dir`},
		`["a\"b", "c\\*"]`:                                        {`a"b`, `c\*`},
	} {
		items, closed, ok := util.TOMLStringArray(text)
		if !ok || !closed || !slices.Equal(items, want) {
			t.Errorf("TOMLStringArray(%q) = %q, closed %v, ok %v; want %q", text, items, closed, ok, want)
		}
	}
}

// Negative: a value that is no array, an element that is not a single-line string TOML can
// decode, and text after the closing bracket are refused.
func TestTOMLStringArrayNegative(t *testing.T) {
	for _, text := range []string{"", `"a"`, "{ a = 1 }", "[1, 2]", "[true]", `[["a"]]`, `["a\qb"]`,
		`["""a"""]`, `["a""b"]`, "[\"a\n\"]", `["a"] extra`, `["a" ]]`} {
		if items, closed, ok := util.TOMLStringArray(text); ok {
			t.Errorf("TOMLStringArray(%q) = %q, closed %v, true; want refused", text, items, closed)
		}
	}
}

// Boundary: an empty array is closed and holds nothing, and an array whose closing bracket is on
// a later line is open until that line is appended.
func TestTOMLStringArrayBoundary(t *testing.T) {
	if items, closed, ok := util.TOMLStringArray("[]"); !ok || !closed || len(items) != 0 {
		t.Errorf("[] = %q, closed %v, ok %v; want an empty closed array", items, closed, ok)
	}
	open := "[\n  \"a\", # first\n"
	if items, closed, ok := util.TOMLStringArray(open); !ok || closed || !slices.Equal(items, []string{"a"}) {
		t.Errorf("open array = %q, closed %v, ok %v; want [a] still open", items, closed, ok)
	}
	if items, closed, ok := util.TOMLStringArray(open + "  \"b\"\n]\n"); !ok || !closed || !slices.Equal(items, []string{"a", "b"}) {
		t.Errorf("closed array = %q, closed %v, ok %v; want [a b] closed", items, closed, ok)
	}
}

// feedTOMLValue feeds lines to a fresh scan and returns, per line, whether the value ended on it,
// stopping at the first line the scan refuses.
func feedTOMLValue(lines ...string) (ends []bool, ok bool) {
	var scan util.TOMLValueScan
	for _, line := range lines {
		done, good := scan.Feed(line)
		if !good {
			return ends, false
		}
		ends = append(ends, done)
	}
	return ends, true
}

// Positive: single-line values end on their line whatever they hold (escapes, brackets inside
// strings, a comment), and an array of escaped strings, a multi-line basic string with an
// escaped quote and a multi-line literal string holding a table header end on their closing line.
func TestTOMLValueScanPositive(t *testing.T) {
	for name, lines := range map[string][]string{
		"escaped string":       {`"2024 A \"B\" C" # x`},
		"bracket in string":    {`["a]", 'b[', "c#d"] # ]`},
		"inline table":         {`{ a = "}", b = [1, 2] }`},
		"array of escapes":     {`[`, `  "2024 A \"B\" C",`, `  "x", # ]`, `]`},
		"multi-line basic":     {`"""`, `text with \""" inside`, `and a closing run """"`},
		"multi-line literal":   {`'''`, `[[annotations]]`, `path = "x"'''`},
		"bare value":           {`1`},
		"two-quote empty":      {`""`},
		"escaped backslash":    {`"C:\\"`},
		"line-ending escape":   {`"""a \`, `b"""`},
		"string after closing": {`"""a""" # "`},
	} {
		ends, ok := feedTOMLValue(lines...)
		if !ok || len(ends) != len(lines) || !ends[len(ends)-1] || slices.Contains(ends[:len(ends)-1], true) {
			t.Errorf("%s: ends %v, ok %v; want the value to end on its last line only", name, ends, ok)
		}
	}
}

// Negative: a single-line string left open and a closing bracket with nothing open are refused.
func TestTOMLValueScanNegative(t *testing.T) {
	for _, line := range []string{`"open`, `'open`, `"escaped close\"`, `]`, `[1]]`, `}`} {
		if ends, ok := feedTOMLValue(line); ok {
			t.Errorf("Feed(%q) = %v, ok; want refused", line, ends)
		}
	}
}

// Boundary: an empty line ends an empty value, a value left open by an array or a multi-line
// string does not end, and a comment line inside an open array leaves it open.
func TestTOMLValueScanBoundary(t *testing.T) {
	if ends, ok := feedTOMLValue(""); !ok || !ends[0] {
		t.Errorf("empty value: %v, %v", ends, ok)
	}
	for name, lines := range map[string][]string{
		"open array":            {`[`, `  "a",`},
		"open multi-line":       {`'''`, `text`},
		"comment in open array": {`[ "a",`, `# ]`},
	} {
		if ends, ok := feedTOMLValue(lines...); !ok || slices.Contains(ends, true) {
			t.Errorf("%s: ends %v, ok %v; want still open", name, ends, ok)
		}
	}
}
