// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package hiss

import "strings"

// ShellCode returns the code of each physical line of script as the shell scanner reads it
// (shell_lex.go): quoted text, comments and parameter expansions blanked, and a line of a
// here-document body returned empty. ok is false when script has more than maxLines lines,
// when the lexer could not follow a construct, or when a quote, substitution or here-document
// is still open at the end; a caller that judges commands treats that as unreadable.
func ShellCode(script string, maxLines int) (lines []string, ok bool) {
	return shellLines(script, maxLines, false)
}

// ShellSource returns validated shell source with here-document bodies removed.
// Quotes remain intact so callers can judge literal command words and shell payloads.
func ShellSource(script string, maxLines int) ([]string, bool) {
	return shellLines(script, maxLines, true)
}

// shellLines shares syntax validation and heredoc tracking for both shell views.
func shellLines(script string, maxLines int, source bool) (lines []string, ok bool) {
	physical := strings.Split(strings.ReplaceAll(script, "\r\n", "\n"), "\n")
	if len(physical) > maxLines {
		return nil, false
	}
	var l shellLexer
	lines = make([]string, 0, len(physical))
	for _, text := range physical {
		code, body := l.line(text)
		if source && !body {
			code = text
		}
		lines = append(lines, code)
	}
	if l.misread || l.open() {
		return nil, false
	}
	return lines, true
}
