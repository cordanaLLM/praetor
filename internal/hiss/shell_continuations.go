// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package hiss

import "strings"

// JoinShellContinuations shares the shell lexer with the hold and background guards.
// Only active backslash-newline pairs join: physical comments, single quotes, escaped
// backslashes and heredoc bodies retain their newlines. Both LF and CRLF are supported.
func JoinShellContinuations(script string) string {
	var lexer shellLexer
	var code, joined strings.Builder
	for _, line := range strings.SplitAfter(script, "\n") {
		lexer.joinLine(line, &code, &joined)
	}
	return joined.String()
}

// joinLine retains heredoc bodies and finishes lexer state only at an unjoined newline.
func (l *shellLexer) joinLine(line string, code, joined *strings.Builder) {
	text := strings.TrimSuffix(line, "\n")
	newline := len(text) < len(line)
	if newline {
		text = strings.TrimSuffix(text, "\r")
	}
	if len(l.bodies) > 0 {
		l.bodyLine(text)
		joined.WriteString(line)
		return
	}
	if l.joinText(text, newline, code, joined) {
		return
	}
	joined.WriteString(line[len(text):])
	l.finishLine()
	code.Reset()
}

// joinText copies lexer-consumed spans, stopping before an active continuation or comment.
func (l *shellLexer) joinText(text string, newline bool, code, joined *strings.Builder) bool {
	for i := 0; i < len(text); {
		if l.joinComment(text[i], code.String()) {
			joined.WriteString(text[i:])
			return false
		}
		if newline && text[i] == '\\' && i == len(text)-1 && l.joinContext() {
			return true
		}
		next := min(l.step(text, i, code), len(text))
		joined.WriteString(text[i:next])
		i = next
	}
	return false
}

// joinComment uses the lexer's active context and decoded boundary, including escaped blanks.
func (l *shellLexer) joinComment(c byte, code string) bool {
	frame := l.top()
	if frame != nil && frame.ctx != shellCommand && frame.ctx != shellBacktick {
		return false
	}
	return c == '#' && shellCommentBoundary(code)
}

// joinContext excludes quotes whose backslashes cannot introduce shell continuations.
func (l *shellLexer) joinContext() bool {
	frame := l.top()
	return frame == nil || (frame.ctx != shellSingle && frame.ctx != shellAnsi)
}

// shellCommentBoundary checks decoded code so an escaped separator never starts a comment.
func shellCommentBoundary(code string) bool {
	return len(code) == 0 || strings.ContainsRune(" \t\r\n;&|()<>", rune(code[len(code)-1]))
}
