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
	// A shorter run never closes a longer one. Callers hand the tracker an already
	// trimmed line, so the only whitespace a closing delimiter can still carry is
	// interior whitespace: "```` `" is the line that separates the tolerant close the
	// ledger parsers use from the stricter one, and deleting " \t" from the cutset in
	// Inside has to fail here.
	live, unterminated := scanFence("````md\n```\nstill fenced\n```` `\nafter")
	if unterminated {
		t.Fatal("an interleaved closing delimiter did not close the fence")
	}
	if len(live) != 1 || live[0] != "after" {
		t.Fatalf("nested run handling is wrong: %q", live)
	}

	// The same tolerance on a bare run, on a tab rather than a space, and on tildes.
	for _, document := range []string{
		"```\n- [ ] example\n``` ```\nafter",
		"```\n- [ ] example\n```\t```\nafter",
		"~~~\n- [ ] example\n~~~ ~~~\nafter",
	} {
		live, unterminated := scanFence(document)
		if unterminated || len(live) != 1 || live[0] != "after" {
			t.Fatalf("%q did not close its fence: %q, open=%v", document, live, unterminated)
		}
	}

	// A delimiter run followed by anything else is content, not a close: the fence
	// stays open and the scan reports it rather than promoting the examples after it.
	if live, unterminated := scanFence("```\n- [ ] example\n``` x\n- [ ] also example"); !unterminated || len(live) != 0 {
		t.Fatalf("a close carrying a word ended the fence: %q, open=%v", live, unterminated)
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

// TestMarkdownFenceMarkerReportsTheOpenDelimiter pins the accessor the caveman scanner
// takes the info string of a fence from, so that no scanner recomputes the run itself.
func TestMarkdownFenceMarkerReportsTheOpenDelimiter(t *testing.T) {
	var fence util.MarkdownFence
	if fence.Marker() != "" {
		t.Fatalf("a fresh tracker reports marker %q", fence.Marker())
	}
	if !fence.Inside("````md") || fence.Marker() != "````" {
		t.Fatalf("opening run reported as %q", fence.Marker())
	}
	if !fence.Inside("```") || fence.Marker() != "````" {
		t.Fatalf("a shorter run changed the marker: %q", fence.Marker())
	}
	if !fence.Inside("````") || fence.Marker() != "" {
		t.Fatalf("a closed fence still reports marker %q", fence.Marker())
	}
}

// TestFindMarkedBlockTolerantCloseInsideTheCompiledContext carries the tolerant close
// through markerLines, the AGENTS.md compile-context path. A marker quoted inside the
// fence stays quoted, and the real marker after the interleaved close is the one found.
func TestFindMarkedBlockTolerantCloseInsideTheCompiledContext(t *testing.T) {
	document := strings.Join([]string{
		"# Canonical",
		"```md",
		"<!-- praetor:start -->",
		"quoted example",
		"``` `",
		"<!-- praetor:start -->",
		"body",
		"<!-- praetor:end -->",
	}, "\n")
	first, last, err := util.FindMarkedBlock(document, "<!-- praetor:start -->", "<!-- praetor:end -->")
	if err != nil || first != 5 || last != 7 {
		t.Fatalf("marked block after an interleaved close: %d..%d, %v", first, last, err)
	}
}

// TestMarkdownFenceBacktickInfoStringIsAnInlineSpan pins the CommonMark rule that a
// backtick fence's info string may not carry a backtick: "```make verify-all``` must
// pass" is inline code, so the checkbox after it stays live and the scan ends closed. A
// tilde fence's info string may carry backticks and still opens.
func TestMarkdownFenceBacktickInfoStringIsAnInlineSpan(t *testing.T) {
	var fence util.MarkdownFence
	for _, line := range []string{"```make verify-all``` must pass", "````x` y", "```go`"} {
		if fence.Inside(line) || fence.Open() {
			t.Fatalf("%q opened a fence", line)
		}
	}

	live, unterminated := scanFence("```make verify-all``` must pass\n- [ ] live")
	if unterminated || len(live) != 2 || live[1] != "- [ ] live" {
		t.Fatalf("an inline span opened a fence: %q, open=%v", live, unterminated)
	}

	// Positive: a plain info string, and a tilde fence whose info string quotes a span.
	for _, document := range []string{"```go\n- [ ] example\n```\nafter", "~~~ `sh` demo\n- [ ] example\n~~~\nafter"} {
		live, unterminated := scanFence(document)
		if unterminated || len(live) != 1 || live[0] != "after" {
			t.Fatalf("%q did not fence its content: %q, open=%v", document, live, unterminated)
		}
	}

	// Boundary: inside an open fence an inline-span line is content, never a close, and a
	// bare run of six backticks still opens a fence with an empty info string.
	if live, unterminated := scanFence("```\n```x``` y\nstill fenced"); !unterminated || len(live) != 0 {
		t.Fatalf("an inline span closed an open fence: %q, open=%v", live, unterminated)
	}
	if !fence.Inside("``````") || fence.Marker() != "``````" {
		t.Fatalf("a bare six-backtick run did not open: %q", fence.Marker())
	}
}
