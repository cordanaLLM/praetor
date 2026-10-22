// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package hiss

import (
	"strings"
	"testing"
)

func stepAll(text string) QuoteStepper {
	var s QuoteStepper
	for i := 0; i < len(text); i++ {
		i = s.Step(text, i)
	}
	return s
}

func TestQuoteStepper_Positive(t *testing.T) {
	cases := []struct {
		name, input string
	}{
		{"plain double quote", `"hello world"`},
		{"plain single quote", `'hello world'`},
		{"nested command substitution", `"$(echo "x y")"`},
		{"printf in command substitution", `"$(printf '%s' "x")"`},
		{"20w token line", `"$(printf '%s' "$token_response" | sed -n 's/.*"token":"\([^"]*\)".*/\1/p')"`},
		{"backquote inside double quote", `"` + "`" + `echo "x y"` + "`" + `"`},
		{"arithmetic expansion", `"$(( 1 + (2 * 3) ))"`},
		{"escaped double quote", `"hello \"world\""`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := stepAll(tc.input)
			if s.Open() {
				t.Fatalf("expected fully closed, got open with quote %q", s.OpenQuote())
			}
			if s.ExceededLimit() {
				t.Fatal("unexpected limit exceeded")
			}
		})
	}
}

func TestQuoteStepper_Negative(t *testing.T) {
	cases := []struct {
		name, input string
		wantQuote   byte
	}{
		{"unclosed single quote", `'hello`, '\''},
		{"unclosed double quote", `"hello`, '"'},
		{"unclosed substitution quote", `"$(echo "x)"`, '"'},
		{"unclosed command substitution", `"$(echo x"`, '"'},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := stepAll(tc.input)
			if !s.Open() {
				t.Fatal("expected unclosed state")
			}
			if s.OpenQuote() != tc.wantQuote {
				t.Fatalf("OpenQuote = %q; want %q", s.OpenQuote(), tc.wantQuote)
			}
		})
	}
}

func TestQuoteStepper_Boundary(t *testing.T) {
	// Boundary 1: Empty text leaves stepper closed.
	var empty QuoteStepper
	if empty.Open() || empty.InQuote() || empty.OpenQuote() != 0 || empty.ExceededLimit() {
		t.Fatal("empty stepper must be closed and within limits")
	}

	// Boundary 2: At maxShellNesting (32 frames) must not exceed limit.
	// Each "$(" opens 2 frames: shellDouble and shellCommand.
	var atBound strings.Builder
	for i := 0; i < maxShellNesting/2; i++ {
		atBound.WriteString(`"$(`)
	}
	for i := 0; i < maxShellNesting/2; i++ {
		atBound.WriteString(`)"`)
	}
	s := stepAll(atBound.String())
	if s.ExceededLimit() {
		t.Fatal("stepper at max nesting limit must not exceed limit")
	}
	if s.Open() {
		t.Fatal("balanced nesting at bound must be closed")
	}

	// Boundary 3: Beyond maxShellNesting (33+ frames) sets limit.
	var pastBound strings.Builder
	for i := 0; i < (maxShellNesting/2)+1; i++ {
		pastBound.WriteString(`"$(`)
	}
	sPast := stepAll(pastBound.String())
	if !sPast.ExceededLimit() {
		t.Fatal("stepper past max nesting limit must report limit exceeded")
	}
}
