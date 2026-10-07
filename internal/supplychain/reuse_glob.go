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
	states := reuseGlobStart(tokens)
	for index := 0; index < len(text); index++ {
		states = reuseGlobStep(tokens, states, text[index])
	}
	return states
}

// reuseGlobStart returns the states of tokens before any byte is read.
func reuseGlobStart(tokens []reuseGlobToken) []bool {
	states := make([]bool, len(tokens)+1)
	states[0] = true
	reuseGlobClose(tokens, states)
	return states
}

// reuseGlobStep returns the states tokens reach from states by reading one byte.
func reuseGlobStep(tokens []reuseGlobToken, states []bool, char byte) []bool {
	next := make([]bool, len(tokens)+1)
	for position, token := range tokens {
		if !states[position] {
			continue
		}
		switch {
		case token.kind == reuseGlobstar, token.kind == reuseGlobStar && char != '/':
			next[position] = true
		case token.kind == reuseGlobLiteral && token.char == char:
			next[position+1] = true
		}
	}
	reuseGlobClose(tokens, next)
	return next
}

// maxReuseGlobPairs bounds the product states one reuseGlobIncludes explores (HISS-02). The globs
// of a REUSE.toml reach a few dozen; one past the bound is answered "not included", which only
// ever withholds a finding.
const maxReuseGlobPairs = 1 << 14

// reuseGlobPair is one state of the product of an inner glob's automaton and the union of outer
// globs' automata: the states each reaches after one path prefix.
type reuseGlobPair struct {
	inner []bool
	outer [][]bool
}

// reuseGlobProduct is the product reuseGlobIncludes explores: one inner glob, the outer globs
// whose union it is compared with, and the bytes that tell their paths apart.
type reuseGlobProduct struct {
	inner    []reuseGlobToken
	outers   [][]reuseGlobToken
	alphabet []byte
}

// reuseGlobIncludes reports whether the outers together match every path inner matches, so later
// tables with outers relabel every file of inner: one glob such as "**", or a union such as
// "*", ".*", "*/**" and ".*/**", which no one of its globs covers alone. It explores the product of
// inner's automaton and every outer's, their state sets after each path prefix, breadth first,
// and looks for a prefix inner accepts and no outer does. A prefix after which inner matches
// nothing more, or an outer matches every continuation, leads to no such path and is not
// explored. The
// paths read only bytes the globs name as literals, "/" and one other byte that stands for every
// byte none names, which every star treats alike. A product past maxReuseGlobPairs is "not
// included".
func reuseGlobIncludes(outers []string, inner string) bool {
	product := newReuseGlobProduct(outers, inner)
	start := reuseGlobPair{inner: reuseGlobStart(product.inner), outer: reuseGlobUnionStart(product.outers)}
	if product.escapes(start) {
		return false
	}
	queue := []reuseGlobPair{start}
	seen := map[string]bool{reuseGlobPairKey(start): true}
	for len(queue) > 0 && len(seen) <= maxReuseGlobPairs {
		next, escaped := product.expand(queue[0])
		if escaped {
			return false
		}
		queue = queue[1:]
		for _, pair := range next {
			if key := reuseGlobPairKey(pair); !seen[key] {
				seen[key] = true
				queue = append(queue, pair)
			}
		}
	}
	return len(queue) == 0
}

// newReuseGlobProduct is the product of inner with the union of outers.
func newReuseGlobProduct(outers []string, inner string) reuseGlobProduct {
	product := reuseGlobProduct{inner: reuseGlobTokens(inner)}
	for _, outer := range outers {
		product.outers = append(product.outers, reuseGlobTokens(outer))
	}
	product.alphabet = reuseGlobAlphabet(append([][]reuseGlobToken{product.inner}, product.outers...)...)
	return product
}

// expand returns the open pairs that one more byte after pair leads to, and whether one of them
// escapes. A byte after which inner matches nothing more leads nowhere.
func (p reuseGlobProduct) expand(pair reuseGlobPair) (next []reuseGlobPair, escaped bool) {
	for _, char := range p.alphabet {
		inner := reuseGlobStep(p.inner, pair.inner, char)
		if !slices.Contains(inner, true) {
			continue
		}
		candidate := reuseGlobPair{inner: inner, outer: reuseGlobUnionStep(p.outers, pair.outer, char)}
		if p.escapes(candidate) {
			return nil, true
		}
		if p.open(candidate) {
			next = append(next, candidate)
		}
	}
	return next, false
}

// escapes reports whether the prefix that reached pair is a path inner matches and no outer does.
func (p reuseGlobProduct) escapes(pair reuseGlobPair) bool {
	return pair.inner[len(p.inner)] && !reuseGlobUnionAccepts(p.outers, pair.outer)
}

// open reports whether a longer prefix than the one that reached pair may still escape: no outer
// already matches every continuation.
func (p reuseGlobProduct) open(pair reuseGlobPair) bool {
	for index, tokens := range p.outers {
		for position := range tokens {
			if pair.outer[index][position] && !slices.ContainsFunc(tokens[position:], func(token reuseGlobToken) bool { return token.kind != reuseGlobstar }) {
				return false
			}
		}
	}
	return true
}

// reuseGlobUnionStart returns the states of every glob of globs before any byte is read.
func reuseGlobUnionStart(globs [][]reuseGlobToken) [][]bool {
	states := make([][]bool, len(globs))
	for index, tokens := range globs {
		states[index] = reuseGlobStart(tokens)
	}
	return states
}

// reuseGlobUnionStep returns the states every glob of globs reaches from states by reading char.
func reuseGlobUnionStep(globs [][]reuseGlobToken, states [][]bool, char byte) [][]bool {
	next := make([][]bool, len(globs))
	for index, tokens := range globs {
		next[index] = reuseGlobStep(tokens, states[index], char)
	}
	return next
}

// reuseGlobUnionAccepts reports whether one glob of globs matches the path its states read.
func reuseGlobUnionAccepts(globs [][]reuseGlobToken, states [][]bool) bool {
	for index, tokens := range globs {
		if states[index][len(tokens)] {
			return true
		}
	}
	return false
}

// reuseGlobAlphabet returns the bytes that tell paths apart for globs: one byte no glob names,
// first, since a path no later glob names is the usual escape, then "/" and every literal byte of
// the globs.
func reuseGlobAlphabet(globs ...[]reuseGlobToken) []byte {
	named := map[byte]bool{'/': true}
	for _, tokens := range globs {
		for _, token := range tokens {
			if token.kind == reuseGlobLiteral {
				named[token.char] = true
			}
		}
	}
	alphabet := make([]byte, 0, len(named)+1)
	other := byte(0)
	for char := 0; char < 256; char++ {
		if named[byte(char)] {
			alphabet = append(alphabet, byte(char))
		} else if other == 0 && char != 0 {
			other = byte(char)
		}
	}
	return append([]byte{other}, alphabet...)
}

// reuseGlobPairKey identifies a product state, one byte per automaton state and one separator
// after each automaton.
func reuseGlobPairKey(pair reuseGlobPair) string {
	key := make([]byte, 0, len(pair.inner)+1)
	for _, states := range append([][]bool{pair.inner}, pair.outer...) {
		for _, state := range states {
			mark := byte('0')
			if state {
				mark = '1'
			}
			key = append(key, mark)
		}
		key = append(key, '|')
	}
	return string(key)
}

// reuseGlobClose marks every position that a marked one reaches by matching stars to nothing.
func reuseGlobClose(tokens []reuseGlobToken, states []bool) {
	for position, token := range tokens {
		states[position+1] = states[position+1] || (states[position] && token.kind != reuseGlobLiteral)
	}
}
