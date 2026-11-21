// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package agenthook

import (
	"regexp"
	"slices"
	"strings"

	"github.com/cordanaLLM/praetor/internal/forge"
	"github.com/cordanaLLM/praetor/internal/hiss"
)

// readBackgroundPayloads adapts existing shell fields and wrapper traversal to the
// shellPayloadReader boundary. PR 1958's hold walker replaces this adapter on convergence.
func readBackgroundPayloads(source string) ([]backgroundScript, string) {
	fields, err := forge.ShellFields(source)
	if err != nil {
		return nil, "command unreadable: shell payload words"
	}
	var payloads []backgroundScript
	start := 0
	for i := 0; i <= len(fields) && i <= admissionMaxSegments*admissionMaxWords; i++ {
		if i < len(fields) && !targetCommandSeparator(fields[i]) {
			continue
		}
		payload, reason := backgroundPayload(fields[start:i])
		if reason != "" {
			return nil, reason
		}
		if payload != nil {
			payloads = append(payloads, *payload)
		}
		start = i + 1
	}
	return payloads, ""
}

var backgroundRedirection = regexp.MustCompile(`^[0-9]*[<>]`)

// backgroundPayload reuses admission's bounded wrapper traversal, with literal decoding.
// Unknown command names are data for this rule; executable shell input must be static.
func backgroundPayload(words []string) (*backgroundScript, string) {
	if len(words) > admissionMaxWords {
		return nil, "command unreadable: background word bound"
	}
	values := backgroundWordValues(words)
	i := skipPrelude(words)
	for depth := 0; i < len(words) && depth < admissionMaxWrappers; depth++ {
		name, reason := backgroundCommandName(words[i:])
		if reason != "" {
			return nil, reason
		}
		if backgroundLookup(name, values[i+1:]) {
			return nil, ""
		}

		next, wrapper, reason := backgroundWrapperTarget(name, words, values, i)
		if reason != "" {
			return nil, reason
		}
		if wrapper {
			i = next
			continue
		}

		return backgroundInterpreter(name, words[i+1:])
	}
	if i < len(words) {
		return nil, "command unreadable: background wrapper bound"
	}
	return nil, ""
}

// backgroundInterpreter accepts one literal eval argument or a shell -c payload.
// Script files remain outside this command-line rule; stdin shells fail closed.
func backgroundInterpreter(name string, args []string) (*backgroundScript, string) {
	if name == "eval" {
		return backgroundLiteral(name, args)
	}
	if !slices.Contains([]string{"sh", "bash", "zsh", "dash", "ash", "ksh", "mksh"}, name) {
		return nil, ""
	}
	if backgroundShellRedirection(args) {
		return nil, "command unreadable: ambiguous shell redirection"
	}
	return backgroundShellArguments(name, args)
}

// backgroundShellArguments locates -c only among unambiguous no-value shell options.
func backgroundShellArguments(name string, args []string) (*backgroundScript, string) {
	for i := 0; i < len(args) && i < admissionMaxWords; i++ {
		if !backgroundLiteralWord(args[i]) {
			return nil, "command unreadable: ambiguous shell argument"
		}
		word := unquoteShellWord(args[i])
		if word == "--" {
			return backgroundScriptOperand(args[i+1:])
		}
		if !strings.HasPrefix(word, "-") {
			return backgroundScriptOperand(args[i:])
		}
		payload, valid := backgroundShellOption(word)
		if !valid {
			return nil, "command unreadable: ambiguous shell option"
		}
		if payload {
			return backgroundLiteral(name, args[i+1:min(i+2, len(args))])
		}
	}
	return nil, "command unreadable: shell stdin payload"
}

// backgroundShellOption refuses unknown or value-bearing options before a payload.
func backgroundShellOption(word string) (bool, bool) {
	if strings.HasPrefix(word, "--") {
		return false, slices.Contains([]string{"--norc", "--noprofile"}, word)
	}
	return strings.Contains(word, "c"), strings.Trim(word[1:], "abcefhkmnptuvxBCEHPTl") == ""
}

// backgroundCommandName returns the command name of a payload's first word, or an empty name and a refusal
// reason when the word is an ambiguous leading redirection or an executable word bash would expand.
func backgroundCommandName(words []string) (string, string) {
	raw := words[0]
	if len(words) > 1 && strings.Trim(raw, "0123456789") == "" && backgroundRedirection.MatchString(words[1]) {
		return "", "command unreadable: ambiguous leading redirection"
	}
	if !backgroundLiteralWord(raw) || backgroundBraceExpansion(raw) {
		return "", "command unreadable: ambiguous executable word"
	}
	name := unquoteShellWord(raw)
	if backgroundRedirection.MatchString(name) {
		return "", "command unreadable: ambiguous leading redirection"
	}
	return commandName(name), ""
}

// backgroundBraceExpansion refuses unquoted list or sequence syntax in executable words.
// Argument words retain their current handling; quoting and escapes make braces literal.
func backgroundBraceExpansion(word string) bool {
	var quotes hiss.QuoteStepper
	var braces backgroundBraces
	for i := 0; i < len(word); i++ {
		if !quotes.InQuote() && braces.step(word[i:]) {
			return true
		}
		i = quotes.Step(word, i)
	}
	return false
}

type backgroundBraces struct {
	depth, marked int
}

// step recognizes expansion syntax only once an unquoted closing brace balances its opener.
// Depth is bounded by the caller's word length; no brace expansion is evaluated.
func (b *backgroundBraces) step(rest string) bool {
	switch rest[0] {
	case '{':
		b.depth++
	case ',', '.':
		if b.depth > 0 && (rest[0] == ',' || strings.HasPrefix(rest, "..")) {
			b.marked = b.depth
		}
	case '}':
		if b.depth > 0 {
			if b.marked == b.depth {
				return true
			}
			b.depth--
		}
	}
	return false
}

func backgroundLookup(name string, args []string) bool {
	return name == "command" && len(args) > 0 && slices.Contains([]string{"-v", "-V"}, args[0])
}

func backgroundScriptOperand(args []string) (*backgroundScript, string) {
	if len(args) == 0 || !backgroundLiteralWord(args[0]) || strings.HasPrefix(unquoteShellWord(args[0]), "+") || backgroundRedirection.MatchString(args[0]) {
		return nil, "command unreadable: ambiguous shell input"
	}
	return nil, ""
}

// backgroundLiteral refuses argument concatenation and expansion rather than guessing eval.
func backgroundLiteral(shell string, args []string) (*backgroundScript, string) {
	if len(args) != 1 || !backgroundLiteralWord(args[0]) {
		return nil, "command unreadable: ambiguous shell payload; require one literal word"
	}
	return &backgroundScript{source: unquoteShellWord(args[0]), shell: shell}, ""
}

// backgroundLiteralWord shares quote/escape tracking without evaluating expansions.
// ANSI/locale quotes require shell-specific decoding and therefore fail closed here.
func backgroundLiteralWord(word string) bool {
	var quotes hiss.QuoteStepper
	for i := 0; i < len(word); i++ {
		if quotes.OpenQuote() != '\'' && strings.ContainsRune("$`", rune(word[i])) {
			return false
		}
		if !quotes.InQuote() && strings.ContainsRune("*?[~", rune(word[i])) {
			return false
		}
		i = quotes.Step(word, i)
	}
	return !quotes.Open() && !quotes.ExceededLimit()
}

// backgroundWrapperTarget shares admission's traversal and refuses string-reparsing wrappers.
// PR 1958 convergence replaces this conservative adapter with the common payload walker.
func backgroundWrapperTarget(name string, words, values []string, i int) (int, bool, string) {
	if next, admit := admitStep(name, values, i); admit {
		if next < 0 {
			return 0, true, "command unreadable: ambiguous admission payload; require --"
		}
		return next, true, ""
	}
	spec, wrapper := admissionWrappers[name]
	if !wrapper {
		return i, false, ""
	}
	next := commandAfter(values, i, spec)
	if backgroundWrapperString(name, words[i+1:min(next, len(words))]) {
		return 0, true, "command unreadable: ambiguous wrapper shell payload"
	}
	if reason := backgroundWrapperOptions(name, words, values, i+1, next, spec); reason != "" {
		return 0, true, reason
	}
	return next, true, ""
}

func backgroundWrapperString(name string, args []string) bool {
	var short, long string
	switch name {
	case "env":
		short, long = "S", "--split-string"
	case "flock":
		short, long = "c", "--command"
	default:
		return false
	}
	for i := 0; i < len(args) && i < admissionMaxWords; i++ {
		if backgroundStringOption(unquoteShellWord(args[i]), short, long) {
			return true
		}
	}
	return false
}

func backgroundStringOption(word, short, long string) bool {
	if strings.HasPrefix(word, "--") {
		return strings.HasPrefix(word, long)
	}
	return strings.HasPrefix(word, "-") && strings.Contains(word, short)
}

func backgroundShellRedirection(args []string) bool {
	for i := 0; i < len(args) && i < admissionMaxWords; i++ {
		if backgroundRedirection.MatchString(args[i]) {
			return true
		}
	}
	return false
}

// backgroundWrapperOptions validates only consumed wrapper options, reusing admission's
// value/terminator reader. Unknown value-bearing flags cannot shift the executable word.
func backgroundWrapperOptions(name string, words, values []string, start, end int, spec wrapperSpec) string {
	if !backgroundWrapperLiterals(words[start:min(end, len(words))]) {
		return "command unreadable: ambiguous wrapper argument"
	}
	for i := start; i < end && i < len(words) && i < admissionMaxWords; i++ {
		next, done, option := advanceOptionWord(values[i], i, spec)
		if done {
			return ""
		}
		if !option {
			continue
		}
		if next > i {
			i = next
			continue
		}
		if backgroundWrapperFlag(name, values[i], spec.values) {
			continue
		}
		return "command unreadable: ambiguous wrapper option"
	}
	return ""
}

func backgroundWrapperFlag(name, word string, values []string) bool {
	key, _, attached := strings.Cut(word, "=")
	if attached && slices.Contains(values, key) {
		return true
	}
	switch name {
	case "env":
		return slices.Contains([]string{"-i", "--ignore-environment", "-v", "--debug"}, word)
	case "command":
		return word == "-p"
	case "exec":
		return word == "-c" || word == "-l"
	case "systemd-run":
		return word == "--user" || word == "--scope"
	default:
		return false
	}
}

// backgroundWordValues supplies commandAfter/admitStep their actual literal argument values.
// Raw words remain separate for expansion checks and payload decoding; quoting never turns
// an option or env assignment into an opaque executable word.
func backgroundWordValues(words []string) []string {
	values := make([]string, len(words))
	for i := 0; i < len(words) && i < admissionMaxWords; i++ {
		values[i] = unquoteShellWord(words[i])
	}
	return values
}

// backgroundWrapperLiterals checks values too, including words skipped as option operands.
func backgroundWrapperLiterals(words []string) bool {
	for i := 0; i < len(words) && i < admissionMaxWords; i++ {
		if !backgroundLiteralWord(words[i]) {
			return false
		}
	}
	return true
}
