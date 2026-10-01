// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package supplychain

import (
	"slices"
	"strings"
)

// REUSE 3.3 defines its own glob dialect for the path key of REUSE.toml, and the reuse tool
// translates it character by character (reuse/global_licensing.py, AnnotationsItem._paths_regex):
// "*" matches everything but "/", "**" and "**/" match everything, "/" included and anywhere in
// the glob, and a backslash makes the next character literal. "?" and "[" are literal.
// util.MatchGlobSegments reads path.Match segments instead, where "**" spans whole segments
// only and "?" and "[" are patterns, so "**.md" or "file[1].txt" would match other files than
// REUSE applies the table to.

// The kinds of a reuseGlobToken.
const (
	// reuseGlobLiteral matches its one byte.
	reuseGlobLiteral = iota
	// reuseGlobStar matches any run of bytes without "/".
	reuseGlobStar
	// reuseGlobstar matches any run of bytes.
	reuseGlobstar
)

// reuseGlobToken is one token of a REUSE.toml path glob.
type reuseGlobToken struct {
	kind int
	char byte
}

// reuseGlobRelation reports whether glob matches every file subject names (covers) and at least
// one of them (overlaps). subject is a file path, matched in full, or a prefix followed by "**",
// naming every file whose path continues the prefix: "<dir>/**" names the files below dir.
func reuseGlobRelation(glob, subject string) (covers, overlaps bool) {
	tokens := reuseGlobTokens(glob)
	prefix, directory := strings.CutSuffix(subject, "**")
	if !directory {
		matched := reuseGlobStates(tokens, subject)[len(tokens)]
		return matched, matched
	}
	states := reuseGlobStates(tokens, prefix)
	for position := range tokens {
		if states[position] {
			overlaps = true
			covers = covers || !slices.ContainsFunc(tokens[position:], func(token reuseGlobToken) bool { return token.kind != reuseGlobstar })
		}
	}
	return covers, overlaps
}

// reuseGlobTokens splits glob into its tokens, one for each literal byte or star and one for each
// run of two or more asterisks together with the "/" that follows it.
func reuseGlobTokens(glob string) []reuseGlobToken {
	tokens := make([]reuseGlobToken, 0, len(glob))
	// Every token spans at least one byte, so len(glob) passes read the whole glob.
	for index := 0; index < len(glob); {
		token, width := reuseGlobNext(glob[index:])
		tokens = append(tokens, token)
		index += width
	}
	return tokens
}

// reuseGlobNext returns the token rest opens and the bytes it spans; rest is not empty.
func reuseGlobNext(rest string) (reuseGlobToken, int) {
	switch {
	case rest[0] == '\\' && len(rest) > 1:
		return reuseGlobToken{kind: reuseGlobLiteral, char: rest[1]}, 2
	case rest[0] != '*':
		return reuseGlobToken{kind: reuseGlobLiteral, char: rest[0]}, 1
	case !strings.HasPrefix(rest, "**"):
		return reuseGlobToken{kind: reuseGlobStar}, 1
	}
	width := len(rest) - len(strings.TrimLeft(rest, "*"))
	if strings.HasPrefix(rest[width:], "/") {
		width++
	}
	return reuseGlobToken{kind: reuseGlobstar}, width
}

// reuseGlobStates runs tokens over text as a nondeterministic automaton and returns, for each
// position in tokens, whether the tokens before it can match all of text. The run takes
// len(text) passes over the tokens (HISS-02).
func reuseGlobStates(tokens []reuseGlobToken, text string) []bool {
	states := make([]bool, len(tokens)+1)
	states[0] = true
	reuseGlobClose(tokens, states)
	for index := 0; index < len(text); index++ {
		next := make([]bool, len(tokens)+1)
		for position, token := range tokens {
			if !states[position] {
				continue
			}
			switch {
			case token.kind == reuseGlobstar, token.kind == reuseGlobStar && text[index] != '/':
				next[position] = true
			case token.kind == reuseGlobLiteral && token.char == text[index]:
				next[position+1] = true
			}
		}
		reuseGlobClose(tokens, next)
		states = next
	}
	return states
}

// reuseGlobClose marks every position that a marked one reaches by matching stars to nothing.
func reuseGlobClose(tokens []reuseGlobToken, states []bool) {
	for position, token := range tokens {
		states[position+1] = states[position+1] || (states[position] && token.kind != reuseGlobLiteral)
	}
}
