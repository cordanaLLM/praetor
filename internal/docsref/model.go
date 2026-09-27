// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package docsref

import (
	"context"
	"fmt"
	"maps"
	"regexp"
	"slices"
	"strings"
)

// maxAlternatives bounds the choices read from one "[a|b|c]" token (HISS-02).
const maxAlternatives = 32

// CLIModel is the documented binary's command tree as its code defines it: the top-level
// commands of the dispatch table, and, below each, the subcommands and flags its handler's
// code dispatches on and registers.
type CLIModel struct {
	tree *sourceTree
	dir  string
	// commands maps each top-level command name (aliases included) to its handler function
	// in dir. An empty handler marks a registered command whose arguments are not checked.
	commands map[string]string
	flagMemo map[string]flagTable
}

// wordPattern is the shape of a subcommand: lower-case letters and digits joined by single
// hyphens or underscores. Anything else in a command line -- a path, a placeholder, a value,
// an upper-case metavariable -- is an operand the code cannot enumerate.
var wordPattern = regexp.MustCompile(`^[a-z][a-z0-9]*(?:[-_][a-z0-9]+)*$`)

// flagPattern is the shape of a flag: one or two dashes, a name, and an optional =value.
var flagPattern = regexp.MustCompile(`^--?([A-Za-z][A-Za-z0-9._-]*)(=.*)?$`)

// helpFlags are answered by Go's flag package itself, whatever a command registers.
var helpFlags = map[string]bool{"h": true, "help": true}

// pathWalk is one invocation followed down the command tree.
type pathWalk struct {
	words    []string
	nodes    []string
	chosen   []map[string]bool
	flags    []string
	problems []string
	leaf     bool
}

// Check returns one problem per word or flag of call that the command's code does not
// define. Subcommand words are checked only as deep as the code dispatches: below a leaf,
// every word is an operand (a package name, a task description, an agent name) and passes.
func (m *CLIModel) Check(ctx context.Context, call Invocation) ([]string, error) {
	if len(call.Args) == 0 {
		return nil, nil
	}
	first := call.Args[0]
	if isPlaceholder(first) || !wordPattern.MatchString(first) && !flagPattern.MatchString(first) {
		return nil, nil
	}
	handler, known := m.commands[first]
	if !known {
		return []string{fmt.Sprintf("%q names no command of the CLI (in %q)", first, call.Text)}, nil
	}
	if handler == "" {
		return nil, nil
	}
	walk := &pathWalk{words: []string{first}, nodes: []string{handler}, chosen: []map[string]bool{nil}}
	if err := m.follow(ctx, call, walk); err != nil {
		return nil, err
	}
	if err := m.checkFlags(ctx, call, walk); err != nil {
		return nil, err
	}
	return walk.problems, nil
}

// otherCommands returns the handlers of every top-level command except the one a walk
// starts from. A flag walk never enters them: code that reads the dispatch table (the docs
// references command itself does) would otherwise reach every command's flags.
func (m *CLIModel) otherCommands(handler string) map[string]bool {
	stops := map[string]bool{}
	for _, other := range m.commands {
		if other != "" && other != handler {
			stops[other] = true
		}
	}
	return stops
}

// follow walks the arguments after the command name, descending one subcommand per word.
func (m *CLIModel) follow(ctx context.Context, call Invocation, walk *pathWalk) error {
	command, err := m.flagsFor(ctx, walk.nodes[0], m.otherCommands(walk.nodes[0]))
	if err != nil {
		return err
	}
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
		var words []string
		words, expectValue = walk.classify(token, command)
		if err := m.descend(ctx, call, walk, words); err != nil {
			return err
		}
	}
	return nil
}

// classify splits one token's choices into flags, recorded on the walk, and subcommand
// words, returned; it reports whether the next token is the value of a flag in this one.
func (walk *pathWalk) classify(token string, command flagTable) (words []string, expectValue bool) {
	for _, choice := range alternatives(token) {
		if match := flagPattern.FindStringSubmatch(choice); match != nil {
			walk.flags = append(walk.flags, choice)
			expectValue = expectValue || command[match[1]] && match[2] == ""
		} else if wordPattern.MatchString(choice) {
			words = append(words, choice)
		}
	}
	return words, expectValue
}

// descend checks the words of one token against the subcommands of the current node and,
// when exactly one word names a subcommand with a handler, moves the walk below it.
func (m *CLIModel) descend(ctx context.Context, call Invocation, walk *pathWalk, words []string) error {
	if walk.leaf || len(words) == 0 {
		return nil
	}
	current := len(walk.nodes) - 1
	children, err := m.tree.children(ctx, m.dir, walk.nodes[current])
	if err != nil {
		return err
	}
	if len(children) == 0 {
		walk.leaf = true
		return nil
	}
	walk.chosen[current] = map[string]bool{}
	for _, word := range words {
		handler, known := children[word]
		if !known {
			walk.problems = append(walk.problems, fmt.Sprintf("command %s has no subcommand %q (in %q)",
				strings.Join(walk.words, " "), word, call.Text))
			walk.leaf = true
			return nil
		}
		walk.chosen[current][handler] = true
	}
	walk.leaf = true
	if len(words) == 1 && children[words[0]] != "" {
		walk.words = append(walk.words, words[0])
		walk.nodes = append(walk.nodes, children[words[0]])
		walk.chosen = append(walk.chosen, nil)
		walk.leaf = false
		return m.reread(ctx, walk, words[0])
	}
	return nil
}

// reread handles a subcommand handler that is handed the whole list, its own word included
// (runDogfoodRepairAction(ctx, args) under "repairs run"): when the new node dispatches on the
// word it was reached by, that word is consumed there too instead of the next operand being
// checked against the node's subcommands.
func (m *CLIModel) reread(ctx context.Context, walk *pathWalk, word string) error {
	current := len(walk.nodes) - 1
	children, err := m.tree.children(ctx, m.dir, walk.nodes[current])
	if err != nil {
		return err
	}
	handler, again := children[word]
	if !again {
		return nil
	}
	walk.chosen[current] = map[string]bool{handler: true}
	walk.leaf = true
	if handler != "" && handler != walk.nodes[current] {
		walk.nodes = append(walk.nodes, handler)
		walk.chosen = append(walk.chosen, nil)
		walk.leaf = false
	}
	return nil
}

// checkFlags checks every flag of the call against the flags of the path it reached.
func (m *CLIModel) checkFlags(ctx context.Context, call Invocation, walk *pathWalk) error {
	if len(walk.flags) == 0 {
		return nil
	}
	valid, err := m.pathFlags(ctx, walk)
	if err != nil {
		return err
	}
	for _, flag := range walk.flags {
		name := flagPattern.FindStringSubmatch(flag)[1]
		if _, known := valid[name]; !known && !helpFlags[name] {
			walk.problems = append(walk.problems, fmt.Sprintf("command %s has no flag %q (in %q)",
				strings.Join(walk.words, " "), "--"+name, call.Text))
		}
	}
	return nil
}

// pathFlags returns the flags of every node on the walk's path: each node's own code, without
// the code of the subcommands the call did not choose or of any other top-level command.
func (m *CLIModel) pathFlags(ctx context.Context, walk *pathWalk) (flagTable, error) {
	valid := flagTable{}
	for index, node := range walk.nodes {
		children, err := m.tree.children(ctx, m.dir, node)
		if err != nil {
			return nil, err
		}
		stops := m.otherCommands(walk.nodes[0])
		for _, handler := range children {
			if handler != "" && !walk.chosen[index][handler] {
				stops[handler] = true
			}
		}
		table, err := m.flagsFor(ctx, node, stops)
		if err != nil {
			return nil, err
		}
		maps.Copy(valid, table)
	}
	return valid, nil
}

// flagsFor returns the memoised flags of one function walked without entering stops.
func (m *CLIModel) flagsFor(ctx context.Context, function string, stops map[string]bool) (flagTable, error) {
	key := function + "|" + strings.Join(slices.Sorted(maps.Keys(stops)), ",")
	if table, ok := m.flagMemo[key]; ok {
		return table, nil
	}
	table, err := m.tree.flags(ctx, m.dir, function, stops)
	if err != nil {
		return nil, err
	}
	m.flagMemo[key] = table
	return table, nil
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
