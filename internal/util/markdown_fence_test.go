package util_test

import (
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/util"
)

// scanFence drives a fresh tracker over a document and returns the lines it reported as
// live content plus whether a fence was still open when the scan ended.
func scanFence(document string) (live []string, unterminated bool) {
	var fence util.MarkdownFence
	for _, line := range strings.Split(document, "\n") {
		trimmed := strings.TrimSpace(line)
		if fence.Inside(trimmed) {
			continue
		}
		live = append(live, trimmed)
	}
	return live, fence.Open()
}

func TestMarkdownFencePositiveSkipsFencedContent(t *testing.T) {
	live, unterminated := scanFence("- [ ] live\n```sh\n- [ ] example\n```\n- [x] also live")
	if unterminated {
		t.Fatal("a balanced fence was reported as unterminated")
	}
	want := []string{"- [ ] live", "- [x] also live"}
	if len(live) != len(want) || live[0] != want[0] || live[1] != want[1] {
		t.Fatalf("fenced content leaked into the scan: %q", live)
	}
}

func TestMarkdownFenceNegativeIgnoresNonFenceLines(t *testing.T) {
	var fence util.MarkdownFence
	for _, line := range []string{"| a | b |", "``inline code``", "~~strike~~", "#### heading", ""} {
		if fence.Inside(line) {
			t.Fatalf("%q was treated as a fence delimiter", line)
		}
		if fence.Open() {
			t.Fatalf("%q opened a fence", line)
		}
	}
}

func TestMarkdownFenceBoundaryRunsAndUnterminatedFences(t *testing.T) {
	// A shorter run never closes a longer one, and trailing whitespace on the closing
	// delimiter still closes it.
	live, unterminated := scanFence("````md\n```\nstill fenced\n```` \nafter")
	if unterminated {
		t.Fatal("a padded closing delimiter did not close the fence")
	}
	if len(live) != 1 || live[0] != "after" {
		t.Fatalf("nested run handling is wrong: %q", live)
	}

	// A tilde fence is never closed by a backtick run of the same length.
	live, unterminated = scanFence("~~~\n```\nswallowed")
	if !unterminated || len(live) != 0 {
		t.Fatalf("mismatched delimiters closed a fence: %q, open=%v", live, unterminated)
	}

	// The documented boundary the ledger parsers rely on: a fence opened and never
	// closed reports Open at the end of the scan instead of silently eating the rest.
	if _, unterminated := scanFence("# Open\n```sh\n- [ ] ship"); !unterminated {
		t.Fatal("an unterminated fence was not reported")
	}
	if _, unterminated := scanFence(""); unterminated {
		t.Fatal("an empty document opened a fence")
	}
}
