// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package pyliteral_test

import (
	"slices"
	"testing"

	"github.com/cordanaLLM/praetor/internal/hiss/pyliteral"
)

// TestParseTolerant pins parse tolerant.
func TestParseTolerant(t *testing.T) {
	for _, tc := range []struct {
		name, source, want string
		start              int
		bad                bool
	}{
		{"named digit", `"\N{DIGIT TWO}"`, "2", 0, false},
		{"unknown named rune", `"\N{DEGREE SIGN}"`, "\ufffd", 0, false},
		{"surrogate", `"\uD800"`, "\ufffd", 0, false},
		{"long surrogate", `"\U0000DFFF"`, "\ufffd", 0, false},
		{"high octal", `"\400"`, "\ufffd", 0, false},
		{"highest octal", `"\777"`, "\ufffd", 0, false},
		{"octal boundary", `"\377"`, "\xff", 0, false},
		{"surrogate lower boundary", `"\uD7FF"`, "\ud7ff", 0, false},
		{"surrogate upper boundary", `"\uE000"`, "\ue000", 0, false},
		{"formatted named escape", `f"\N{DIGIT TWO} {{literal}} {value}"`, "2 {literal} {value}", 0, false},
		{"formatted surrogate", `f"\ud800 {value}"`, "\ufffd {value}", 0, false},
		{"raw", `r"\N{DIGIT TWO}"`, `\N{DIGIT TWO}`, 0, false},
		{"raw formatted", `rf"\uD800 {value}"`, `\uD800 {value}`, 0, false},
		{"triple quote", `'''\N{DIGIT TWO}'''`, "2", 0, false},
		{"nonzero offset", `x = "\N{DIGIT TWO}"`, "2", 4, false},
		{"unterminated", `"\N{DIGIT TWO}`, "", 0, true},
		{"invalid hex", `"\xZZ"`, "", 0, true},
		{"invalid named escape", `"\N{BROKEN"`, "", 0, true},
		{"invalid Unicode", `"\U00110000"`, "", 0, true},
		{"unmatched formatted brace", `f"\N{DIGIT TWO} }"`, "", 0, true},
		{"unsupported prefix", `b"\400"`, "", 0, true},
		{"negative offset", `"value"`, "", -1, true},
		{"empty source", "", "", 0, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			value, end, err := pyliteral.ParseTolerant(tc.source, tc.start)
			if (err != nil) != tc.bad || !tc.bad && (value != tc.want || end != len(tc.source)) {
				t.Fatalf("decoded %q, end %d, error %v; want %q, error %t", value, end, err, tc.want, tc.bad)
			}
		})
	}
}

// TestExpressions pins f-string replacement-expression span extraction.
func TestExpressions(t *testing.T) {
	for _, tc := range []struct {
		name, source string
		start        int
		want         []string
	}{
		{"positive/single-expression", `f"{parse('x')}"`, 0, []string{`parse('x')`}},
		{"positive/multiple-and-nested-brace", `f"{a} {parse('}')}"`, 0, []string{`a`, `parse('}')`}},
		{"negative/plain-string", `"plain {x}"`, 0, nil},
		{"negative/invalid-fstring", `f"{parse('unterminated)"`, 0, nil},
		{"boundary/named-and-escaped-braces", `f"\N{DIGIT TWO} {{literal}} {value}"`, 0, []string{`value`}},
		{"boundary/nonzero-offset", `x = rf"{value}"`, 4, []string{`value`}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var got []string
			for _, span := range pyliteral.Expressions(tc.source, tc.start) {
				got = append(got, tc.source[span[0]:span[1]])
			}
			if !slices.Equal(got, tc.want) {
				t.Fatalf("Expressions(%q, %d) = %q, want %q", tc.source, tc.start, got, tc.want)
			}
		})
	}
}
