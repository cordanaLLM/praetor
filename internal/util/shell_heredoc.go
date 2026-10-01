// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package util

import (
	"errors"
	"strings"
)

// shellWordEnd lists the bytes that end an unquoted shell word: blanks and the metacharacters
// a POSIX shell splits on.
const shellWordEnd = " \t;&|<>()"

// ShellHereDocDelimiter reads the delimiter word of a here-document from text, which starts
// at the word: the caller has already consumed the << operator, its optional '-' and the
// blanks after them. It returns the delimiter as the body's closing line must spell it,
// whether any part of the word was quoted (a quoted delimiter turns off expansion in the
// body), and how many bytes of text the word took.
//
// The word ends at a blank or a shell metacharacter, so `<<EOF;` and `<<EOF >out` both name
// EOF. A word quoted whole ('EOF', "EOF") or escaped (\EOF) is accepted. A word that is only
// partly quoted, holds an expansion or is missing is refused, because its delimiter is not the
// word as written and a reader that guessed would read the rest of the file as the body.
func ShellHereDocDelimiter(text string) (delimiter string, quoted bool, width int, err error) {
	if text == "" || strings.IndexByte(shellWordEnd, text[0]) >= 0 {
		return "", false, 0, errors.New("missing heredoc delimiter")
	}
	switch text[0] {
	case '\'', '"':
		end := strings.IndexByte(text[1:], text[0])
		if end < 0 {
			return "", false, 0, errors.New("unsupported heredoc delimiter")
		}
		width = end + 2
		delimiter, quoted = text[1:end+1], true
	case '\\':
		width = 1 + unquotedWordWidth(text[1:])
		delimiter, quoted = text[1:width], true
	default:
		width = unquotedWordWidth(text)
		delimiter = text[:width]
	}
	if delimiter == "" || strings.ContainsAny(delimiter, "$`'\"\\") ||
		(width < len(text) && strings.IndexByte(shellWordEnd, text[width]) < 0) {
		return "", false, 0, errors.New("unsupported heredoc delimiter")
	}
	return delimiter, quoted, width, nil
}

// unquotedWordWidth returns the length of the unquoted shell word text starts with.
func unquotedWordWidth(text string) int {
	for i := 0; i < len(text); i++ {
		if strings.IndexByte(shellWordEnd, text[i]) >= 0 || text[i] == '\'' || text[i] == '"' {
			return i
		}
	}
	return len(text)
}
