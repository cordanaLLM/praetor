package caveman

import (
	"reflect"
	"testing"
)

// TestScanDrivesTheSharedFenceTracker pins the caveman scanner on util.MarkdownFence
// instead of a restated copy of the fence rule (HISS-19). Both halves of that rule are
// observable here: a shorter run never closes a longer one, and a close that interleaves
// the delimiter with whitespace ("```` `") does close it - the case the scanner's own
// former copy read as content, leaving every later line classified as code.
func TestScanDrivesTheSharedFenceTracker(t *testing.T) {
	lines, s := scan("````md\n```\n## Quoted\n```` `\n## Live")
	if s.fence.Open() {
		t.Fatalf("the fence was left open: %q", s.fence.Marker())
	}
	wantKind := []lineKind{kindCode, kindCode, kindCode, kindCode, kindStructured}
	for i, want := range wantKind {
		if lines[i].kind != want {
			t.Fatalf("line %d classified as %v, want %v", i+1, lines[i].kind, want)
		}
	}
	if !lines[0].edge || !lines[3].edge || lines[1].edge || lines[2].edge {
		t.Fatalf("fence delimiters are not the edges: %+v", lines)
	}
	if lines[0].lang != "md" || lines[3].lang != "md" {
		t.Fatalf("the info string was lost: %q, %q", lines[0].lang, lines[3].lang)
	}
}

// TestScanBlockquotedFenceAndWrappedSpan pins the one model Check, Compress and Floor read: a
// fence inside a blockquote is code without its markers and ends with the quote (#356), and a
// code span that wraps is split into pieces, the later ones marked as continuing (#320).
func TestScanBlockquotedFenceAndWrappedSpan(t *testing.T) {
	lines, s := scan("> ```sh\n> make serve\n\nRun `a\nb` and `c`.")
	if s.fence.Open() {
		t.Fatal("the fence outlived its blockquote")
	}
	if lines[0].kind != kindCode || !lines[0].edge || lines[1].code != "make serve" || lines[1].shell == 0 {
		t.Fatalf("blockquoted fence: %+v", lines[:2])
	}
	if lines[2].kind != kindBlank || lines[3].kind != kindProse {
		t.Fatalf("lines after the quote: %+v", lines[2:])
	}
	wantFirst := []spanPiece{{start: 4, end: 6}}
	wantNext := []spanPiece{{start: 0, end: 2, cont: true}, {start: 7, end: 10}}
	if !reflect.DeepEqual(lines[3].spans, wantFirst) || !reflect.DeepEqual(lines[4].spans, wantNext) {
		t.Fatalf("span pieces %+v / %+v", lines[3].spans, lines[4].spans)
	}
	want := []lineItem{{num: 4, text: "`a b`"}, {num: 5, text: "`c`"}}
	if got := codeSpanTexts(lines); !reflect.DeepEqual(got, want) {
		t.Fatalf("span facts %+v, want %+v", got, want)
	}
}

// TestScanNegativeUnterminatedFenceStaysOpen keeps the scanner state readable by Check: a
// fence nobody closed reports as open rather than quietly resetting at end of input.
func TestScanNegativeUnterminatedFenceStaysOpen(t *testing.T) {
	lines, s := scan("# Title\n```sh\npraetorctl state sync .")
	if !s.fence.Open() {
		t.Fatal("an unterminated fence closed itself")
	}
	if lines[0].kind != kindStructured || lines[2].kind != kindCode {
		t.Fatalf("unterminated fence classification: %+v", lines)
	}
}
