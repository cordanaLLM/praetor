package caveman

import (
	"regexp"
	"strings"
)

const (
	// codeIndent is the indentation, in columns, that makes a line indented code
	// (CommonMark 0.31.2, 4.4), and tabStop the stop a tab advances to (2.2).
	codeIndent = 4
	tabStop    = 4
	// hardBreak is the trailing blank run that makes a line ending a hard line break
	// (CommonMark 0.31.2, 6.7).
	hardBreak = "  "
)

// listMarkerRe matches a bullet or ordered list marker at the start of a line.
var listMarkerRe = regexp.MustCompile(`^(?:[-*+]|\d{1,9}[.)])`)

// markIndentedCode reclassifies the lines of indented code blocks as code, so Compress keeps
// their bytes the way it keeps fenced code. The shared scan knows fences only: Check lints an
// indented block as the prose it has always read it as.
//
// The rule is CommonMark's (0.31.2, 4.4): a line that is not blank and is indented four or
// more columns is code unless it continues a paragraph, which indented code cannot interrupt,
// and the blank lines between two such lines belong to the block, with their bytes. Inside a
// list item the same indentation can be a nested paragraph instead; the scan does not track
// list nesting, so that paragraph keeps its bytes too. That costs a little compression and
// never meaning.
func markIndentedCode(lines []line) {
	paragraph := false
	last := -1
	for i := range lines {
		ln := &lines[i]
		switch {
		case blankBody(*ln):
			paragraph = false
		case ln.kind != kindBlank && indentedCode(ln.text, paragraph):
			keepLines(lines[last+1:i+1], last >= 0)
			paragraph, last = false, i
		default:
			paragraph, last = ln.kind == kindProse, -1
		}
	}
}

// keepLines marks the last of lines, an indented code line, as code, and with interior set
// the blank lines before it too: they separate it from the indented line above.
func keepLines(lines []line, interior bool) {
	first := len(lines) - 1
	if interior {
		first = 0
	}
	for i := first; i < len(lines); i++ {
		if lines[i].kind == kindProse || lines[i].kind == kindBlank {
			lines[i].kind = kindCode
		}
	}
}

// blankBody reports a line that is blank, or blank behind its blockquote markers.
func blankBody(ln line) bool {
	if ln.kind == kindBlank {
		return true
	}
	_, body := unquote(ln.text, len(ln.text))
	return ln.kind == kindProse && strings.TrimSpace(body) == ""
}

// indentedCode reports whether raw is a line of indented code: indented codeIndent columns at
// the start of the line or behind blockquote markers, unless it continues a paragraph, or a
// list item that opens with indented code ("-     code": one column belongs to the marker),
// which starts a block of its own and so holds inside a paragraph too.
func indentedCode(raw string, paragraph bool) bool {
	rest := raw
	for depth := 0; depth <= len(raw); depth++ {
		if indentColumns(rest) >= codeIndent {
			return !paragraph
		}
		trimmed := strings.TrimLeft(rest, " \t")
		if marker := listMarkerRe.FindString(trimmed); marker != "" {
			return indentColumns(trimmed[len(marker):]) > codeIndent
		}
		if !strings.HasPrefix(trimmed, ">") {
			return false
		}
		rest = strings.TrimPrefix(trimmed[1:], " ")
	}
	return false
}

// indentColumns counts the columns of blanks text opens with, a tab advancing to the next
// tab stop. A line of blanks only has no indentation to speak of and counts as zero.
func indentColumns(text string) int {
	columns := 0
	for i := 0; i < len(text); i++ {
		switch text[i] {
		case ' ':
			columns++
		case '\t':
			columns += tabStop - columns%tabStop
		default:
			return columns
		}
	}
	return 0
}

// proseEnd splits a prose line into the text squeeze works on and the bytes its line ending
// needs, and reports whether the line ends in a hard line break. followed says a line that is
// not blank comes after it: a break needs one, since neither form works at the end of a block
// (CommonMark 0.31.2, 6.7).
//
//   - Two or more trailing spaces before such a line are a hard line break: the trailing
//     blanks keep their bytes.
//   - A backslash at the very end is the other form, and stays where it is.
//   - A backslash with blanks after it is no break, and trimming them would make it one: one
//     blank stays.
//   - A code span that runs to the end of the line wraps onto the next, and its blanks are
//     code: the line keeps them.
//
// Everywhere else trailing blanks carry nothing and go.
func proseEnd(ln line, followed bool) (text, end string, held bool) {
	if n := len(ln.spans); n > 0 && ln.spans[n-1].end == len(ln.text) {
		return ln.text, "", false
	}
	text = strings.TrimRight(ln.text, " \t")
	blanks := ln.text[len(text):]
	escaped := (len(text)-len(strings.TrimRight(text, `\`)))%2 == 1
	switch {
	case !followed:
		return text, "", false
	case strings.HasSuffix(blanks, hardBreak):
		return text, blanks, true
	case escaped && blanks != "":
		return text, blanks[:1], false
	}
	return text, "", escaped
}
