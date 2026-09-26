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
	for i := 0; i < len(line); i++ {
		if line[i] != '#' {
			continue
		}
		if i == 0 || line[i-1] == ' ' || line[i-1] == '\t' {
			return strings.TrimRight(line[:i], " \t")
		}
	}
	return line
}
