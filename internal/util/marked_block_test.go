package util

import (
	"errors"
	"strings"
	"testing"
)

const (
	testBlockStart = "<!-- test:start -->"
	testBlockEnd   = "<!-- test:end -->"
)

func testBlock(inner string) string {
	return testBlockStart + "\n" + inner + "\n" + testBlockEnd
}

func TestFindMarkedBlockPositive(t *testing.T) {
	content := "# Title\n\n  " + testBlockStart + "  \nold\n" + testBlockEnd + "\ntail\n"
	first, last, err := FindMarkedBlock(content, testBlockStart, testBlockEnd)
	if err != nil {
		t.Fatalf("FindMarkedBlock: %v", err)
	}
	if first != 2 || last != 4 {
		t.Fatalf("span = %d..%d, want 2..4", first, last)
	}
}

func TestFindMarkedBlockNegative(t *testing.T) {
	cases := map[string]struct {
		content    string
		start, end string
		want       error
	}{
		"start without end": {"a\n" + testBlockStart + "\nb\n", testBlockStart, testBlockEnd, ErrMarkedBlockUnbalanced},
		"end without start": {"a\n" + testBlockEnd + "\n", testBlockStart, testBlockEnd, ErrMarkedBlockUnbalanced},
		"end before start":  {testBlockEnd + "\n" + testBlockStart + "\n", testBlockStart, testBlockEnd, ErrMarkedBlockUnbalanced},
		"duplicated start":  {testBlockStart + "\n" + testBlockStart + "\n" + testBlockEnd, testBlockStart, testBlockEnd, ErrMarkedBlockDuplicated},
		"duplicated end":    {testBlock("x") + "\n" + testBlockEnd, testBlockStart, testBlockEnd, ErrMarkedBlockDuplicated},
		"empty marker":      {"a", "", testBlockEnd, ErrMarkedBlockArguments},
		"identical markers": {"a", testBlockStart, testBlockStart, ErrMarkedBlockArguments},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			if _, _, err := FindMarkedBlock(tc.content, tc.start, tc.end); !errors.Is(err, tc.want) {
				t.Fatalf("error = %v, want %v", err, tc.want)
			}
		})
	}
}

func TestFindMarkedBlockBoundary(t *testing.T) {
	first, last, err := FindMarkedBlock("", testBlockStart, testBlockEnd)
	if err != nil || first != -1 || last != -1 {
		t.Fatalf("empty document = %d..%d, %v; want -1..-1, nil", first, last, err)
	}
	fenced := "~~~~md\n" + testBlock("example") + "\n~~~\nstill fenced\n~~~~\n" + testBlock("live")
	first, last, err = FindMarkedBlock(fenced, testBlockStart, testBlockEnd)
	if err != nil || first != 7 || last != 9 {
		t.Fatalf("fenced document = %d..%d, %v; want 7..9, nil", first, last, err)
	}
	atLimit := strings.Repeat("\n", MaxMarkedBlockLines-1)
	if _, _, err := FindMarkedBlock(atLimit, testBlockStart, testBlockEnd); err != nil {
		t.Fatalf("document of exactly %d lines: %v", MaxMarkedBlockLines, err)
	}
	if _, _, err := FindMarkedBlock(atLimit+"\n", testBlockStart, testBlockEnd); !errors.Is(err, ErrMarkedBlockBudget) {
		t.Fatalf("document one line over the scan bound: error = %v, want %v", err, ErrMarkedBlockBudget)
	}
}

func TestFindMarkedBlockWithinBudgetPositive(t *testing.T) {
	content := strings.Repeat("keep\n", MaxMarkedBlockLines) + testBlock("old")
	budget := MaxMarkedBlockLines + 3
	first, last, err := FindMarkedBlockWithinBudget(content, testBlockStart, testBlockEnd, budget)
	if err != nil || first != MaxMarkedBlockLines || last != budget-1 {
		t.Fatalf("span=%d..%d err=%v, want %d..%d", first, last, err, MaxMarkedBlockLines, budget-1)
	}
}

func TestFindMarkedBlockWithinBudgetNegative(t *testing.T) {
	for name, tc := range map[string]struct {
		content string
		budget  int
		want    error
	}{
		"unbalanced":  {testBlockStart + "\n", 2, ErrMarkedBlockUnbalanced},
		"duplicated":  {testBlock("a") + "\n" + testBlock("b"), 7, ErrMarkedBlockDuplicated},
		"zero budget": {"", 0, ErrMarkedBlockArguments},
	} {
		t.Run(name, func(t *testing.T) {
			if _, _, err := FindMarkedBlockWithinBudget(tc.content, testBlockStart, testBlockEnd, tc.budget); !errors.Is(err, tc.want) {
				t.Fatalf("error = %v, want %v", err, tc.want)
			}
		})
	}
}

func TestFindMarkedBlockWithinBudgetBoundary(t *testing.T) {
	content := "```md\n" + testBlock("example") + "\n```\n" + testBlock("live")
	lineCount := strings.Count(content, "\n") + 1
	first, last, err := FindMarkedBlockWithinBudget(content, testBlockStart, testBlockEnd, lineCount)
	if err != nil || first != 5 || last != 7 {
		t.Fatalf("exact-budget fenced scan = %d..%d, %v; want 5..7, nil", first, last, err)
	}
	if _, _, err := FindMarkedBlockWithinBudget(content, testBlockStart, testBlockEnd, lineCount-1); !errors.Is(err, ErrMarkedBlockBudget) {
		t.Fatalf("budget minus one: error = %v, want %v", err, ErrMarkedBlockBudget)
	}
}

func TestReplaceMarkedBlockPositive(t *testing.T) {
	content := "# Title\n\n## Section\n" + testBlock("old") + "\n\n## After\ntext\n"
	out, changed, err := ReplaceMarkedBlock(content, testBlockStart, testBlockEnd, testBlock("new"), 100)
	if err != nil || !changed {
		t.Fatalf("replace: changed=%v err=%v", changed, err)
	}
	want := "# Title\n\n## Section\n" + testBlock("new") + "\n\n## After\ntext\n"
	if out != want {
		t.Fatalf("replaced document:\n%q\nwant:\n%q", out, want)
	}
	again, changed, err := ReplaceMarkedBlock(out, testBlockStart, testBlockEnd, testBlock("new"), 100)
	if err != nil || changed || again != out {
		t.Fatalf("second replace must be a no-op: changed=%v err=%v", changed, err)
	}

	appended, changed, err := ReplaceMarkedBlock("# Title\ntext\n\n\n", testBlockStart, testBlockEnd, testBlock("new"), 100)
	if err != nil || !changed {
		t.Fatalf("append: changed=%v err=%v", changed, err)
	}
	if want := "# Title\ntext\n\n" + testBlock("new") + "\n"; appended != want {
		t.Fatalf("appended document:\n%q\nwant:\n%q", appended, want)
	}
	if _, changed, err = ReplaceMarkedBlock(appended, testBlockStart, testBlockEnd, testBlock("new"), 100); err != nil || changed {
		t.Fatalf("append then replace must be a no-op: changed=%v err=%v", changed, err)
	}
}

func TestReplaceMarkedBlockNegative(t *testing.T) {
	if _, _, err := ReplaceMarkedBlock("a\n"+testBlockStart+"\nrest of file\n", testBlockStart, testBlockEnd, testBlock("new"), 100); !errors.Is(err, ErrMarkedBlockUnbalanced) {
		t.Fatalf("unterminated marker: error = %v, want %v", err, ErrMarkedBlockUnbalanced)
	}
	duplicated := testBlock("a") + "\n" + testBlock("b") + "\n"
	if _, _, err := ReplaceMarkedBlock(duplicated, testBlockStart, testBlockEnd, testBlock("new"), 100); !errors.Is(err, ErrMarkedBlockDuplicated) {
		t.Fatalf("duplicated markers: error = %v, want %v", err, ErrMarkedBlockDuplicated)
	}
	if _, _, err := ReplaceMarkedBlock("one\ntwo\n", testBlockStart, testBlockEnd, testBlock("new"), 5); !errors.Is(err, ErrMarkedBlockBudget) {
		t.Fatalf("over budget: error = %v, want %v", err, ErrMarkedBlockBudget)
	}
	if _, _, err := ReplaceMarkedBlock("one\n", testBlockStart, testBlockEnd, testBlock("new"), 0); !errors.Is(err, ErrMarkedBlockArguments) {
		t.Fatalf("zero budget: error = %v, want %v", err, ErrMarkedBlockArguments)
	}
}

func TestReplaceMarkedBlockBoundary(t *testing.T) {
	// Markers on the first and last line, replaced by an empty body.
	out, changed, err := ReplaceMarkedBlock(testBlock("only"), testBlockStart, testBlockEnd, "", 10)
	if err != nil || !changed || out != "" {
		t.Fatalf("empty body over a whole-document span: out=%q changed=%v err=%v", out, changed, err)
	}
	// An empty document receives the body without a leading blank line.
	out, _, err = ReplaceMarkedBlock("", testBlockStart, testBlockEnd, testBlock("new"), 3)
	if err != nil || out != testBlock("new")+"\n" {
		t.Fatalf("empty document: out=%q err=%v", out, err)
	}
	// A marker inside fenced code is documentation, not the live block.
	fenced := "```md\n" + testBlock("example") + "\n```\n"
	out, changed, err = ReplaceMarkedBlock(fenced, testBlockStart, testBlockEnd, testBlock("new"), 100)
	if err != nil || !changed || !strings.Contains(out, "example") || !strings.HasSuffix(out, testBlock("new")+"\n") {
		t.Fatalf("fenced marker: out=%q changed=%v err=%v", out, changed, err)
	}
	// maxLines equal to the resulting line count passes; one less fails.
	content := "a\n" + testBlock("old") + "\nb\n"
	if _, _, err := ReplaceMarkedBlock(content, testBlockStart, testBlockEnd, testBlock("new"), 5); err != nil {
		t.Fatalf("exact budget: %v", err)
	}
	if _, _, err := ReplaceMarkedBlock(content, testBlockStart, testBlockEnd, testBlock("new"), 4); !errors.Is(err, ErrMarkedBlockBudget) {
		t.Fatalf("budget minus one: error = %v, want %v", err, ErrMarkedBlockBudget)
	}
}

func TestReplaceMarkedBlockWithinBudgetPositive(t *testing.T) {
	content := strings.Repeat("keep\n", MaxMarkedBlockLines) + testBlock("old")
	budget := MaxMarkedBlockLines + 3
	out, changed, err := ReplaceMarkedBlockWithinBudget(content, testBlockStart, testBlockEnd, testBlock("new"), budget)
	if err != nil || !changed {
		t.Fatalf("replace above default scan budget: changed=%v err=%v", changed, err)
	}
	if !strings.HasSuffix(out, testBlock("new")) || strings.Contains(out, "old") {
		t.Fatal("caller-bounded replacement did not replace the marked block")
	}
	if _, _, err := ReplaceMarkedBlock(content, testBlockStart, testBlockEnd, testBlock("new"), budget); !errors.Is(err, ErrMarkedBlockBudget) {
		t.Fatalf("legacy entrypoint scan changed: error = %v, want %v", err, ErrMarkedBlockBudget)
	}
}

func TestReplaceMarkedBlockWithinBudgetNegative(t *testing.T) {
	duplicated := testBlock("a") + "\n" + testBlock("b")
	for name, tc := range map[string]struct {
		content string
		budget  int
		want    error
	}{
		"unbalanced":  {testBlockStart + "\nrest", 2, ErrMarkedBlockUnbalanced},
		"duplicated":  {duplicated, 7, ErrMarkedBlockDuplicated},
		"zero budget": {"", 0, ErrMarkedBlockArguments},
	} {
		t.Run(name, func(t *testing.T) {
			if _, _, err := ReplaceMarkedBlockWithinBudget(tc.content, testBlockStart, testBlockEnd, testBlock("new"), tc.budget); !errors.Is(err, tc.want) {
				t.Fatalf("error = %v, want %v", err, tc.want)
			}
		})
	}
}

func TestReplaceMarkedBlockWithinBudgetBoundary(t *testing.T) {
	content := "a\n" + testBlock("old") + "\nb"
	if _, _, err := ReplaceMarkedBlockWithinBudget(content, testBlockStart, testBlockEnd, testBlock("new"), 5); err != nil {
		t.Fatalf("exact budget: %v", err)
	}
	if _, _, err := ReplaceMarkedBlockWithinBudget(content, testBlockStart, testBlockEnd, testBlock("new"), 4); !errors.Is(err, ErrMarkedBlockBudget) {
		t.Fatalf("budget minus one: error = %v, want %v", err, ErrMarkedBlockBudget)
	}
}

func TestRemoveMarkdownSectionPositive(t *testing.T) {
	content := "# Top\n\n## Legacy\nold\n\n### Keep\ntail\n"
	out, err := RemoveMarkdownSection(content, "## Legacy", 8)
	if err != nil {
		t.Fatal(err)
	}
	if want := "# Top\n\n### Keep\ntail\n"; out != want {
		t.Fatalf("removed section = %q, want %q", out, want)
	}
}

func TestRemoveMarkdownSectionNegative(t *testing.T) {
	if _, err := RemoveMarkdownSection("a\nb", "", 2); !errors.Is(err, ErrMarkedBlockArguments) {
		t.Fatalf("empty heading: error = %v, want %v", err, ErrMarkedBlockArguments)
	}
	if _, err := RemoveMarkdownSection("a\nb", "## Legacy", 1); !errors.Is(err, ErrMarkedBlockBudget) {
		t.Fatalf("over budget: error = %v, want %v", err, ErrMarkedBlockBudget)
	}
}

func TestRemoveMarkdownSectionBoundaryPreservesFencedExample(t *testing.T) {
	content := "```md\n## Legacy\nexample\n```\n\n## Legacy\nold\n###\tKeep\ntail"
	out, err := RemoveMarkdownSection(content, "## Legacy", 9)
	if err != nil {
		t.Fatal(err)
	}
	if want := "```md\n## Legacy\nexample\n```\n\n###\tKeep\ntail"; out != want {
		t.Fatalf("fenced heading was not preserved: got %q want %q", out, want)
	}
}
