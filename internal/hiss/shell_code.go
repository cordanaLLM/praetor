// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package hiss

import (
	"regexp"
	"strings"
)

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

// MaskDataHeredocs removes plain data bodies; unquoted substitutions remain unmasked.
// Unreadable constructs return the original script and false.
func MaskDataHeredocs(script string, maxLines int) (string, bool) {
	lines := strings.Split(script, "\n")
	if len(lines) > maxLines || strings.Contains(script, "\r") {
		return script, false
	}
	if heredocSharedOutput(script) || !heredocInputSafe(lines) {
		return script, false
	}
	if !maskHeredocLines(lines) {
		return script, false
	}
	return strings.Join(lines, "\n"), true
}

// maskHeredocLines classifies headers and processes each body with bounded lexer state.
func maskHeredocLines(lines []string) bool {
	var l shellLexer
	for i, line := range lines {
		if len(l.bodies) > 0 {
			var ok bool
			lines[i], ok = maskHeredocBody(line, &l)
			if !ok {
				return false
			}
			continue
		}
		if !maskHeredocHeader(line, &l) {
			return false
		}
	}
	return !l.misread && len(l.stack) == 0
}

// heredocShadow catches definitions conservatively, including ambiguous data and comments.
var heredocShadow = regexp.MustCompile(`(?:^|[^A-Za-z0-9_])(?:alias|function)(?:[^A-Za-z0-9_]|$)|[A-Za-z_][A-Za-z0-9_]*[ \t]*\([ \t]*\)`)

// heredocFD catches descriptor openings and output to inherited descriptors.
var heredocFD = regexp.MustCompile(`(?:^|[^0-9])[0-9]+[ \t]*[<>]|\{[^}]+\}[ \t]*[<>]|>[ \t]*&`)

// heredocInputSafe refuses whole-input ambiguity before any body is masked.
func heredocInputSafe(lines []string) bool {
	var l shellLexer
	for _, line := range lines {
		if heredocShadow.MatchString(line) || heredocFD.MatchString(line) || strings.Contains(line, "/dev/fd/") || strings.Contains(line, "/proc/self/fd/") {
			return false
		}
		code, body := l.line(line)
		if !body {
			// Quoted or expanded command words can disguise definitions or descriptors.
			if strings.Contains(line, `\`) || strings.ContainsAny(code, "\"'$") {
				return false
			}
			if _, ok := splitShellCommandsBounded(code); !ok {
				return false
			}
		}
	}
	return !l.misread
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
func maskHeredocBody(line string, l *shellLexer) (string, bool) {
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
		return line, true
	}
	if !h.quoted && (strings.Contains(line, "`") || strings.Contains(line, "$(") || strings.Contains(line, "${")) {
		return line, false
	}
	if h.data {
		return "", true
	}
	return line, true
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
	if i >= len(words) {
		return false
	}
	name := strings.SplitN(words[i], "<<", 2)[0]
	switch name {
	case "cat", "tee", "/bin/cat", "/usr/bin/cat", "/bin/tee", "/usr/bin/tee":
		return true
	}
	return false
}

// heredoc path patterns conservatively identify output words across commands.
var (
	heredocSpecial        = regexp.MustCompile(`(?:^|[^A-Za-z0-9_])(?:mkfifo|mknod)(?:[^A-Za-z0-9_]|$)`)
	heredocParts          = regexp.MustCompile(`[\n;&|]`)
	heredocPathWord       = regexp.MustCompile(`[A-Za-z0-9_./-]+`)
	heredocOutputCommand  = regexp.MustCompile(`(?:^|[ \t/])(?:cat|tee)(?:[ \t<]|$)`)
	heredocTee            = regexp.MustCompile(`(?:^|[ \t/])tee(?:[ \t]|$)`)
	heredocOutputRedirect = regexp.MustCompile(`>[ \t]*([A-Za-z0-9_./-]+)`)
	heredocRawOp          = regexp.MustCompile(`<<-?[ \t]*(?:'[^']+'|"[^"]+"|[A-Za-z_0-9.-]+)`)
)

// heredocOutputPaths collects conservative output path candidates from a data header.
func heredocOutputPaths(part string) []string {
	if !strings.Contains(part, "<<") || !heredocOutputCommand.MatchString(part) {
		return nil
	}
	clean := heredocRawOp.ReplaceAllString(part, " ")
	var paths []string
	for _, match := range heredocOutputRedirect.FindAllStringSubmatch(clean, -1) {
		paths = append(paths, match[1])
	}
	if loc := heredocTee.FindStringIndex(clean); loc != nil {
		paths = append(paths, heredocPathWord.FindAllString(clean[loc[1]:], -1)...)
	}
	return paths
}

// heredocPathAppears reports whether another command contains an output path word.
func heredocPathAppears(paths []string, parts []string, header int) bool {
	for i, part := range parts {
		if i == header {
			continue
		}
		for _, word := range heredocPathWord.FindAllString(part, -1) {
			for _, path := range paths {
				if word == path {
					return true
				}
			}
		}
	}
	return false
}

// heredocSharedOutput refuses special files and output paths used by another command.
func heredocSharedOutput(script string) bool {
	if heredocSpecial.MatchString(script) {
		return true
	}
	parts := heredocParts.Split(script, -1)
	for i, part := range parts {
		if heredocPathAppears(heredocOutputPaths(part), parts, i) {
			return true
		}
	}
	return false
}
