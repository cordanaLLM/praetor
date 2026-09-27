// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package docsref

import (
	"path"
	"regexp"
	"strings"

	"github.com/cordanaLLM/praetor/internal/util"
)

// maxTokens bounds the tokens read from one candidate (HISS-02).
const maxTokens = 512

// Invocation is one documented call of the Praetor CLI.
type Invocation struct {
	// Args are the tokens after the binary name, up to the end of the simple command.
	Args []string
	// Text is the invocation as written, for findings.
	Text string
}

// envAssignment matches a leading NAME=value word of a shell simple command.
var envAssignment = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*=`)

// commandWrappers run the command that follows them; the word after one is still in command
// position.
var commandWrappers = map[string]bool{"sudo": true, "time": true, "exec": true, "command": true, "env": true}

// commandSeparators end one shell simple command and begin the next.
var commandSeparators = map[string]bool{"&&": true, "||": true, "|": true, ";": true, "&": true, "then": true, "do": true, "else": true}

// Invocations returns every Praetor CLI call in one candidate: the binary under either of its
// names, bare or by path, or `go run ./<sourcePackage>`, where sourcePackage is the
// repository-relative directory of the CLI's main package. A call is recognised only in
// command position -- the first word of a simple command, after environment assignments and
// wrappers -- so a sentence or a quoted log line that merely mentions the binary is not read
// as one.
func Invocations(candidate Candidate, sourcePackage string) []Invocation {
	tokens := tokenize(candidate.Text)
	var calls []Invocation
	for start := 0; start < len(tokens) && start < maxTokens; {
		end := simpleCommandEnd(tokens, start)
		if call, ok := invocation(tokens[start:end], sourcePackage); ok {
			calls = append(calls, call)
		}
		start = end + 1
	}
	return calls
}

// simpleCommandEnd returns the index of the separator ending the command at start, or the
// token count when the command runs to the end.
func simpleCommandEnd(tokens []string, start int) int {
	for index := start; index < len(tokens) && index < maxTokens; index++ {
		if commandSeparators[tokens[index]] {
			return index
		}
	}
	return len(tokens)
}

// invocation reads one simple command and reports whether it calls the Praetor CLI.
func invocation(words []string, sourcePackage string) (Invocation, bool) {
	index := 0
	for index < len(words) && (envAssignment.MatchString(words[index]) || commandWrappers[words[index]]) {
		index++
	}
	if index >= len(words) {
		return Invocation{}, false
	}
	var head int
	switch {
	case isBinaryWord(words[index]):
		head = 1
	case words[index] == "go" && index+2 < len(words) && words[index+1] == "run" && isSourcePackage(words[index+2], sourcePackage):
		head = 3
	default:
		return Invocation{}, false
	}
	args := commandArguments(words[index+head:])
	return Invocation{Args: args, Text: strings.Join(words[index:index+head+len(args)], " ")}, true
}

// isBinaryWord reports whether word runs one of the binary's two names, bare or by path.
func isBinaryWord(word string) bool {
	base := path.Base(strings.ReplaceAll(strings.Trim(word, `"'`), `\`, "/"))
	base = strings.TrimSuffix(base, ".exe")
	return base == util.PraetorCLI || base == util.LegacyCLI
}

// isSourcePackage reports whether word names the CLI's Go package, as `go run` accepts it:
// ./cmd/standardsctl, or a module path ending in /cmd/standardsctl with an optional @version.
func isSourcePackage(word, sourcePackage string) bool {
	word = strings.TrimSuffix(strings.SplitN(word, "@", 2)[0], "/")
	return sourcePackage != "" && (word == "./"+sourcePackage || strings.HasSuffix(word, "/"+sourcePackage))
}

// commandArguments cuts the argument list at a comment or a redirection.
func commandArguments(args []string) []string {
	for index := 0; index < len(args) && index < maxTokens; index++ {
		word := args[index]
		if strings.HasPrefix(word, "#") || isRedirection(word) {
			return args[:index]
		}
	}
	return args
}

// isRedirection reports whether word redirects a stream (">", "2>&1", ">/dev/null", "<").
// A placeholder such as "<file>" is not one.
func isRedirection(word string) bool {
	if isPlaceholder(word) {
		return false
	}
	trimmed := strings.TrimLeft(word, "0123456789")
	return strings.HasPrefix(trimmed, ">") || strings.HasPrefix(trimmed, "<")
}

// tokenize splits shell-like text into words. Quotes group a word and stay part of it, so a
// quoted value is recognisable as one; separators glued to a word are split off it.
func tokenize(text string) []string {
	var tokens []string
	var current strings.Builder
	quote := byte(0)
	for index := 0; index < len(text) && len(tokens) < maxTokens; index++ {
		char := text[index]
		if quote != 0 || char == '"' || char == '\'' {
			quote = quoteState(quote, char)
			current.WriteByte(char)
			continue
		}
		switch strings.IndexByte(" \t;()`", char) {
		case -1:
			current.WriteByte(char)
		case 0, 1:
			tokens = appendToken(tokens, &current)
		default:
			tokens = append(appendToken(tokens, &current), ";")
		}
	}
	return appendToken(tokens, &current)
}

// quoteState returns the quote open after char: char opens a quote when none is open and
// closes the open one when it matches.
func quoteState(open, char byte) byte {
	switch {
	case open == 0:
		return char
	case char == open:
		return 0
	}
	return open
}

// appendToken moves a finished word onto tokens.
func appendToken(tokens []string, current *strings.Builder) []string {
	if current.Len() == 0 {
		return tokens
	}
	tokens = append(tokens, current.String())
	current.Reset()
	return tokens
}
