package util

import "strings"

// maxLineDeltaLines bounds every line walk of LineDeltaOf (HISS-02). It sits well above the
// line count of the largest text the repository writers accept (contextopt.MaxSourceBytes); a
// longer text keeps its remainder in the last line compared.
const maxLineDeltaLines = 1 << 21

// LineDelta summarises how a replacement text differs from the text it replaces, line by line:
// how many lines of the old text it drops, how many lines it adds, and the first dropped lines
// in their old order.
type LineDelta struct {
	Removed      int
	Added        int
	RemovedLines []string
}

// Changed reports whether the replacement drops or adds any line.
func (d LineDelta) Changed() bool {
	return d.Removed != 0 || d.Added != 0
}

// LineDeltaOf returns the LineDelta from before to after, keeping at most limit removed lines.
// Both texts are compared as LF text (NormalizeLineEndings), so a text and its CRLF checkout
// differ in nothing. The lines both texts share at the start and at the end are unchanged;
// between them a line is unchanged as often as it occurs on both sides, so one edited line
// counts once rather than shifting every line after it. The counts are that multiset summary: a
// line that moved beside other changes still matches its twin and does not count. Only a middle
// that changed nothing but its order, where every line matches, is counted whole as removed and
// added again. A lost final newline is one removed empty line. The walk is linear and bounded, a
// summary for a report rather than a minimal diff.
func LineDeltaOf(before, after string, limit int) LineDelta {
	oldLines, newLines := splitDeltaLines(before), splitDeltaLines(after)
	oldMiddle, newMiddle := trimCommonLines(oldLines, newLines)
	delta := countLineChanges(oldMiddle, newMiddle, limit)
	if !delta.Changed() && len(oldMiddle) > 0 {
		delta = LineDelta{Removed: len(oldMiddle), Added: len(newMiddle), RemovedLines: firstLines(oldMiddle, limit)}
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

// firstLines returns a copy of at most limit leading lines.
func firstLines(lines []string, limit int) []string {
	if limit <= 0 {
		return nil
	}
	if len(lines) > limit {
		lines = lines[:limit]
	}
	return append([]string(nil), lines...)
}
