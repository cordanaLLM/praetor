package strictjson

import (
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"
)

// base is a caller with the default wording and bounds large enough not to interfere.
var base = Options{MaxBytes: 1 << 20, MaxDepth: 32}

func TestValidate_Positive_AcceptsOneStrictDocument(t *testing.T) {
	accepted := map[string]string{
		"object":                     `{"a":1,"b":[true,false,null],"c":{"d":"e"}}`,
		"scalar root":                "9007199254740993",
		"string root":                `"text"`,
		"surrounding whitespace":     " \n\t{}\r\n ",
		"same name in sibling scope": `{"a":{"k":1},"b":{"k":1},"k":[{"k":1},{"k":2}]}`,
		"repeated values":            `{"a":["x","x"],"b":{"c":"x"}}`,
		"paired surrogate escape":    `{"emoji":"😄"}`,
		"escaped backslash before u": `{"text":"literal \\ud800"}`,
		"distinct letters":           `{"a":1,"b":2}`,
	}
	for name, raw := range accepted {
		if err := Validate([]byte(raw), base); err != nil {
			t.Errorf("%s: %s refused: %v", name, raw, err)
		}
	}
}

func TestValidate_Negative_RefusesEachKind(t *testing.T) {
	nulls := base
	nulls.RejectNull = true
	refused := []struct {
		name string
		raw  string
		opts Options
		kind error
		text string
	}{
		{"empty", "", base, ErrSize, "JSON requires 1..1048576 UTF-8 bytes"},
		{"invalid UTF-8", "\"\xff\"", base, ErrSize, "JSON requires 1..1048576 UTF-8 bytes"},
		{"whitespace only", " \n", base, ErrSyntax, "unexpected EOF"},
		{"trailing comma", `{"a":1,}`, base, ErrSyntax, ""},
		{"JSONC comment", "{\"a\":1 // note\n}", base, ErrSyntax, ""},
		{"cut short", `[1,2`, base, ErrSyntax, ""},
		{"malformed escape", `{"a":"\uZZZZ"}`, base, ErrSyntax, ""},
		{"cut inside a surrogate escape", `{"a":"\ud800`, base, ErrSyntax, ""},
		{"lone high surrogate", `{"a":"\ud800"}`, base, ErrSurrogate, ErrSurrogate.Error()},
		{"lone low surrogate", `"\udfff"`, base, ErrSurrogate, ErrSurrogate.Error()},
		{"high surrogate then letter", `"\ud800A"`, base, ErrSurrogate, ErrSurrogate.Error()},
		{"surrogate in a name", `{"\ud800":1}`, base, ErrSurrogate, ErrSurrogate.Error()},
		{"exact duplicate", `{"a":1,"a":2}`, base, ErrDuplicate, "invalid or duplicate JSON key"},
		{"escaped duplicate", `{"a":1,"a":2}`, base, ErrDuplicate, "invalid or duplicate JSON key"},
		{"case variant", `{"quality":1,"Quality":2}`, base, ErrDuplicate, "invalid or duplicate JSON key"},
		{"nested case variant", `{"inner":[{"a":1,"A":2}]}`, base, ErrDuplicate, "invalid or duplicate JSON key"},
		{"second document", `{} {}`, base, ErrTrailing, "expected exactly one JSON document"},
		{"trailing garbage", `{"a":1}x`, base, ErrTrailing, "expected exactly one JSON document"},
		{"null under RejectNull", `{"a":null}`, nulls, ErrNull, ErrNull.Error()},
		{"null root under RejectNull", `null`, nulls, ErrNull, ErrNull.Error()},
	}
	for _, tc := range refused {
		err := Validate([]byte(tc.raw), tc.opts)
		if !errors.Is(err, tc.kind) {
			t.Errorf("%s: %q returned %v, want kind %v", tc.name, tc.raw, err, tc.kind)
			continue
		}
		if tc.text != "" && !strings.Contains(err.Error(), tc.text) {
			t.Errorf("%s: %q worded %q, want %q", tc.name, tc.raw, err, tc.text)
		}
	}
}

// TestValidate_Negative_FoldMatchesEncodingJSON is the defect behind issue #310: encoding/json
// matches a member to a struct field with strings.EqualFold, so every spelling it would merge
// into one field must be one name to the scan. Lower-casing alone misses the long s, which
// folds to s, and the Kelvin sign, which folds to k.
func TestValidate_Negative_FoldMatchesEncodingJSON(t *testing.T) {
	var target struct {
		S int `json:"s"`
		K int `json:"k"`
	}
	// U+017F LATIN SMALL LETTER LONG S and U+212A KELVIN SIGN, spelled as escapes so the test
	// cannot silently lose them to an editor's normalisation.
	if err := json.Unmarshal([]byte("{\"\u017f\":1,\"\u212a\":2}"), &target); err != nil || target.S != 1 || target.K != 2 {
		t.Fatalf("encoding/json no longer folds these spellings (%+v, %v); revisit foldName", target, err)
	}
	for _, raw := range []string{"{\"s\":1,\"\u017f\":2}", "{\"k\":1,\"\u212a\":2}", `{"é":1,"É":2}`, `{"QUALITY":1,"quality":2}`} {
		if err := Validate([]byte(raw), base); !errors.Is(err, ErrDuplicate) {
			t.Errorf("%s returned %v, want a duplicate refusal", raw, err)
		}
	}
}

func TestValidate_Positive_ExactNamesKeepsCaseVariants(t *testing.T) {
	exact := base
	exact.Names = ExactNames
	if err := Validate([]byte(`{"**/Build/**":true,"**/build/**":true,"*.S":"asm","*.s":"asm"}`), exact); err != nil {
		t.Fatalf("case-variant names refused under ExactNames: %v", err)
	}
	for _, raw := range []string{`{"a":1,"a":2}`, `{"a":1,"a":2}`, `{"x":{"k":1,"k":2}}`} {
		if err := Validate([]byte(raw), exact); !errors.Is(err, ErrDuplicate) {
			t.Errorf("ExactNames accepted the identical names of %s: %v", raw, err)
		}
	}
}

func TestValidate_Boundary_Bounds(t *testing.T) {
	sized := Options{MaxBytes: 7, MaxDepth: 32}
	if err := Validate([]byte(`{"a":1}`), sized); err != nil {
		t.Errorf("input of exactly MaxBytes refused: %v", err)
	}
	if err := Validate([]byte(`{"a":1} `), sized); !errors.Is(err, ErrSize) {
		t.Errorf("input one byte over MaxBytes returned %v", err)
	}
	if err := Validate([]byte(`1`), Options{MaxDepth: 1}); !errors.Is(err, ErrSize) {
		t.Errorf("MaxBytes 0 accepted input: %v", err)
	}
	deep := Options{MaxBytes: 1 << 10, MaxDepth: 4}
	if err := Validate([]byte(`[[{"a":[1]}]]`), deep); err != nil {
		t.Errorf("nesting of exactly MaxDepth refused: %v", err)
	}
	if err := Validate([]byte(`[[{"a":[[1]]}]]`), deep); !errors.Is(err, ErrDepth) || err.Error() != "JSON nesting exceeds 4" {
		t.Errorf("nesting one over MaxDepth returned %v", err)
	}
	if err := Validate([]byte(`1`), Options{MaxBytes: 1}); err != nil {
		t.Errorf("MaxDepth 0 refused a scalar: %v", err)
	}
	if err := Validate([]byte(`[]`), Options{MaxBytes: 2}); !errors.Is(err, ErrDepth) {
		t.Errorf("MaxDepth 0 accepted an array: %v", err)
	}
}

func TestValidate_Boundary_TokenBound(t *testing.T) {
	counted := Options{MaxBytes: 1 << 10, MaxDepth: 4, MaxTokens: 5}
	if err := Validate([]byte(`[1,2,3]`), counted); err != nil {
		t.Errorf("document of exactly MaxTokens tokens refused: %v", err)
	}
	if err := Validate([]byte(`[1,2,3,4]`), counted); !errors.Is(err, ErrTokens) {
		t.Errorf("document one token over MaxTokens returned %v", err)
	}
	// Below 1 the input length bounds the tokens; every token takes at least one byte.
	if err := Validate([]byte(`[]`), Options{MaxBytes: 2, MaxDepth: 1}); err != nil {
		t.Errorf("document with one token per byte refused: %v", err)
	}
}

func TestValidate_Positive_CallerWording(t *testing.T) {
	opts := Options{MaxBytes: 4, MaxDepth: 1, Messages: Messages{
		Size:      "caller JSON requires 1..%d bytes",
		Syntax:    "decode caller JSON: %w",
		Depth:     "caller JSON nesting exceeds %d",
		Duplicate: "caller JSON repeats %q",
		Trailing:  "caller JSON holds two documents",
	}}
	cases := map[string]string{
		`12345`:      "caller JSON requires 1..4 bytes",
		`[[]]`:       "caller JSON nesting exceeds 1",
		`1 2`:        "caller JSON holds two documents",
		`{"A":1,"a"`: "caller JSON requires 1..4 bytes",
	}
	for raw, want := range cases {
		if err := Validate([]byte(raw), opts); err == nil || err.Error() != want {
			t.Errorf("%s worded %v, want %q", raw, err, want)
		}
	}
	opts.MaxBytes = 64
	if err := Validate([]byte(`{"A":1,"a":2}`), opts); err == nil || err.Error() != `caller JSON repeats "a"` {
		t.Errorf("duplicate worded %v", err)
	}
	err := Validate([]byte(`[`), opts)
	if !errors.Is(err, ErrSyntax) || !errors.Is(err, io.ErrUnexpectedEOF) || !strings.HasPrefix(err.Error(), "decode caller JSON: ") {
		t.Errorf("syntax refusal %v does not wrap the scanner error under the caller's wording", err)
	}
}

type record struct {
	Name  string `json:"name"`
	Count int    `json:"count"`
}

func TestDecode_Positive_DecodesIntoTarget(t *testing.T) {
	var got record
	if err := Decode([]byte(`{"name":"a","count":2}`), &got, base); err != nil || got != (record{Name: "a", Count: 2}) {
		t.Fatalf("Decode = %+v, %v", got, err)
	}
	numbers := base
	numbers.UseNumber = true
	var value any
	if err := Decode([]byte(`{"big":9007199254740993}`), &value, numbers); err != nil {
		t.Fatal(err)
	}
	object, ok := value.(map[string]any)
	if !ok || object["big"] != json.Number("9007199254740993") {
		t.Fatalf("UseNumber lost the literal: %#v", value)
	}
}

func TestDecode_Negative_RefusesBeforeAndDuringDecode(t *testing.T) {
	var got record
	if err := Decode([]byte(`{"name":"a","Name":"b"}`), &got, base); !errors.Is(err, ErrDuplicate) || got.Name != "" {
		t.Errorf("case-variant member decoded: %+v, %v", got, err)
	}
	opts := base
	opts.Messages.Decode = "decode record: %w"
	err := Decode([]byte(`{"name":"a","extra":1}`), &got, opts)
	if !errors.Is(err, ErrDecode) || !strings.HasPrefix(err.Error(), "decode record: ") {
		t.Errorf("unknown field returned %v", err)
	}
	if err := Decode([]byte(`{"count":"two"}`), &got, base); !errors.Is(err, ErrDecode) || !strings.HasPrefix(err.Error(), "decode JSON: ") {
		t.Errorf("wrong field type returned %v", err)
	}
}

func TestDecode_Boundary_SingleFieldAtEveryBound(t *testing.T) {
	raw := []byte(`{"count":1}`)
	exact := Options{MaxBytes: len(raw), MaxDepth: 1, MaxTokens: 4}
	var got record
	if err := Decode(raw, &got, exact); err != nil || got.Count != 1 {
		t.Fatalf("document at every bound refused: %+v, %v", got, err)
	}
	exact.MaxTokens = 3
	if err := Decode(raw, &got, exact); !errors.Is(err, ErrTokens) {
		t.Errorf("document one token over the bound returned %v", err)
	}
}

func TestFoldName_MatchesStringsEqualFold(t *testing.T) {
	cases := map[string]string{
		"quality": "QUALITY",
		"Quality": "QUALITY",
		"":        "",
		"snake_1": "SNAKE_1",
		"\u212a":  "K",
		"\u017f":  "S",
		"é":       "É",
		"sigma-σ": "SIGMA-Σ",
	}
	for in, want := range cases {
		if got := foldName(in); got != want {
			t.Errorf("foldName(%q) = %q, want %q", in, got, want)
		}
		if !strings.EqualFold(in, want) {
			t.Errorf("strings.EqualFold(%q, %q) is false; the fold no longer matches it", in, want)
		}
	}
	if foldName("a") == foldName("b") || foldName("snake_case") == foldName("snakecase") {
		t.Error("distinct names folded together")
	}
}
