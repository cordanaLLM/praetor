// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package docsref

import (
	"fmt"
	"regexp"
	"strings"
)

// maxAlternatives bounds the choices read from one "[a|b|c]" token (HISS-02).
const maxAlternatives = 32

// Vocabulary is what one top-level command's code can recognise: every string literal in
// the code its handler reaches, and every flag that code registers.
type Vocabulary struct {
	words map[string]bool
	// flags maps a flag name to whether it takes a value (false only for boolean flags).
	flags map[string]bool
}

// newVocabulary returns an empty vocabulary.
func newVocabulary() *Vocabulary {
	return &Vocabulary{words: map[string]bool{}, flags: map[string]bool{}}
}

// addFlag records a flag; a name registered both as a boolean and with a value takes one.
func (v *Vocabulary) addFlag(name string, takesValue bool) {
	v.flags[name] = v.flags[name] || takesValue
}

// CLIModel is the documented binary's command tree as its code defines it.
type CLIModel struct {
	// commands maps each top-level command name (aliases included) to its vocabulary. A
	// name may map to nil when the command is registered but its handler reaches no code
	// this model reads; such a command's arguments are not checked.
	commands map[string]*Vocabulary
}

// wordPattern is the shape of a subcommand: lower-case letters and digits joined by single
// hyphens or underscores. Anything else in a command line -- a path, a placeholder, a value,
// an upper-case metavariable -- is an operand the code cannot enumerate.
var wordPattern = regexp.MustCompile(`^[a-z][a-z0-9]*(?:[-_][a-z0-9]+)*$`)

// flagPattern is the shape of a flag: one or two dashes, a name, and an optional =value.
var flagPattern = regexp.MustCompile(`^--?([A-Za-z][A-Za-z0-9._-]*)(=.*)?$`)

// helpFlags are answered by Go's flag package itself, whatever a command registers.
var helpFlags = map[string]bool{"h": true, "help": true}

// Check returns one problem per word or flag of call that the command's code does not know.
func (m *CLIModel) Check(call Invocation) []string {
	if len(call.Args) == 0 {
		return nil
	}
	first := call.Args[0]
	if isPlaceholder(first) || !wordPattern.MatchString(first) && !flagPattern.MatchString(first) {
		return nil
	}
	vocabulary, known := m.commands[first]
	if !known {
		return []string{fmt.Sprintf("%q names no command of the CLI (in %q)", first, call.Text)}
	}
	if vocabulary == nil {
		return nil
	}
	return vocabulary.check(call)
}

// check walks the arguments after the command name.
func (v *Vocabulary) check(call Invocation) []string {
	var problems []string
	expectValue := false
	for index := 1; index < len(call.Args) && index < maxTokens; index++ {
		token := call.Args[index]
		if token == "--" {
			break
		}
		if expectValue && !strings.HasPrefix(token, "-") {
			expectValue = false
			continue
		}
		expectValue = false
		for _, choice := range alternatives(token) {
			problem, value := v.checkChoice(choice, call)
			if problem != "" {
				problems = append(problems, problem)
			}
			expectValue = expectValue || value
		}
	}
	return problems
}

// checkChoice checks one word or flag and reports whether the next token is its value.
func (v *Vocabulary) checkChoice(choice string, call Invocation) (string, bool) {
	if match := flagPattern.FindStringSubmatch(choice); match != nil {
		name := match[1]
		if helpFlags[name] {
			return "", false
		}
		takesValue, known := v.flags[name]
		if !known {
			return fmt.Sprintf("command %s has no flag %q (in %q)", call.Args[0], "--"+name, call.Text), false
		}
		return "", takesValue && match[2] == ""
	}
	if wordPattern.MatchString(choice) && !v.words[choice] {
		return fmt.Sprintf("command %s has no subcommand or keyword %q (in %q)", call.Args[0], choice, call.Text), false
	}
	return "", false
}

// alternatives expands one token into the words or flags it offers. "[--json]" offers
// "--json"; "[add|list]" and "<add|list>" offer both words; a bare placeholder such as
// "<file>" or "[path]" offers nothing, because it stands for an operand.
func alternatives(token string) []string {
	bracketed := strings.HasPrefix(token, "[") && strings.HasSuffix(token, "]")
	token = strings.TrimSuffix(strings.TrimPrefix(token, "["), "]")
	if bracketed && !strings.Contains(token, "|") && !strings.HasPrefix(token, "-") {
		return nil
	}
	if strings.HasPrefix(token, "<") && strings.HasSuffix(token, ">") {
		if !strings.Contains(token, "|") {
			return nil
		}
		token = token[1 : len(token)-1]
	}
	if !strings.Contains(token, "|") {
		return []string{token}
	}
	parts := strings.Split(token, "|")
	if len(parts) > maxAlternatives {
		parts = parts[:maxAlternatives]
	}
	return parts
}

// isPlaceholder reports whether token stands for an operand: "<name>", "[name]", "{a,b}",
// "...", or an upper-case metavariable such as PATH.
func isPlaceholder(token string) bool {
	switch {
	case strings.HasPrefix(token, "<") && strings.HasSuffix(token, ">") && len(token) > 2:
		return !strings.Contains(token, "|")
	case strings.HasPrefix(token, "{"), strings.Contains(token, "..."), strings.Contains(token, "…"):
		return true
	}
	return token != "" && strings.ToUpper(token) == token && strings.ToLower(token) != token
}
