// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package util

import (
	"strings"
	"testing"
)

// Positive: a block that opens the document ends after its closing fence, with LF or CRLF line
// ends and blanks after either fence, and an empty block is a block.
func TestFrontMatterEndPositive(t *testing.T) {
	for text, want := range map[string]int{
		"---\nname: a\n---\n# A\n":                    3,
		"---\r\nname: a\r\n---\r\nbody\r\n":           3,
		"--- \t\nname: a\nmetadata:\n  k: v\n---  \n": 5,
		"---\n---\nbody\n":                            2,
	} {
		end, opened := FrontMatterEnd(strings.Split(text, "\n"), 100)
		if end != want || !opened {
			t.Errorf("FrontMatterEnd(%q) = %d, %v; want %d, true", text, end, opened, want)
		}
	}
}

// Negative: a document whose first line is not a fence has no block, and one whose block never
// closes reports it opened without an end.
func TestFrontMatterEndNegative(t *testing.T) {
	for _, text := range []string{"# A\n---\nname: a\n---\n", "\n---\nname: a\n---\n", "----\nname: a\n----\n", "--- x\nname: a\n---\n"} {
		if end, opened := FrontMatterEnd(strings.Split(text, "\n"), 100); end != 0 || opened {
			t.Errorf("FrontMatterEnd(%q) = %d, %v; want 0, false", text, end, opened)
		}
	}
	if end, opened := FrontMatterEnd(strings.Split("---\nname: a\nbody\n", "\n"), 100); end != 0 || !opened {
		t.Errorf("an unclosed block = %d, %v; want 0, true", end, opened)
	}
}

// Boundary: no lines, a lone fence, and a closing fence at the last line the bound reads and
// one line past it.
func TestFrontMatterEndBoundary(t *testing.T) {
	if end, opened := FrontMatterEnd(nil, 100); end != 0 || opened {
		t.Errorf("no lines = %d, %v", end, opened)
	}
	if end, opened := FrontMatterEnd([]string{"---"}, 100); end != 0 || !opened {
		t.Errorf("a lone fence = %d, %v", end, opened)
	}
	lines := []string{"---", "a: 1", "---"}
	if end, opened := FrontMatterEnd(lines, 3); end != 3 || !opened {
		t.Errorf("closing fence at the bound = %d, %v; want 3, true", end, opened)
	}
	if end, opened := FrontMatterEnd(lines, 2); end != 0 || !opened {
		t.Errorf("closing fence past the bound = %d, %v; want 0, true", end, opened)
	}
}

// The cases the caveman scanner relied on when it had its own reader (#374), on the shared one.
func TestFrontMatterEnd_SharedCases(t *testing.T) {
	cases := map[string]struct {
		lines []string
		want  int
	}{
		"closed":            {[]string{"---", "name: x", "---", "# Body"}, 3},
		"trailing blanks":   {[]string{"--- ", "name: x", "---\t", "body"}, 3},
		"empty block":       {[]string{"---", "---"}, 2},
		"never closed":      {[]string{"---", "name: x", "body"}, 0},
		"not on line one":   {[]string{"", "---", "name: x", "---"}, 0},
		"thematic break":    {[]string{"----", "text", "---"}, 0},
		"empty document":    {nil, 0},
		"single delimiter":  {[]string{"---"}, 0},
		"indented opener":   {[]string{" ---", "name: x", "---"}, 0},
		"later block stays": {[]string{"---", "a: 1", "---", "---", "b: 2", "---"}, 3},
	}
	for name, tc := range cases {
		if got, _ := FrontMatterEnd(tc.lines, len(tc.lines)); got != tc.want {
			t.Errorf("%s: end %d, want %d", name, got, tc.want)
		}
	}
}
