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
	physical := strings.Split(strings.ReplaceAll(script, "\r\n", "\n"), "\n")
	if len(physical) > maxLines {
		return nil, false
	}
	var l shellLexer
	lines = make([]string, 0, len(physical))
	for _, text := range physical {
		code, _ := l.line(text)
		lines = append(lines, code)
	}
	if l.misread || l.open() {
		return nil, false
	}
	return lines, true
}

// MaskDataHeredocs removes plain data bodies while retaining executable substitutions.
// Unreadable constructs return the original script and false.
func MaskDataHeredocs(script string, maxLines int) (string, bool) {
	lines := strings.Split(script, "\n")
	if len(lines) > maxLines || strings.Contains(script, "\r") {
		return script, false
	}
	var l, expansion shellLexer
	for i, line := range lines {
		if len(l.bodies) > 0 {
			var ok bool
			lines[i], ok = maskHeredocBody(line, &l, &expansion)
			if !ok {
				return script, false
			}
			continue
		}
		if !maskHeredocHeader(line, &l) {
			return script, false
		}
	}
	if l.misread || expansion.misread || len(expansion.stack) > 0 || len(l.stack) > 0 {
		return script, false
	}
	return strings.Join(lines, "\n"), true
}

// maskHeredocHeader classifies bodies opened by one unambiguous command line.
func maskHeredocHeader(line string, l *shellLexer) bool {
	if len(l.stack) > 0 || strings.HasSuffix(strings.TrimSuffix(line, "\r"), `\`) {
		return false
	}
	code, _ := l.line(line)
	data := dataHeredocCommand(code, line)
	for j := range l.bodies {
		l.bodies[j].data = data
	}
	return true
}

// maskHeredocBody handles one body or closing delimiter line.
func maskHeredocBody(line string, l, expansion *shellLexer) (string, bool) {
	h := l.bodies[0]
	end := strings.TrimSuffix(line, "\r")
	if !h.quoted && strings.HasSuffix(end, `\`) {
		return line, false
	}
	if h.stripTabs {
		end = strings.TrimLeft(end, "\t")
	}
	l.bodyLine(strings.TrimSuffix(line, "\r"))
	if end == h.delimiter {
		return line, len(expansion.stack) == 0
	}
	if !h.data {
		return line, true
	}
	if h.quoted {
		return "", true
	}
	return heredocExpansions(line, expansion), true
}

// dataHeredocCommand accepts only an unambiguous cat or tee invocation without a pipe.
func dataHeredocCommand(code, raw string) bool {
	if strings.ContainsAny(raw, "|`") || strings.Contains(raw, "<(") || strings.Contains(raw, ">(") || strings.Contains(code, "$(") {
		return false
	}
	cmds := splitShellCommands(code)
	found := false
	for _, cmd := range cmds {
		if !heredocWords(cmd.words) {
			continue
		}
		found = true
		if !dataHeredocWords(cmd.words) {
			return false
		}
	}
	return found
}

// heredocWords reports whether the lexer left a heredoc operator in a command.
func heredocWords(words []string) bool {
	for _, word := range words {
		if strings.Contains(word, "<<") {
			return true
		}
	}
	return false
}

// dataHeredocWords follows the permitted wrappers to the data command.
func dataHeredocWords(words []string) bool {
	i := commandStart(words)
	for n := 0; n < maxShellWrappers && i < len(words); n++ {
		if words[i] != "env" && words[i] != "command" && words[i] != "exec" {
			break
		}
		i++
		for i < len(words) && shellAssignment.MatchString(words[i]) {
			i++
		}
	}
	return i < len(words) && (words[i] == "cat" || words[i] == "tee")
}

// heredocExpansions retains command substitutions in an expanding data body.
func heredocExpansions(line string, l *shellLexer) string {
	var out, discard strings.Builder
	for i := 0; i < len(line); {
		start := i
		if len(l.stack) == 0 {
			if line[i] == '\\' && i+1 < len(line) {
				i += 2
				continue
			}
			if strings.HasPrefix(line[i:], "$(") {
				l.push(shellCommand)
				i += 2
			} else if line[i] == '`' {
				l.push(shellBacktick)
				i++
			} else {
				i++
				continue
			}
		} else {
			i = l.step(line, i, &discard)
		}
		if i > len(line) {
			i = len(line)
		}
		out.WriteString(line[start:i])
	}
	return out.String()
}
