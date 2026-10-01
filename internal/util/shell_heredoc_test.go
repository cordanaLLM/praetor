// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package util_test

import (
	"testing"

	"github.com/cordanaLLM/praetor/internal/util"
)

// Positive: the delimiter word ends at a blank or a metacharacter, a word quoted whole or
// escaped is quoted, and the width covers exactly the word so a reader can go on lexing the
// rest of the line.
func TestShellHereDocDelimiter_Positive(t *testing.T) {
	for _, tc := range []struct {
		text, want string
		quoted     bool
		width      int
	}{
		{"EOF", "EOF", false, 3},
		{"EOF >out", "EOF", false, 3},
		{"EOF;", "EOF", false, 3},
		{"END_OF_TEXT|sh", "END_OF_TEXT", false, 11},
		{"'EOF'", "EOF", true, 5},
		{`"EOF" | tee`, "EOF", true, 5},
		{`\EOF`, "EOF", true, 4},
	} {
		got, quoted, width, err := util.ShellHereDocDelimiter(tc.text)
		if err != nil || got != tc.want || quoted != tc.quoted || width != tc.width {
			t.Errorf("%q: got (%q, %t, %d, %v), want (%q, %t, %d)", tc.text, got, quoted, width, err, tc.want, tc.quoted, tc.width)
		}
	}
}

// Negative: a missing, partly quoted, unterminated or expanding word is refused rather than
// guessed, since a wrong delimiter reads the rest of the file as the body.
func TestShellHereDocDelimiter_Negative(t *testing.T) {
	for _, text := range []string{"", " EOF", ";", `E"O"F`, `'EOF`, `'EOF'x`, "$END", "EO`x`", `EO\F`, `''`} {
		if got, _, _, err := util.ShellHereDocDelimiter(text); err == nil {
			t.Errorf("%q: want a refusal, got %q", text, got)
		}
	}
}

// Boundary: a one-byte word and a word that runs to the end of the text.
func TestShellHereDocDelimiter_Boundary(t *testing.T) {
	if got, _, width, err := util.ShellHereDocDelimiter("X"); err != nil || got != "X" || width != 1 {
		t.Errorf("one-byte word: got (%q, %d, %v)", got, width, err)
	}
	if got, quoted, width, err := util.ShellHereDocDelimiter(`\X)`); err != nil || got != "X" || !quoted || width != 2 {
		t.Errorf("escaped one-byte word: got (%q, %t, %d, %v)", got, quoted, width, err)
	}
}
