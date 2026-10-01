package util

import (
	"fmt"
	"strings"
)

// maxCommentScanLines bounds the lines StripHashComments walks (HISS-02).
const maxCommentScanLines = 100000

// StripHashComments removes "#" comments from YAML or shell text, line by line: a "#" that
// starts a line (after indentation) or follows whitespace starts a comment running to the
// end of that line. A "#" inside a token, such as a URL fragment or "a#b", is kept.
// Quoting is not tracked, so a quoted " #" is treated as a comment too; the callers read
// workflow `uses:` references and `run:` commands, where that shape does not carry meaning.
// Text longer than the scan bound is refused rather than passed through half-stripped.
func StripHashComments(text string) (string, error) {
	lines := strings.Split(text, "\n")
	if len(lines) > maxCommentScanLines {
		return "", fmt.Errorf("text exceeds %d lines", maxCommentScanLines)
	}
	for i := 0; i < len(lines) && i < maxCommentScanLines; i++ {
		lines[i] = stripLineComment(lines[i])
	}
	return strings.Join(lines, "\n"), nil
}

// stripLineComment cuts one line at its first comment-starting "#".
func stripLineComment(line string) string {
	return StripLineComment(line, "#")
}

// StripLineComment cuts one line at its first comment that marker opens, such as "#", "//"
// or "--", and trims the blanks before it: a marker that starts the line or follows a blank
// starts a comment running to the end of the line. A marker inside a token, such as the
// "//" of a URL or the "#" of "a#b", is kept. Quoting is not tracked, as in
// StripHashComments. An empty marker leaves the line unchanged.
func StripLineComment(line, marker string) string {
	if marker == "" {
		return line
	}
	for from := 0; from < len(line); {
		next := strings.Index(line[from:], marker)
		if next < 0 {
			break
		}
		at := from + next
		if at == 0 || line[at-1] == ' ' || line[at-1] == '\t' {
			return strings.TrimRight(line[:at], " \t")
		}
		from = at + 1
	}
	return line
}
