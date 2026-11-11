package docdistill

import (
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/util"
)

// TestBudgetDoesNotCutInsideFence plants a budget boundary inside a fenced block.
func TestBudgetDoesNotCutInsideFence(t *testing.T) {
	input := "intro words\n```go\none two three four five six seven eight nine ten\n```\nafter more words\n"
	got, _ := fitTokenBudget(input, 14)
	var fence util.MarkdownFence
	for _, line := range strings.Split(got, "\n") {
		fence.Inside(strings.TrimSpace(line))
	}
	if fence.Open() {
		t.Fatalf("cut leaves an open fence: %q", got)
	}
}

// TestBudgetFenceBoundaries retains closed fences and removes partial openers.
func TestBudgetFenceBoundaries(t *testing.T) {
	cases := []struct {
		input string
		want  string
	}{
		{"before\n~~~go\nbody\n~~~\nafter\n", "~~~go"},
		{"before\n```go extra words\nbody\n", "before"},
	}
	for _, tc := range cases {
		got := strings.Join(trimUnclosedFence(strings.Split(tc.input, "\n")), "\n")
		if !strings.Contains(got, tc.want) {
			t.Errorf("got %q, want %q", got, tc.want)
		}
		var fence util.MarkdownFence
		for _, line := range strings.Split(got, "\n") {
			fence.Inside(strings.TrimSpace(line))
		}
		if fence.Open() {
			t.Errorf("unbalanced result: %q", got)
		}
	}
}
