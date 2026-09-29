package util

import (
	"sort"
	"strings"
)

// maxLineDeltaLines bounds every line walk of LineDeltaOf (HISS-02). It sits well above the
// line count of the largest text the repository writers accept (contextopt.MaxSourceBytes); a
// longer text keeps its remainder in the last line compared.
const maxLineDeltaLines = 1 << 21

// LineDelta summarises how a replacement text differs from the text it replaces, line by line:
// how many lines of the old text it drops, how many lines it adds, and the first dropped lines
// in their old order. A replacement that keeps every line and changes only their order drops
// and adds none; it counts the lines that left their place instead.
type LineDelta struct {
	Removed      int
	Added        int
	RemovedLines []string
	// Moved counts, for a replacement that changes only the order of its lines, the lines
	// outside the longest run of lines the two orders share; it is zero for any other change.
	Moved int
	// MovedLines are the first moved lines, in their old order.
	MovedLines []string
}

// Changed reports whether the replacement drops, adds or moves any line.
func (d LineDelta) Changed() bool {
	return d.Removed != 0 || d.Added != 0 || d.Moved != 0
}

// LineDeltaOf returns the LineDelta from before to after, keeping at most limit removed or
// moved lines. Both texts are compared as LF text (NormalizeLineEndings), so a text and its
// CRLF checkout differ in nothing. The lines both texts share at the start and at the end are
// unchanged; between them a line is unchanged as often as it occurs on both sides, so one
// edited line counts once rather than shifting every line after it. The counts are that
// multiset summary: a line that moved beside other changes still matches its twin and does not
// count. A middle that changed nothing but its order, where every line matches, drops and adds
// nothing and counts its moved lines (movedLines), so a reordered table reads as the rows that
// moved rather than as a replacement of every row. A lost final newline is one removed empty
// line. The walk is bounded and at most log-linear, a summary for a report rather than a
// minimal diff.
func LineDeltaOf(before, after string, limit int) LineDelta {
	oldLines, newLines := splitDeltaLines(before), splitDeltaLines(after)
	oldMiddle, newMiddle := trimCommonLines(oldLines, newLines)
	delta := countLineChanges(oldMiddle, newMiddle, limit)
	if !delta.Changed() && len(oldMiddle) > 0 {
		delta = movedLines(oldMiddle, newMiddle, limit)
	}
	return delta
}

// splitDeltaLines splits text into its LF lines. A final newline leaves an empty last line, so
// a text with one and a text without one differ in that line.
func splitDeltaLines(text string) []string {
	normalized, _ := NormalizeLineEndings(text)
	return strings.SplitN(normalized, "\n", maxLineDeltaLines)
}

// trimCommonLines drops the lines a and b share at the start and at the end and returns what is
// left of each.
func trimCommonLines(a, b []string) (aMiddle, bMiddle []string) {
	start := 0
	for start < len(a) && start < len(b) && start < maxLineDeltaLines && a[start] == b[start] {
		start++
	}
	endA, endB := len(a), len(b)
	for i := 0; i < maxLineDeltaLines && endA > start && endB > start && a[endA-1] == b[endB-1]; i++ {
		endA--
		endB--
	}
	return a[start:endA], b[start:endB]
}

// countLineChanges matches each old line against an unused equal new line and counts what
// matches nothing on either side, keeping the first limit unmatched old lines.
func countLineChanges(oldLines, newLines []string, limit int) LineDelta {
	unused := make(map[string]int, len(newLines))
	for i := 0; i < len(newLines) && i < maxLineDeltaLines; i++ {
		unused[newLines[i]]++
	}
	var delta LineDelta
	for i := 0; i < len(oldLines) && i < maxLineDeltaLines; i++ {
		if unused[oldLines[i]] > 0 {
			unused[oldLines[i]]--
			continue
		}
		delta.Removed++
		if len(delta.RemovedLines) < limit {
			delta.RemovedLines = append(delta.RemovedLines, oldLines[i])
		}
	}
	delta.Added = len(newLines) - (len(oldLines) - delta.Removed)
	return delta
}

// movedLines is the LineDelta of a middle whose lines changed only their order: newLines holds
// every line of oldLines as often. Each old line is paired with the same occurrence of it in
// newLines (pairedPositions); the old lines whose pairs keep their relative order along the
// longest such run stay in place (longestIncreasingRun) and every other one moved. The first
// and last lines of a trimmed middle differ, so at least one line moved.
func movedLines(oldLines, newLines []string, limit int) LineDelta {
	kept := longestIncreasingRun(pairedPositions(oldLines, newLines))
	var delta LineDelta
	for i := 0; i < len(oldLines) && i < maxLineDeltaLines; i++ {
		if kept[i] {
			continue
		}
		delta.Moved++
		if len(delta.MovedLines) < limit {
			delta.MovedLines = append(delta.MovedLines, oldLines[i])
		}
	}
	return delta
}

// pairedPositions returns, for each old line, the index in newLines of the occurrence it pairs
// with: the k-th occurrence of a line in oldLines pairs with its k-th occurrence in newLines,
// which never crosses two equal lines, so no pairing keeps more lines in place. A line newLines
// lacks pairs with nothing and gets -1.
func pairedPositions(oldLines, newLines []string) []int {
	at := make(map[string][]int, len(newLines))
	for i := 0; i < len(newLines) && i < maxLineDeltaLines; i++ {
		at[newLines[i]] = append(at[newLines[i]], i)
	}
	positions := make([]int, len(oldLines))
	for i := 0; i < len(oldLines) && i < maxLineDeltaLines; i++ {
		queue := at[oldLines[i]]
		if len(queue) == 0 {
			positions[i] = -1
			continue
		}
		positions[i], at[oldLines[i]] = queue[0], queue[1:]
	}
	return positions
}

// longestIncreasingRun marks the entries of positions on one longest strictly increasing
// subsequence, skipping negative ones. It keeps, for each run length, the entry ending the
// run with the smallest last value and each entry's predecessor, then walks back from the end
// of the longest run.
func longestIncreasingRun(positions []int) []bool {
	tails := make([]int, 0, len(positions))
	prev := make([]int, len(positions))
	for i := 0; i < len(positions) && i < maxLineDeltaLines; i++ {
		prev[i] = -1
		if positions[i] < 0 {
			continue
		}
		length := sort.Search(len(tails), func(k int) bool { return positions[tails[k]] >= positions[i] })
		if length > 0 {
			prev[i] = tails[length-1]
		}
		if length == len(tails) {
			tails = append(tails, i)
		} else {
			tails[length] = i
		}
	}
	kept := make([]bool, len(positions))
	if len(tails) == 0 {
		return kept
	}
	for i, steps := tails[len(tails)-1], 0; i >= 0 && steps < maxLineDeltaLines; i, steps = prev[i], steps+1 {
		kept[i] = true
	}
	return kept
}

// CountLines counts the lines of text the way a reader counts them: an empty text has none, a
// final newline ends the last line rather than starting another, and a last line without a
// newline still counts. "a\nb" and "a\nb\n" both have two lines; a CRLF text counts the same
// as its LF form because only the newline ends a line.
func CountLines(text string) int {
	if text == "" {
		return 0
	}
	return strings.Count(strings.TrimSuffix(text, "\n"), "\n") + 1
}
