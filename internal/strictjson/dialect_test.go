package strictjson

import (
	"bytes"
	"encoding/json"
	"errors"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// jsonc is base reading the JSONC dialect.
var jsonc = Options{MaxBytes: 1 << 20, MaxDepth: 32, Dialect: JSONC}

// Positive: the three extensions VS Code documents for its configuration files, each where it
// may stand, and the strict document under them decodes to the same value.
func TestValidate_Positive_JSONCAcceptsCommentsAndTrailingCommas(t *testing.T) {
	const want = `{"a":1,"b":[true,null],"c":{"d":"e"}}`
	accepted := map[string]string{
		"line comment":                  "{\"a\":1, // note\n\"b\":[true,null],\"c\":{\"d\":\"e\"}}",
		"line comment, CRLF":            "{\"a\":1, // note\r\n\"b\":[true,null],\"c\":{\"d\":\"e\"}}",
		"line comment before document":  "// header\n" + want,
		"line comment at end of input":  want + " // footer",
		"block comment":                 `{"a":/* one */1,"b":[true,null],"c":{"d":"e"}}`,
		"block comment over lines":      "{/*\n * note\n */\"a\":1,\"b\":[true,null],\"c\":{\"d\":\"e\"}}",
		"empty block comment":           `{/**/"a":1,"b":[true,null],"c":{"d":"e"}}`,
		"block comment holding markers": `{"a":1,/* // " , } */"b":[true,null],"c":{"d":"e"}}`,
		"line comment holding markers":  "{\"a\":1,// /* \" , }\n\"b\":[true,null],\"c\":{\"d\":\"e\"}}",
		"comment in another script":     "{\"a\":1,// größe 尺寸\n\"b\":[true,null],\"c\":{\"d\":\"e\"}}",
		"trailing comma in object":      `{"a":1,"b":[true,null],"c":{"d":"e"},}`,
		"trailing comma in array":       `{"a":1,"b":[true,null,],"c":{"d":"e"}}`,
		"trailing commas nested":        `{"a":1,"b":[true,null,],"c":{"d":"e",},}`,
		"trailing comma, then comment":  "{\"a\":1,\"b\":[true,null, /* last */ ],\"c\":{\"d\":\"e\"}, // end\n}",
		"strict document":               want,
	}
	var wantValue any
	if err := json.Unmarshal([]byte(want), &wantValue); err != nil {
		t.Fatal(err)
	}
	for name, raw := range accepted {
		var got any
		if err := Decode([]byte(raw), &got, jsonc); err != nil {
			t.Errorf("%s: %s refused: %v", name, raw, err)
			continue
		}
		if !reflect.DeepEqual(got, wantValue) {
			t.Errorf("%s: %s decoded to %#v, want %#v", name, raw, got, wantValue)
		}
	}
}

// Positive: comment markers and a comma before a bracket inside a string are data, whatever
// escapes surround them.
func TestDecode_Positive_JSONCKeepsStringContent(t *testing.T) {
	raw := `{
		// the keys below hold comment markers
		"url": "https://example.com/a//b", /* and so does this */
		"glob": "**/*.go",
		"block": "/* kept */",
		"escaped": "quote \" // still inside",
		"backslash": "ends in \\", // a comment after an escaped backslash
		"list": ",]",
	}`
	var got map[string]string
	if err := Decode([]byte(raw), &got, jsonc); err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"url":       "https://example.com/a//b",
		"glob":      "**/*.go",
		"block":     "/* kept */",
		"escaped":   `quote " // still inside`,
		"backslash": `ends in \`,
		"list":      ",]",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("string content changed:\n got %#v\nwant %#v", got, want)
	}
}

// Negative: what JSONC does not add stays refused, each as the kind the strict form gives it.
func TestValidate_Negative_JSONCRefusesWhatItDoesNotAdd(t *testing.T) {
	nulls := jsonc
	nulls.RejectNull = true
	refused := []struct {
		name string
		raw  string
		opts Options
		kind error
	}{
		{"unterminated block comment", `{"a":1 /* note }`, jsonc, ErrSyntax},
		{"block comment closed by its opener", `{"a":1}/*/`, jsonc, ErrTrailing},
		{"nested block comment", `{/* a /* b */ c */"a":1}`, jsonc, ErrSyntax},
		{"lone slash", `{"a":1 / }`, jsonc, ErrSyntax},
		{"hash comment", "{\"a\":1 # note\n}", jsonc, ErrSyntax},
		{"comment only", "// nothing else\n", jsonc, ErrSyntax},
		{"comma in an empty array", `[,]`, jsonc, ErrSyntax},
		{"comma in an empty object", `{,}`, jsonc, ErrSyntax},
		{"two trailing commas", `[1,,]`, jsonc, ErrSyntax},
		{"comma after a colon", `{"a":,}`, jsonc, ErrSyntax},
		{"comma after the document", `{"a":1},`, jsonc, ErrTrailing},
		{"comment between two documents", "{} // one\n{}", jsonc, ErrTrailing},
		{"comment splitting a literal", `[tr/**/ue]`, jsonc, ErrSyntax},
		{"single-quoted string", `{'a':1}`, jsonc, ErrSyntax},
		{"duplicate key", "{\"a\":1, // first\n\"a\":2}", jsonc, ErrDuplicate},
		{"duplicate key, trailing comma", `{"a":1,"a":2,}`, jsonc, ErrDuplicate},
		{"case-variant key", `{"quality":1,/* again */"Quality":2}`, jsonc, ErrDuplicate},
		{"unpaired surrogate after a comment", `/* note */{"a":"\ud800"}`, jsonc, ErrSurrogate},
		{"null under RejectNull", "{\"a\":null, // unset\n}", nulls, ErrNull},
		{"empty", "", jsonc, ErrSize},
		{"invalid UTF-8 in a comment", "{} // \xff", jsonc, ErrSize},
	}
	for _, tc := range refused {
		if err := Validate([]byte(tc.raw), tc.opts); !errors.Is(err, tc.kind) {
			t.Errorf("%s: %q returned %v, want kind %v", tc.name, tc.raw, err, tc.kind)
		}
	}
}

// Negative: the dialect is the caller's choice. The documents JSONC accepts are refused as
// syntax by every caller that does not name it, so no other input becomes tolerant.
func TestValidate_Negative_StrictDialectRefusesJSONC(t *testing.T) {
	for name, raw := range map[string]string{
		"line comment":   "{\"a\":1 // note\n}",
		"block comment":  `{/* note */"a":1}`,
		"trailing comma": `{"a":[1,],}`,
	} {
		if err := Validate([]byte(raw), jsonc); err != nil {
			t.Errorf("%s: JSONC refused %q: %v", name, raw, err)
		}
		for label, opts := range map[string]Options{"zero value": base, "named": {MaxBytes: 1 << 20, MaxDepth: 32, Dialect: StrictJSON}} {
			if err := Validate([]byte(raw), opts); !errors.Is(err, ErrSyntax) {
				t.Errorf("%s: strict dialect (%s) returned %v for %q, want a syntax refusal", name, label, err, raw)
			}
		}
	}
}

// Boundary: a comment is no token and no nesting, and its bytes count toward MaxBytes, so each
// bound sits where it does for the strict form.
func TestValidate_Boundary_JSONCBoundsAreTheStrictBounds(t *testing.T) {
	commented := []byte("[1,/* two */2,3,] // tail")
	bounds := Options{MaxBytes: len(commented), MaxDepth: 1, MaxTokens: 5, Dialect: JSONC}
	if err := Validate(commented, bounds); err != nil {
		t.Fatalf("document at every bound refused: %v", err)
	}
	over := bounds
	over.MaxTokens = 4
	if err := Validate(commented, over); !errors.Is(err, ErrTokens) {
		t.Errorf("one token over MaxTokens returned %v", err)
	}
	over = bounds
	over.MaxBytes = len(commented) - 1
	if err := Validate(commented, over); !errors.Is(err, ErrSize) {
		t.Errorf("one comment byte over MaxBytes returned %v", err)
	}
	over = bounds
	over.MaxDepth = 0
	if err := Validate(commented, over); !errors.Is(err, ErrDepth) {
		t.Errorf("an array under MaxDepth 0 returned %v", err)
	}
	deep := "[/*1*/[/*2*/[//3\n]]]"
	if err := Validate([]byte(deep), Options{MaxBytes: 64, MaxDepth: 3, Dialect: JSONC}); err != nil {
		t.Errorf("nesting of exactly MaxDepth refused: %v", err)
	}
	if err := Validate([]byte(deep), Options{MaxBytes: 64, MaxDepth: 2, Dialect: JSONC}); !errors.Is(err, ErrDepth) {
		t.Errorf("nesting one over MaxDepth returned %v", err)
	}
}

// Boundary: blanking keeps every byte offset and returns a document without extensions as the
// same bytes, so a syntax refusal points into the file the operator edits.
func TestBlankJSONC_Boundary_OffsetsAndUntouchedInput(t *testing.T) {
	for _, raw := range []string{`{"a":1,"b":[true,false,null]}`, `"// not a comment"`, ` [ ] `, `{"a":"\"/*"}`} {
		in := []byte(raw)
		if out := blankJSONC(in); &out[0] != &in[0] {
			t.Errorf("%s holds no extension and was copied", raw)
		}
	}
	cases := map[string]string{
		"{/*x*/\"a\":1,}":    "{     \"a\":1 }",
		"[1,2,//c\n]":        "[1,2    \n]",
		"[1 /*a*/ , /*b*/ ]": "[1" + strings.Repeat(" ", 15) + "]",
		"{\"a\":\"//\",}//é": "{\"a\":\"//\" }    ",
		"[1,] /* open":       "[1 ] /* open",
		"/*/ [1,]":           "/*/ [1,]",
	}
	for raw, want := range cases {
		got := blankJSONC([]byte(raw))
		if string(got) != want {
			t.Errorf("blankJSONC(%q) = %q, want %q", raw, got, want)
		}
		if len(got) != len(raw) {
			t.Errorf("blankJSONC(%q) changed the length from %d to %d", raw, len(raw), len(got))
		}
	}
	original := []byte("[1,] // note")
	kept := bytes.Clone(original)
	blankJSONC(original)
	if !bytes.Equal(original, kept) {
		t.Errorf("blankJSONC changed its input to %q", original)
	}
	commented := Validate([]byte("/* note */ {\"a\":tru}"), jsonc)
	padded := Validate([]byte(strings.Repeat(" ", 11)+"{\"a\":tru}"), base)
	if !errors.Is(commented, ErrSyntax) || padded == nil || commented.Error() != padded.Error() {
		t.Errorf("syntax refusal %v differs from %v, the refusal of the same document without its comment", commented, padded)
	}
}

// Boundary: many openers and no closer are read once, not once per opener.
func TestBlankJSONC_Boundary_UnterminatedCommentStopsThePass(t *testing.T) {
	raw := []byte("[1,]" + strings.Repeat("/* ", 1<<18))
	if got := blankJSONC(raw); string(got[:4]) != "[1 ]" || !bytes.Equal(got[4:], raw[4:]) {
		t.Errorf("the pass rewrote bytes after an unterminated comment: %q", got[:16])
	}
	if err := Validate(raw, Options{MaxBytes: len(raw), MaxDepth: 1, Dialect: JSONC}); !errors.Is(err, ErrTrailing) {
		t.Errorf("unterminated comment after a document returned %v", err)
	}
}

func TestDialectOf(t *testing.T) {
	cases := map[string]Dialect{
		".vscode/settings.json":           JSONC,
		".vscode/extensions.json":         JSONC,
		".vscode/launch.json":             JSONC,
		".vscode/tasks.json":              JSONC,
		"services/api/.vscode/tasks.json": JSONC,
		".vscode/mcp.json":                StrictJSON,
		".vscode/other.json":              StrictJSON,
		".vscode/settings.json.bak":       StrictJSON,
		".vscode/nested/settings.json":    StrictJSON,
		"vscode/settings.json":            StrictJSON,
		".zed/settings.json":              StrictJSON,
		".fleet/settings.json":            StrictJSON,
		"settings.json":                   StrictJSON,
		".github/rulesets/main.json":      StrictJSON,
		"project.sublime-project":         StrictJSON,
		"":                                StrictJSON,
	}
	for file, want := range cases {
		if got := DialectOf(file); got != want {
			t.Errorf("DialectOf(%q) = %v, want %v", file, got, want)
		}
	}
	// A backslash separates path elements on Windows only; elsewhere it is part of a file name.
	hostForm := StrictJSON
	if filepath.Separator == '\\' {
		hostForm = JSONC
	}
	if got := DialectOf(`.vscode\settings.json`); got != hostForm {
		t.Errorf("DialectOf of the backslash form = %v, want %v on this host", got, hostForm)
	}
	if JSONC.String() != "JSON with Comments" || StrictJSON.String() != "strict JSON" {
		t.Errorf("dialect names changed: %q, %q", JSONC, StrictJSON)
	}
}
