// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package util

import (
	"errors"
	"strings"
)

// shellWordEnd lists blanks and shell metacharacters ending an unquoted word.
const shellWordEnd = " \t;&|<>()"

// ShellHereDocDelimiter reads a delimiter after <<, its optional '-' and blanks.
// It returns the quote-removed delimiter, whether any part was quoted or escaped,
// and the width of the original word. Whole and partial quoting are supported.
// Expansion-like, empty and unterminated words are refused rather than guessed.
func ShellHereDocDelimiter(text string) (delimiter string, quoted bool, width int, err error) {
	var word strings.Builder
	for width < len(text) && strings.IndexByte(shellWordEnd, text[width]) < 0 {
		part, isQuoted, n, partErr := shellDelimiterPart(text[width:])
		if partErr != nil {
			return "", false, 0, partErr
		}
		word.WriteString(part)
		quoted = quoted || isQuoted
		width += n
	}
	if word.Len() == 0 {
		return "", false, 0, errors.New("empty heredoc delimiter")
	}
	return word.String(), quoted, width, nil
}

// shellDelimiterPart consumes one literal byte, escaped byte or complete quoted span.
func shellDelimiterPart(text string) (string, bool, int, error) {
	switch text[0] {
	case '\'':
		end := strings.IndexByte(text[1:], '\'')
		if end < 0 {
			return "", false, 0, errors.New("unterminated heredoc quote")
		}
		return text[1 : end+1], true, end + 2, nil
	case '"':
		return shellDelimiterDouble(text)
	case '\\':
		if len(text) < 2 || strings.ContainsAny(text[1:2], "\r\n") {
			return "", false, 0, errors.New("unterminated heredoc escape")
		}
		return text[1:2], true, 2, nil
	case '$', '`', '\r', '\n':
		return "", false, 0, errors.New("unsupported heredoc delimiter")
	}
	return text[:1], false, 1, nil
}

// shellDelimiterDouble removes double quotes and their restricted backslash escapes.
func shellDelimiterDouble(text string) (string, bool, int, error) {
	var word strings.Builder
	for i := 1; i < len(text); i++ {
		switch text[i] {
		case '"':
			return word.String(), true, i + 1, nil
		case '$', '`', '\r', '\n':
			return "", false, 0, errors.New("unsupported heredoc delimiter")
		case '\\':
			if i+1 >= len(text) {
				return "", false, 0, errors.New("unterminated heredoc escape")
			}
			if strings.ContainsRune("$`\"\\", rune(text[i+1])) {
				i++
			}
		}
		word.WriteByte(text[i])
	}
	return "", false, 0, errors.New("unterminated heredoc quote")
}
