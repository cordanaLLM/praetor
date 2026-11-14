// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package agenthook

import (
	"regexp"
	"slices"
	"strings"

	"github.com/cordanaLLM/praetor/internal/contextopt"
	"github.com/cordanaLLM/praetor/internal/hiss"
)

// holdWord preserves the source and quote-removed value without evaluating expansions.
type holdWord struct {
	raw, value string
	literal    bool
}

var holdRedirect = regexp.MustCompile(`^(?:[0-9]+)?(?:&>>|&>|<<<|<<-|<<|>>|<>|>&|<&|>\||>|<)`)

// holdShellCommands retains literal words while sharing heredoc and syntax tracking with HISS.
func holdShellCommands(script string) ([][]holdWord, string) {
	if len(script) > contextopt.MaxSourceBytes {
		return nil, "command exceeds hold byte bound"
	}
	lines, ok := hiss.ShellSource(joinLineContinuations(script), admissionMaxLines)
	if !ok {
		return nil, "command unreadable; split command or write it to file first"
	}
	source := strings.Join(lines, "\n")
	commands := [][]holdWord{{}}
	for i := 0; i < len(source) && len(commands) <= admissionMaxSegments; {
		next, reason := holdShellToken(source, i, &commands)
		if reason != "" {
			return nil, reason
		}
		i = next
	}
	if len(commands) > admissionMaxSegments {
		return nil, "command exceeds hold segment bound"
	}
	return commands, ""
}

// holdShellToken consumes one word, comment, control operator or complete redirection.
func holdShellToken(source string, i int, commands *[][]holdWord) (int, string) {
	switch source[i] {
	case ' ', '\t', '\r':
		return i + 1, ""
	case '\n', ';', '|', '(', ')', '{', '}':
		*commands = append(*commands, nil)
		return i + 1, ""
	case '#':
		end := strings.IndexByte(source[i:], '\n')
		if end < 0 {
			return len(source), ""
		}
		return i + end, ""
	}
	if op := holdRedirect.FindString(source[i:]); op != "" {
		return holdSkipRedirect(source, i, op)
	}
	if source[i] == '&' {
		*commands = append(*commands, nil)
		return i + 1, ""
	}
	word, next, reason := holdReadWord(source, i)
	last := len(*commands) - 1
	(*commands)[last] = append((*commands)[last], word)
	if len((*commands)[last]) > admissionMaxWords {
		reason = "command exceeds hold word bound"
	}
	return next, reason
}

// holdSkipRedirect skips a redirection and its target without mistaking either for a program.
func holdSkipRedirect(source string, i int, op string) (int, string) {
	i += len(op)
	for i < len(source) && strings.ContainsRune(" \t", rune(source[i])) {
		i++
	}
	if i >= len(source) {
		return i, "cannot judge missing redirection target"
	}
	word, next, reason := holdReadWord(source, i)
	if next == i {
		return i, "cannot judge redirection target"
	}
	if strings.Contains(op, "<<") && !strings.Contains(op, "<<<") && !strings.ContainsAny(word.raw, "'\"\\") {
		return next, "cannot judge expanding heredoc " + word.raw
	}
	return next, reason
}

// holdReadWord removes shell quoting and escapes, marking expansion-bearing words indeterminate.
func holdReadWord(source string, start int) (holdWord, int, string) {
	word := holdWord{literal: true}
	var value strings.Builder
	quote := byte(0)
	i := start
	for ; i < len(source); i++ {
		c := source[i]
		if quote == 0 && strings.ContainsRune(" \t\r\n;&|()<>", rune(c)) {
			break
		}
		if c == '\\' && quote != '\'' {
			next, escaped := holdEscape(source, i, quote)
			value.WriteString(escaped)
			i = next
			continue
		}
		if holdQuote(c, &quote) {
			continue
		}
		if reason := holdExpansion(source, i, quote, &word); reason != "" {
			return word, i + 1, reason + " in word " + source[start:]
		}
		value.WriteByte(c)
	}
	word.raw, word.value = source[start:i], value.String()
	if quote != 0 {
		return word, i, "cannot judge unclosed quote " + word.raw
	}
	return word, i, ""
}

// holdQuote consumes quote delimiters, leaving ordinary bytes for literal decoding.
func holdQuote(c byte, quote *byte) bool {
	if *quote != 0 && c == *quote {
		*quote = 0
		return true
	}
	if *quote == 0 && (c == '\'' || c == '"') {
		*quote = c
		return true
	}
	return false
}

// holdEscape applies POSIX backslash removal, including double quotes' restricted escapes.
func holdEscape(source string, i int, quote byte) (int, string) {
	if i+1 >= len(source) {
		return i, "\\"
	}
	next := source[i+1]
	if quote == '"' && !strings.ContainsRune("$`\"\\\n", rune(next)) {
		return i, "\\"
	}
	return i + 1, string(next)
}

// holdExpansion refuses executable substitutions and marks other expansions for command checks.
func holdExpansion(source string, i int, quote byte, word *holdWord) string {
	if quote == '\'' {
		return ""
	}
	c := source[i]
	if c == '`' || strings.HasPrefix(source[i:], "$(") {
		return "cannot judge substitution"
	}
	if c == '$' || (quote == 0 && strings.ContainsRune("*?[~{}@+!", rune(c))) {
		word.literal = false
	}
	return ""
}

// holdPrelude skips assignments and unquoted shell prelude keywords.
func holdPrelude(words []holdWord) int {
	i := 0
	for i < len(words) && i < admissionMaxWords {
		if !assignmentWord.MatchString(words[i].raw) && !slices.Contains(admissionPrelude, words[i].raw) {
			break
		}
		i++
	}
	return i
}

// holdLookup recognizes forms that describe a program without executing it.
func holdLookup(name string, args []holdWord) bool {
	if name == "type" || name == "which" {
		return true
	}
	if name == "command" {
		return holdCommandLookup(args)
	}
	if len(args) == 0 || !args[0].literal {
		return false
	}
	return name == "hash" && args[0].value == "-t"
}

// holdCommandLookup recognizes -v/-V, including command's -p flag and short clusters.
func holdCommandLookup(args []holdWord) bool {
	for i := 0; i < len(args) && i < admissionMaxWords; i++ {
		word := args[i]
		if !word.literal || !strings.HasPrefix(word.value, "-") {
			return false
		}
		flags := strings.TrimPrefix(word.value, "-")
		if flags == "" || strings.Trim(flags, "pvV") != "" {
			return false
		}
		if strings.ContainsAny(flags, "vV") {
			return true
		}
	}
	return false
}

// holdShellPayload recognizes shell -c and eval, requiring exactly one literal script word.
func holdShellPayload(name string, args []holdWord) (*string, bool, string) {
	if name == "eval" {
		payload, reason := holdLiteralPayload(args)
		return payload, true, reason
	}
	if !slices.Contains([]string{"bash", "sh", "zsh", "dash", "ash", "ksh", "mksh"}, name) {
		return nil, false, ""
	}
	return holdShellArguments(args)
}

// holdShellArguments locates a -c payload, refusing options whose interpretation is unclear.
func holdShellArguments(args []holdWord) (*string, bool, string) {
	for i := 0; i < len(args) && i < admissionMaxWords; i++ {
		word := args[i]
		if !word.literal {
			return nil, true, "cannot judge shell argument " + word.raw
		}
		if word.value == "--" {
			return holdShellScript(args[i+1:])
		}
		if !strings.HasPrefix(word.value, "-") {
			if strings.HasPrefix(word.value, "+") {
				return nil, true, "cannot judge shell option " + word.raw
			}
			return nil, false, ""
		}
		if !strings.HasPrefix(word.value, "--") && strings.Contains(word.value, "c") {
			payload, reason := holdLiteralPayload(args[i+1 : min(i+2, len(args))])
			return payload, true, reason
		}
		if !holdShellFlags(word.value) {
			return nil, true, "cannot judge shell option " + word.raw
		}
	}
	return nil, true, "cannot judge shell input payload"
}

// holdShellScript requires a literal script operand after the shell option terminator.
func holdShellScript(args []holdWord) (*string, bool, string) {
	if len(args) == 0 {
		return nil, true, "cannot judge shell input payload"
	}
	if !args[0].literal {
		return nil, true, "cannot judge shell script " + args[0].raw
	}
	return nil, false, ""
}

// holdShellFlags permits only unambiguous no-value flags before a shell script or -c payload.
func holdShellFlags(word string) bool {
	if strings.HasPrefix(word, "--") {
		return word == "--noprofile" || word == "--norc"
	}
	return strings.Trim(word[1:], "abefhkmnptuvxBCEHPTl") == ""
}

// holdLiteralPayload extracts one literal word, refusing dynamic or concatenated payloads.
func holdLiteralPayload(args []holdWord) (*string, string) {
	if len(args) != 1 {
		return nil, "cannot judge shell payload: require one literal word"
	}
	if !args[0].literal {
		return nil, "cannot judge shell payload " + args[0].raw
	}
	return &args[0].value, ""
}
