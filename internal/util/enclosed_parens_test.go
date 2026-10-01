package util

import "testing"

// Positive: the text inside the pair opening at open, nested pairs included. Negative: an open
// outside the text or not at "(". Boundary: an empty pair, a pair that never closes, and an open
// at the last byte.
func TestEnclosedParens(t *testing.T) {
	cases := []struct {
		name, text string
		open       int
		want       string
	}{
		{"positive whole group", "(a || b)", 0, "a || b"},
		{"positive nested", "(a (b) c) d", 0, "a (b) c"},
		{"positive inner open", "fn f(x: (u8, u8)) -> u8", 4, "x: (u8, u8)"},
		{"positive first of two groups", "(a) || (b)", 0, "a"},
		{"negative not at a parenthesis", "a (b)", 0, ""},
		{"negative open past the end", "(a)", 3, ""},
		{"negative open before the start", "(a)", -1, ""},
		{"negative empty text", "", 0, ""},
		{"boundary empty pair", "()", 0, ""},
		{"boundary never closes", "(a (b)", 0, "a (b)"},
		{"boundary open at the last byte", "a (", 2, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := EnclosedParens(tc.text, tc.open); got != tc.want {
				t.Fatalf("EnclosedParens(%q, %d) = %q, want %q", tc.text, tc.open, got, tc.want)
			}
		})
	}
}
