// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package hiss

import (
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/hiss/pyliteral"
)

func TestFixtureDatePythonDecoder(t *testing.T) {
	for _, tc := range []struct {
		name, source, want string
		bad                bool
	}{
		{"plain", `'value'`, "value", false},
		{"escapes", `"\x32\u0030\060\060"`, "2000", false},
		{"raw", `r"\x32"`, `\x32`, false},
		{"formatted", `f"part:{value}"`, "part:{value}", false},
		{"empty", `""`, "", false},
		{"multiline", "'''first\nsecond'''", "first\nsecond", false},
		{"invalid escape", `"\xZ0"`, "", true},
		{"unterminated", `"value`, "", true},
		{"not a literal", "value", "", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			value, end, err := pyliteral.Parse(tc.source, 0)
			if (err != nil) != tc.bad || !tc.bad && (value != tc.want || end != len(tc.source)) {
				t.Fatalf("decoded %q, end %d, error %v", value, end, err)
			}
			if !tc.bad {
				if end, err := pyliteral.Skip(tc.source, 0); err != nil || end != len(tc.source) || !pyliteral.At(tc.source, 0) {
					t.Fatalf("literal boundary: end %d, error %v", end, err)
				}
			}
		})
	}
}

func TestFixtureDatePythonDecoderBounds(t *testing.T) {
	for _, index := range []int{-1, 0, 1} {
		if _, _, err := pyliteral.Parse("", index); err == nil || pyliteral.At("", index) {
			t.Errorf("empty source accepted index %d", index)
		}
		if _, err := pyliteral.Skip("", index); err == nil {
			t.Errorf("skip accepted index %d", index)
		}
	}
	var out strings.Builder
	if _, err := pyliteral.AppendEscape(&out, ""); err == nil {
		t.Fatal("empty escape accepted")
	}
	if count, err := pyliteral.AppendEscape(&out, "n"); err != nil || count != 1 || out.String() != "\n" {
		t.Fatalf("escape: %d, %v, %q", count, err, out.String())
	}
}
