package caveman

import (
	"strings"
	"testing"
)

func TestEstimateTokensPositive(t *testing.T) {
	cases := map[string]struct {
		text string
		want int
	}{
		"ten words":                 {"one two three four five six seven eight nine ten", 13},
		"newlines and tabs split":   {"alpha\nbeta\tgamma\r\ndelta", 5},
		"code counts as words":      {"go test -race -count=1 ./internal/caveman", 6},
		"hundred words, 1.3 each":   {strings.Repeat("word ", 100), 130},
		"punctuation stays in word": {"verdict: pass.", 2},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			if got := EstimateTokens(tc.text); got != tc.want {
				t.Fatalf("EstimateTokens(%q) = %d, want %d", tc.text, got, tc.want)
			}
		})
	}
}

func TestEstimateTokensNegative(t *testing.T) {
	for _, text := range []string{"", " ", "\n\n\t\r\n"} {
		if got := EstimateTokens(text); got != 0 {
			t.Errorf("EstimateTokens(%q) = %d, want 0 for text without words", text, got)
		}
	}
}

func TestEstimateTokensBoundary(t *testing.T) {
	// One word is 1.3 tokens and truncates to 1; the estimate never rounds up.
	if got := EstimateTokens("solo"); got != 1 {
		t.Errorf("one word = %d, want 1", got)
	}
	// Three words are 3.9 tokens: still truncated, so a budget check never overshoots.
	if got := EstimateTokens("a b c"); got != 3 {
		t.Errorf("three words = %d, want 3", got)
	}
	// The inverse a word-budget caller computes must stay inside the token budget.
	budget := 400
	words := int(float64(budget) / TokensPerWord)
	if got := EstimateTokens(strings.Repeat("w ", words)); got > budget {
		t.Errorf("%d words estimate %d tokens, above the %d budget they were derived from", words, got, budget)
	}
}
