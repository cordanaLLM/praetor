// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package supplychain

import (
	"errors"
	"fmt"
	"slices"
	"strconv"
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

// reuseGlobOverlaps reports whether glob matches at least one file subject names. subject is a
// file path, matched in full, or a prefix followed by "**", naming every file whose path continues
// the prefix: "<dir>/**" names the files below dir. Whether glob matches every one of them is
// reuseGlobCheck.includes over reuseSubjectGlob, the one coverage decision of the REUSE.toml
// gates.
func reuseGlobOverlaps(glob, subject string) bool {
	tokens := reuseGlobTokens(glob)
	prefix, directory := strings.CutSuffix(subject, "**")
	if !directory {
		return reuseGlobStates(tokens, subject)[len(tokens)]
	}
	states := reuseGlobStates(tokens, prefix)
	return slices.Contains(states[:len(tokens)], true)
}

// reuseSubjectGlob is the glob naming exactly the files subject names (reuseGlobOverlaps): its
// path, or its prefix followed by "**", with every byte the glob dialect reads as a pattern made
// literal by a backslash.
func reuseSubjectGlob(subject string) string {
	prefix, directory := strings.CutSuffix(subject, "**")
	var glob strings.Builder
	for index := 0; index < len(prefix); index++ {
		if prefix[index] == '*' || prefix[index] == '\\' {
			glob.WriteByte('\\')
		}
		glob.WriteByte(prefix[index])
	}
	if directory {
		glob.WriteString("**")
	}
	return glob.String()
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

// maxReuseGlobSteps bounds the automaton steps one REUSE.toml check spends comparing path globs
// (HISS-02, reuseGlobCheck): one step reads one byte into one glob's automaton, so the bound is
// the real cost of the product states explored, whatever the number of tables. A file of literal
// globs within MaxReuseLines, one table per file listed, stays under it.
const maxReuseGlobSteps = 1 << 24

// ErrReuseGlobBound is the error of a REUSE.toml check whose glob comparisons need more automaton
// steps than its bound. The check is not answered, never answered in part: the caller reports it
// as not checked.
var ErrReuseGlobBound = errors.New("comparing the path globs needs more automaton steps than the bound")

// reuseGlobCheck compares the path globs of one REUSE.toml check: each glob is tokenized once,
// every path is read over one alphabet for all of them (reuseGlobAlphabet), and every comparison
// spends one budget of automaton steps, left.
type reuseGlobCheck struct {
	globs    map[string]reuseGlobPrepared
	alphabet []byte
	bound    int
	left     int
}

// reuseGlobPrepared is one glob of a check: its tokens and the literal bytes before its first
// star, which every path it matches starts with.
type reuseGlobPrepared struct {
	tokens []reuseGlobToken
	prefix string
}

// newReuseGlobCheck is a check over globs, every glob it will compare, with a budget of steps.
func newReuseGlobCheck(globs []string, steps int) *reuseGlobCheck {
	check := &reuseGlobCheck{globs: make(map[string]reuseGlobPrepared, len(globs)), bound: steps, left: steps}
	tokenized := make([][]reuseGlobToken, 0, len(globs))
	for _, glob := range globs {
		if _, known := check.globs[glob]; !known {
			tokens := reuseGlobTokens(glob)
			check.globs[glob] = reuseGlobPrepared{tokens: tokens, prefix: reuseGlobLiteralPrefix(tokens)}
			tokenized = append(tokenized, tokens)
		}
	}
	check.alphabet = reuseGlobAlphabet(tokenized...)
	return check
}

// reuseGlobLiteralPrefix returns the bytes of the literal tokens that open tokens.
func reuseGlobLiteralPrefix(tokens []reuseGlobToken) string {
	prefix := make([]byte, 0, len(tokens))
	for _, token := range tokens {
		if token.kind != reuseGlobLiteral {
			break
		}
		prefix = append(prefix, token.char)
	}
	return string(prefix)
}

// spend takes steps from the budget and fails with ErrReuseGlobBound, naming the bound, once it
// runs out.
func (c *reuseGlobCheck) spend(steps int) error {
	c.left -= steps
	if c.left < 0 {
		return fmt.Errorf("%w of %d", ErrReuseGlobBound, c.bound)
	}
	return nil
}

// reuseGlobLive is one outer glob a path prefix leaves able to match a longer path: its position
// among the outers of the product and its automaton states.
type reuseGlobLive struct {
	glob   int
	states []bool
}

// reuseGlobPair is one state of the product of an inner glob's automaton and the union of outer
// globs' automata: the states each reaches after one path prefix, the outers that can no longer
// match left out.
type reuseGlobPair struct {
	inner []bool
	outer []reuseGlobLive
}

// reuseGlobProduct is the product includes explores: one inner glob, the outer globs whose union
// it is compared with, and the check whose alphabet and budget it uses.
type reuseGlobProduct struct {
	check  *reuseGlobCheck
	inner  []reuseGlobToken
	outers [][]reuseGlobToken
}

// includes reports whether the outers together match every path inner matches, so later tables
// with outers relabel every file of inner: one glob such as "**", or a union such as "*", ".*",
// "*/**" and ".*/**", which no one of its globs covers alone. It explores the product of inner's
// automaton and every outer's, their state sets after each path prefix, breadth first, and looks
// for a prefix inner accepts and no outer does. A prefix after which inner matches nothing more,
// or an outer matches every continuation, leads to no such path and is not explored. The paths
// read only the check's alphabet. Every glob must be one the check was made with; past the budget
// the answer is ErrReuseGlobBound.
func (c *reuseGlobCheck) includes(outers []string, inner string) (bool, error) {
	product, err := c.product(outers, inner)
	if err != nil {
		return false, err
	}
	start := product.start()
	if product.escapes(start) {
		return false, nil
	}
	queue := []reuseGlobPair{start}
	seen := map[string]bool{reuseGlobPairKey(start): true}
	// Every pass spends at least one step, so the budget bounds the passes too.
	for pass := 0; len(queue) > 0 && pass <= c.bound; pass++ {
		next, escaped, err := product.expand(queue[0])
		if err != nil || escaped {
			return false, err
		}
		queue = queue[1:]
		for _, pair := range next {
			if key := reuseGlobPairKey(pair); !seen[key] {
				seen[key] = true
				queue = append(queue, pair)
			}
		}
	}
	return len(queue) == 0, nil
}

// product is the product of inner with the union of outers, each tokenized when the check was
// made. An outer whose literal prefix and inner's differ before either ends matches no path inner
// does, so it is left out of the union unread; considering each outer spends a step.
func (c *reuseGlobCheck) product(outers []string, inner string) (reuseGlobProduct, error) {
	innerGlob, known := c.globs[inner]
	if !known {
		return reuseGlobProduct{}, fmt.Errorf("path glob %q is not one the comparison was prepared for", inner)
	}
	product := reuseGlobProduct{check: c, inner: innerGlob.tokens}
	for _, glob := range outers {
		outer, known := c.globs[glob]
		if !known {
			return reuseGlobProduct{}, fmt.Errorf("path glob %q is not one the comparison was prepared for", glob)
		}
		if strings.HasPrefix(outer.prefix, innerGlob.prefix) || strings.HasPrefix(innerGlob.prefix, outer.prefix) {
			product.outers = append(product.outers, outer.tokens)
		}
	}
	return product, c.spend(1 + len(outers))
}

// start returns the product state before any byte is read, every outer alive.
func (p reuseGlobProduct) start() reuseGlobPair {
	pair := reuseGlobPair{inner: reuseGlobStart(p.inner), outer: make([]reuseGlobLive, 0, len(p.outers))}
	for index, tokens := range p.outers {
		pair.outer = append(pair.outer, reuseGlobLive{glob: index, states: reuseGlobStart(tokens)})
	}
	return pair
}

// expand returns the open pairs that one more byte after pair leads to, and whether one of them
// escapes. A byte after which inner matches nothing more leads nowhere.
func (p reuseGlobProduct) expand(pair reuseGlobPair) (next []reuseGlobPair, escaped bool, err error) {
	for _, char := range p.check.alphabet {
		candidate, alive, err := p.step(pair, char)
		switch {
		case err != nil:
			return nil, false, err
		case !alive:
			continue
		case p.escapes(candidate):
			return nil, true, nil
		case p.open(candidate):
			next = append(next, candidate)
		}
	}
	return next, false, nil
}

// step returns the pair that reading char after pair leads to, and whether inner still matches a
// path continuing it. It spends one step for inner and, when inner does, one for each outer
// alive.
func (p reuseGlobProduct) step(pair reuseGlobPair, char byte) (reuseGlobPair, bool, error) {
	inner := reuseGlobStep(p.inner, pair.inner, char)
	if !slices.Contains(inner, true) {
		return reuseGlobPair{}, false, p.check.spend(1)
	}
	return reuseGlobPair{inner: inner, outer: p.stepOuters(pair.outer, char)}, true, p.check.spend(1 + len(pair.outer))
}

// stepOuters returns the outers alive after reading char from live, with their states.
func (p reuseGlobProduct) stepOuters(live []reuseGlobLive, char byte) []reuseGlobLive {
	next := make([]reuseGlobLive, 0, len(live))
	for _, outer := range live {
		if states := reuseGlobStep(p.outers[outer.glob], outer.states, char); slices.Contains(states, true) {
			next = append(next, reuseGlobLive{glob: outer.glob, states: states})
		}
	}
	return next
}

// escapes reports whether the prefix that reached pair is a path inner matches and no outer does.
func (p reuseGlobProduct) escapes(pair reuseGlobPair) bool {
	if !pair.inner[len(p.inner)] {
		return false
	}
	return !slices.ContainsFunc(pair.outer, func(outer reuseGlobLive) bool { return outer.states[len(p.outers[outer.glob])] })
}

// open reports whether a longer prefix than the one that reached pair may still escape: no outer
// already matches every continuation.
func (p reuseGlobProduct) open(pair reuseGlobPair) bool {
	for _, outer := range pair.outer {
		tokens := p.outers[outer.glob]
		for position := range tokens {
			if outer.states[position] && !slices.ContainsFunc(tokens[position:], func(token reuseGlobToken) bool { return token.kind != reuseGlobstar }) {
				return false
			}
		}
	}
	return true
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

// reuseGlobPairKey identifies a product state: one byte per automaton state of inner, then the
// position of every outer alive and its states, a separator after each automaton.
func reuseGlobPairKey(pair reuseGlobPair) string {
	key := make([]byte, 0, len(pair.inner)+1)
	key = appendReuseGlobStates(key, pair.inner)
	for _, outer := range pair.outer {
		key = strconv.AppendInt(key, int64(outer.glob), 10)
		key = appendReuseGlobStates(append(key, ':'), outer.states)
	}
	return string(key)
}

// appendReuseGlobStates appends one byte per state of states, and a separator, to key.
func appendReuseGlobStates(key []byte, states []bool) []byte {
	for _, state := range states {
		mark := byte('0')
		if state {
			mark = '1'
		}
		key = append(key, mark)
	}
	return append(key, '|')
}

// reuseGlobClose marks every position that a marked one reaches by matching stars to nothing.
func reuseGlobClose(tokens []reuseGlobToken, states []bool) {
	for position, token := range tokens {
		states[position+1] = states[position+1] || (states[position] && token.kind != reuseGlobLiteral)
	}
}
