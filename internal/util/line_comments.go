package util

import (
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"
)

// maxCommentScanLines bounds the lines StripHashComments walks (HISS-02).
const maxCommentScanLines = 100000

// StripHashComments removes "#" comments from YAML or shell text, line by line: a "#" that
// starts a line (after indentation) or follows whitespace starts a comment running to the
// end of that line. A "#" inside a token, such as a URL fragment or "a#b", or inside a
// quoted string, such as `echo "a # b"`, is kept (StripComments). Text longer than the scan
// bound is refused rather than passed through half-stripped.
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
// or "--", and trims the blanks before it. It is StripComments with marker as the one line
// comment marker. An empty marker leaves the line unchanged.
func StripLineComment(line, marker string) string {
	code, _ := StripComments(line, CommentSyntax{Line: []string{marker}}, "")
	return code
}

// CommentSyntax names the comment delimiters of one language for StripComments. Each Line
// marker opens a comment running to the end of the line; each Block pair opens a comment with
// its first string and closes it with its second, on the same line or a later one.
type CommentSyntax struct {
	Line  []string
	Block [][2]string
}

// StripComments returns one line of code without its comments, and the closer of a block
// comment still open at its end, "" when none. open is the closer an earlier line left open.
//
//   - A line marker opens a comment where the line starts or after a blank, so the "//" of a
//     URL and the "#" of "a#b" are code. A marker that ends in a letter, such as REM, opens
//     one only as a whole word.
//   - A block opener opens a comment anywhere; the comment reads as one blank.
//   - A marker inside a quoted string is code. A double or single quote that starts a word
//     quotes up to the next same quote on the line; one that nothing closes, or that follows
//     a letter or digit as the apostrophe of "don't" does, is a literal.
//
// A line that holds no comment comes back unchanged; otherwise the blanks a cut leaves at its
// end are trimmed.
func StripComments(line string, syntax CommentSyntax, open string) (string, string) {
	var code strings.Builder
	cut := open != ""
	for pos := 0; pos < len(line); {
		if open != "" {
			end := strings.Index(line[pos:], open)
			if end < 0 {
				return strings.TrimRight(code.String(), " \t"), open
			}
			pos, open = pos+end+len(open), ""
			code.WriteByte(' ')
			continue
		}
		step, closer, stop := commentAt(line, pos, syntax)
		if stop {
			return strings.TrimRight(code.String(), " \t"), ""
		}
		if closer != "" {
			pos, open, cut = pos+step, closer, true
			continue
		}
		code.WriteString(line[pos : pos+step])
		pos += step
	}
	if !cut {
		return line, open
	}
	return strings.TrimRight(code.String(), " \t"), open
}

// commentAt reads line at pos, outside every comment: stop when a line comment opens there,
// the closer and the opener's length when a block comment opens there, and otherwise the
// length of the code that starts there, a whole quoted string or one byte.
func commentAt(line string, pos int, syntax CommentSyntax) (int, string, bool) {
	if quoted := quotedLength(line, pos); quoted > 0 {
		return quoted, "", false
	}
	rest := line[pos:]
	for _, pair := range syntax.Block {
		if pair[0] != "" && strings.HasPrefix(rest, pair[0]) {
			return len(pair[0]), pair[1], false
		}
	}
	for _, marker := range syntax.Line {
		if startsWord(line, pos) && lineMarkerAt(rest, marker) {
			return 0, "", true
		}
	}
	return 1, "", false
}

// startsWord reports whether pos starts the line or follows a blank.
func startsWord(line string, pos int) bool {
	return pos == 0 || line[pos-1] == ' ' || line[pos-1] == '\t'
}

// lineMarkerAt reports whether rest opens with marker as a comment: a marker that ends in a
// letter must end a word there, so "REM" opens "REM note" but not "REMOVE".
func lineMarkerAt(rest, marker string) bool {
	if marker == "" || !strings.HasPrefix(rest, marker) {
		return false
	}
	if !unicode.IsLetter(rune(marker[len(marker)-1])) || len(rest) == len(marker) {
		return true
	}
	next := rest[len(marker)]
	return next == ' ' || next == '\t'
}

// quotedLength returns the length of the quoted string that opens at pos, closing quote
// included, or 0 when no quoted string opens there.
func quotedLength(line string, pos int) int {
	quote := line[pos]
	if quote != '"' && quote != '\'' {
		return 0
	}
	if before, _ := utf8.DecodeLastRuneInString(line[:pos]); unicode.IsLetter(before) || unicode.IsDigit(before) {
		return 0
	}
	end := strings.IndexByte(line[pos+1:], quote)
	if end < 0 {
		return 0
	}
	return end + 2
}
