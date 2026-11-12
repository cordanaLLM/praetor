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
	if len(got) >= len(input) || !strings.Contains(got, truncationNotice) || !strings.Contains(got, "intro words") {
		t.Fatalf("truncation did not retain introduction and notice: %q", got)
	}
	var fence util.MarkdownFence
	lines := strings.Split(got, "\n")
	for i := 0; i < len(lines) && i < maxBudgetLines; i++ {
		fence.Inside(strings.TrimSpace(lines[i]))
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
	for i := 0; i < len(cases) && i < maxBudgetLines; i++ {
		tc := cases[i]
		got := strings.Join(trimUnclosedFence(strings.Split(tc.input, "\n")), "\n")
		if !strings.Contains(got, tc.want) {
			t.Errorf("got %q, want %q", got, tc.want)
		}
		var fence util.MarkdownFence
		lines := strings.Split(got, "\n")
		for j := 0; j < len(lines) && j < maxBudgetLines; j++ {
			fence.Inside(strings.TrimSpace(lines[j]))
		}
		if fence.Open() {
			t.Errorf("unbalanced result: %q", got)
		}
	}
}

// TestBudgetWholeLinesAndQuotedFence checks line and quoted-fence truncation.
func TestBudgetWholeLinesAndQuotedFence(t *testing.T) {
	got, _ := fitTokenBudget("keep\nlong line with many words here\n", 8)
	if strings.Contains(got, "long") {
		t.Fatalf("partial line: %q", got)
	}
	got = strings.Join(trimUnclosedFence([]string{"keep", "> > ````go", "> body", "> ```"}), "\n")
	if got != "keep\n> > ````go\n> body" {
		t.Fatalf("quoted fence container handling: %q", got)
	}
}

// TestBudgetFenceInspectionLimitDropsExcerpt checks both inspection limits.
func TestBudgetFenceInspectionLimitDropsExcerpt(t *testing.T) {
	lines := make([]string, 65537)
	lines[0] = "keep"
	if got := trimUnclosedFence(lines); len(got) != 0 {
		t.Fatal("over-limit excerpt retained")
	}
	got, _ := fitTokenBudget(strings.Repeat("word\n", 65537), 10)
	if got != truncationNotice+"\n" {
		t.Fatalf("over-limit budget excerpt: %q", got)
	}
	if got := trimUnclosedFence([]string{strings.Repeat(">", 65) + "```"}); len(got) != 0 {
		t.Fatal("over-depth quote retained")
	}
}

// TestCompressQuotedFenceContainerEnd keeps the API after a quoted fence ends with its container.
func TestCompressQuotedFenceContainerEnd(t *testing.T) {
	raw := "``` warning about package usage\nfunc Exported() error\nWarning: " + strings.Repeat("word ", 500) + "\n"
	doc := CompressDocumentation(PackageRef{Name: "sample", Kind: KindGoModule}, raw, DistillOptions{MaxTokensPerPackage: 80})
	if !strings.Contains(doc.RawMarkdown, "warning about package usage") || !strings.Contains(doc.RawMarkdown, "func Exported() error") || !strings.Contains(doc.RawMarkdown, truncationNotice) {
		t.Fatalf("summary or API lost: %s", doc.RawMarkdown)
	}
}
