package util

import (
	"reflect"
	"testing"
)

// Positive: one edited line in the middle counts once, with the lines around it unchanged, and
// an added line counts as added without a removed one.
func TestLineDeltaOf_Positive_CountsEditsOnce(t *testing.T) {
	got := LineDeltaOf("alpha\nbeta\ngamma\ndelta\n", "alpha\nBETA\ngamma\ndelta\nepsilon\n", 5)
	want := LineDelta{Removed: 1, Added: 2, RemovedLines: []string{"beta"}}
	if !reflect.DeepEqual(got, want) || !got.Changed() {
		t.Fatalf("delta = %+v, want %+v", got, want)
	}
	if got := LineDeltaOf("one\ntwo\n", "one\ntwo\nthree\n", 5); got.Removed != 0 || got.Added != 1 || got.RemovedLines != nil {
		t.Fatalf("pure addition = %+v", got)
	}
}

// Positive: lines that only changed order drop and add nothing and count the line that moved,
// and a lost final newline is one removed empty line, so a changed text never reads as
// unchanged.
func TestLineDeltaOf_Positive_ReorderAndFinalNewlineAreChanges(t *testing.T) {
	got := LineDeltaOf("head\nb\na\ntail\n", "head\na\nb\ntail\n", 5)
	want := LineDelta{Moved: 1, MovedLines: []string{"b"}}
	if !reflect.DeepEqual(got, want) || !got.Changed() {
		t.Fatalf("reorder = %+v, want %+v", got, want)
	}
	got = LineDeltaOf("x\n", "x", 5)
	want = LineDelta{Removed: 1, RemovedLines: []string{""}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("lost final newline = %+v, want %+v", got, want)
	}
}

// Negative: identical texts, and a text against its CRLF checkout, have no delta; a limit of
// zero or less keeps the counts but quotes no line.
func TestLineDeltaOf_Negative_NoChangeAndNoQuotes(t *testing.T) {
	for name, pair := range map[string][2]string{
		"identical": {"a\nb\n", "a\nb\n"},
		"crlf only": {"a\r\nb\r\n", "a\nb\n"},
		"empty":     {"", ""},
	} {
		if got := LineDeltaOf(pair[0], pair[1], 3); got.Changed() || len(got.RemovedLines) != 0 {
			t.Errorf("%s: delta = %+v", name, got)
		}
	}
	for _, limit := range []int{0, -1} {
		if got := LineDeltaOf("a\nb\n", "c\n", limit); got.Removed != 2 || got.Added != 1 || got.RemovedLines != nil {
			t.Errorf("limit %d: delta = %+v", limit, got)
		}
	}
}

// Boundary: the quoted lines stop at the limit while the count covers every removed line, in
// the old text's order; an empty old text removes nothing and an empty new text removes all.
func TestLineDeltaOf_Boundary_QuotesStopAtLimit(t *testing.T) {
	got := LineDeltaOf("keep\nr1\nr2\nr3\nr4\nr5\n", "keep\n", 2)
	want := LineDelta{Removed: 5, RemovedLines: []string{"r1", "r2"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("delta = %+v, want %+v", got, want)
	}
	if got := LineDeltaOf("", "new\n", 2); got.Removed != 0 || got.Added != 1 {
		t.Fatalf("empty old text = %+v", got)
	}
	if got := LineDeltaOf("a\nb\n", "", 2); got.Removed != 2 || got.Added != 0 {
		t.Fatalf("empty new text = %+v", got)
	}
}

// Positive: one row moved from the top of a table to its bottom counts as that one row, not as
// every row of the table dropped and added again.
func TestLineDeltaOf_Positive_MovedRowCountsOnce(t *testing.T) {
	before := "| h |\n| r1 |\n| r2 |\n| r3 |\n| r4 |\n| r5 |\n"
	after := "| h |\n| r2 |\n| r3 |\n| r4 |\n| r5 |\n| r1 |\n"
	got := LineDeltaOf(before, after, 3)
	want := LineDelta{Moved: 1, MovedLines: []string{"| r1 |"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("moved row = %+v, want %+v", got, want)
	}
}

// Negative: an order change beside an edit is the multiset summary alone, with no moved count,
// and a reorder quotes no moved line under a limit of zero or less.
func TestLineDeltaOf_Negative_MovesBesideEditsAndZeroLimit(t *testing.T) {
	got := LineDeltaOf("a\nb\nc\n", "b\na\nC\n", 3)
	want := LineDelta{Removed: 1, Added: 1, RemovedLines: []string{"c"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("move beside edit = %+v, want %+v", got, want)
	}
	for _, limit := range []int{0, -1} {
		if got := LineDeltaOf("b\na\n", "a\nb\n", limit); got.Moved != 1 || got.MovedLines != nil || got.Removed != 0 {
			t.Errorf("limit %d: reorder = %+v", limit, got)
		}
	}
}

// Boundary: equal lines never cross, so duplicated rows around a moved one stay in place; a
// reversed table keeps one line and moves the rest, quoting only up to the limit.
func TestLineDeltaOf_Boundary_DuplicatesAndReversal(t *testing.T) {
	got := LineDeltaOf("x\ndup\ny\ndup\n", "dup\nx\ndup\ny\n", 5)
	if got.Removed != 0 || got.Added != 0 || got.Moved != 2 {
		t.Fatalf("duplicates = %+v, want two moved lines", got)
	}
	got = LineDeltaOf("1\n2\n3\n4\n5\n", "5\n4\n3\n2\n1\n", 2)
	want := LineDelta{Moved: 4, MovedLines: []string{"1", "2"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("reversal = %+v, want %+v", got, want)
	}
}

// Positive: terminated and unterminated last lines count the same, and CRLF text counts as its
// LF form.
func TestCountLines_Positive_LastLineWithAndWithoutNewline(t *testing.T) {
	for text, want := range map[string]int{"a\nb": 2, "a\nb\n": 2, "a\r\nb\r\n": 2, "one": 1} {
		if got := CountLines(text); got != want {
			t.Errorf("CountLines(%q) = %d, want %d", text, got, want)
		}
	}
}

// Negative: an empty text has no line, so a count of zero never hides a one-line file.
func TestCountLines_Negative_EmptyTextHasNoLine(t *testing.T) {
	if got := CountLines(""); got != 0 {
		t.Fatalf("CountLines(\"\") = %d, want 0", got)
	}
}

// Boundary: a lone newline is one empty line, and every further newline adds one.
func TestCountLines_Boundary_BlankLines(t *testing.T) {
	for text, want := range map[string]int{"\n": 1, "\n\n": 2, "a\n\n": 2, "\na": 2} {
		if got := CountLines(text); got != want {
			t.Errorf("CountLines(%q) = %d, want %d", text, got, want)
		}
	}
}
