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
