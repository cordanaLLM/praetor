package hiss

import "strings"

// Where a call in a Rust function body stands (HISS-01).
//
// A bare call reaches a local of the function's name only while that local is in scope, and
// Rust scopes are lexical: a let shadows from the end of its statement to the end of its
// block, a for, if-let or while-let pattern only inside the body it heads, a match arm's
// pattern only in that arm, and a closure parameter only in the closure's body. A binding
// that stayed in force for the rest of the function hid every recursive call written after a
// closure, loop or inner block that happened to reuse the name.
//
// An arm or a closure is an expression, so its end follows rustc's grammar rather than the
// next closing brace. An arm ends at its comma, or, when its body starts with a block-like
// expression ({, if, match, loop, while, for, unsafe, const), at the brace closing that
// expression unless an else, a method call or ? carries it on; a brace inside the body (an
// if-else branch, a nested match) ends nothing. A closure's body is a whole expression, so it
// ends only at a comma, a semicolon or the bracket around it.
//
// rustWalk follows one function body byte by byte, tracking brace and parenthesis depth, to
// place each call against those scopes. It also marks the input of a macro invocation: a
// macro may rewrite its input into a call of something else (syscall!(recv(fd)) expands to
// libc::recv), so a call written there is not decided. The standard macros whose input is
// plain expressions evaluated where they stand (assert!, format!, vec!, ...) are read
// through.

// rustBindKind is how a pattern binding the function's name comes into scope.
type rustBindKind int

const (
	rustNoBinding rustBindKind = iota
	// rustLetBinding is a let statement: in scope from its semicolon to the end of its block.
	rustLetBinding
	// rustHeadBinding is a for, if-let or while-let pattern: in scope in the block it heads.
	rustHeadBinding
	// rustArmBinding is a match arm's pattern: in scope from its arrow to the end of the arm.
	rustArmBinding
	// rustClosureBinding is a closure parameter: in scope from the pipe closing the parameters
	// to the end of the closure.
	rustClosureBinding
)

// maxRustPendingBindings bounds the bindings waiting to come into scope (HISS-02).
const maxRustPendingBindings = 8

// rustExpressionMacros are the standard macros whose input is plain expressions evaluated
// where they stand, so a call written there is a call.
var rustExpressionMacros = map[string]bool{
	"assert": true, "assert_eq": true, "assert_ne": true, "debug_assert": true,
	"debug_assert_eq": true, "debug_assert_ne": true, "dbg": true, "eprint": true,
	"eprintln": true, "format": true, "format_args": true, "matches": true, "panic": true,
	"print": true, "println": true, "todo": true, "unimplemented": true, "unreachable": true,
	"vec": true, "write": true, "writeln": true,
}

// rustScope is one binding of the name, placed by the brace and parenthesis depth it belongs
// to. While pending, the depths are where its trigger must stand; once in scope, where it
// ends.
type rustScope struct {
	kind  rustBindKind
	brace int
	paren int
	// pipe is the byte, on the binding's line, of the pipe closing a closure's parameters.
	pipe int
	// block is set on an arm whose body starts with a block-like expression, which ends the
	// arm at its closing brace.
	block bool
}

// rustMacroInput is the input of a macro invocation the walk is inside.
type rustMacroInput struct {
	open bool
	// paren is set when a parenthesis or bracket delimits the input rather than a brace.
	paren bool
	depth int
}

// rustWalk is the cross-line state of one function body.
type rustWalk struct {
	brace, paren int
	// pending holds bindings waiting for the byte that brings them into scope.
	pending []rustScope
	// active is the binding in scope while live is set; a bare call then reaches the local.
	active rustScope
	live   bool
	// closing is set once the block-like body of the arm in scope has closed: the next token
	// decides whether the arm ends there (settle).
	closing bool
	macro   rustMacroInput
}

// observeBody walks one line of the body, placing each call against the scopes open at it.
// An item of the name (observeBinding) covers the whole body; a call in the text an arm
// pattern occupies (rustCallText) names a path and calls nothing.
func (r *rustSelfCalls) observeBody(body string, line int) {
	r.calls.observeBinding(body)
	from := len(body) - len(rustCallText(body))
	r.walk.startLine()
	for i := 0; i < len(body); i++ {
		r.walk.settle(body, i)
		if i == 0 || !isIdentByte(body[i-1]) {
			r.observeWord(body, i, from, line)
		}
		r.walk.step(body, i)
	}
}

// observeWord inspects the word starting at at: a call reaching the function, unless it sits
// in a macro's input or a local of the name is in scope, or a pattern binding the name.
func (r *rustSelfCalls) observeWord(body string, at, from, line int) {
	c := &r.calls
	if r.walk.macro.open {
		return
	}
	if at >= from && (!c.shadowable || !r.walk.live) && c.callsAt(body, at) {
		c.addSite(line)
		return
	}
	if c.shadowable && identAt(body, at, c.name) {
		kind, keyword := rustBindingAt(body, at)
		r.walk.bind(body, at, kind, keyword)
	}
}

// rustBindingAt classifies the occurrence of the name at at as a pattern binding, and returns
// the index of the let or for keyword that introduces it (-1 for an arm or closure). A name
// followed by a parenthesis, a macro bang or a path separator is a call, a macro or a path,
// never a binding, so `match f(n) {` and `a | f(n) | b` are calls.
func rustBindingAt(code string, at int) (rustBindKind, int) {
	before, after := code[:at], code[at:]
	next := strings.TrimLeft(after[len(leadingIdent(after)):], " \t")
	if strings.HasPrefix(next, "(") || strings.HasPrefix(next, "!") || strings.HasPrefix(next, "::") {
		return rustNoBinding, -1
	}
	if kind, keyword := rustKeywordBinding(before, after); kind != rustNoBinding {
		return kind, keyword
	}
	if rustInClosureParams(before) && strings.Contains(after, "|") {
		return rustClosureBinding, -1
	}
	if strings.Contains(after, "=>") && !strings.Contains(before, "=>") {
		return rustArmBinding, -1
	}
	return rustNoBinding, -1
}

// rustInClosureParams reports whether the text before a name ends inside a closure's parameter
// list: its last pipe opens one, because what stands before that pipe cannot be the left
// operand of a bitwise or (`(|`, `= |`, `move |`, a line start). A pipe after an operand is an
// or-pattern or a bitwise or, so `A | B => v.map(|f| f())` and `(a | b) + v.map(|f| f())` still
// see the closure's opening pipe.
func rustInClosureParams(before string) bool {
	pipe := strings.LastIndexByte(before, '|')
	if pipe < 0 {
		return false
	}
	prev := strings.TrimRight(before[:pipe], " \t")
	if prev == "" || strings.IndexByte("([{,;=:&>", prev[len(prev)-1]) >= 0 {
		return true
	}
	return endsWithWord(prev, "move") || endsWithWord(prev, "return")
}

// rustKeywordBinding recognises a binding in the pattern of a let or a for, which runs from the
// keyword to the let's = or the for's in, and returns the keyword's index.
func rustKeywordBinding(before, after string) (rustBindKind, int) {
	if let := lastIdent(before, "let"); let >= 0 && !strings.ContainsAny(before[let:], "=;") {
		return rustLetKind(before[:let]), let
	}
	if loop := lastIdent(before, "for"); loop >= 0 && !strings.Contains(before[loop:], " in ") &&
		strings.Contains(after, " in ") {
		return rustHeadBinding, loop
	}
	return rustNoBinding, -1
}

// rustLetKind tells a let statement from the let of an if-let, a while-let or a let chain,
// given the text before the let keyword.
func rustLetKind(prefix string) rustBindKind {
	t := strings.TrimRight(prefix, " \t")
	if strings.HasSuffix(t, "&&") || endsWithWord(t, "if") || endsWithWord(t, "while") {
		return rustHeadBinding
	}
	return rustLetBinding
}

// startLine drops closure bindings still waiting for their pipe: the pipe index belongs to the
// line they were seen on.
func (w *rustWalk) startLine() {
	kept := w.pending[:0]
	for i := 0; i < len(w.pending); i++ {
		if w.pending[i].kind != rustClosureBinding {
			kept = append(kept, w.pending[i])
		}
	}
	w.pending = kept
}

// bind records a binding of the name at at, waiting for its trigger. A let or for binding
// belongs to the parenthesis depth of its keyword, so `let (a, f) = [0; 2];` waits for the
// semicolon outside the pattern and the array.
func (w *rustWalk) bind(code string, at int, kind rustBindKind, keyword int) {
	if kind == rustNoBinding || len(w.pending) >= maxRustPendingBindings {
		return
	}
	s := rustScope{kind: kind, brace: w.brace, paren: w.paren, pipe: -1}
	if keyword >= 0 {
		s.paren -= rustNesting(code[keyword:at])
	}
	if kind == rustClosureBinding {
		s.pipe = at + strings.IndexByte(code[at:], '|')
	}
	w.pending = append(w.pending, s)
}

// step moves the walk past byte i: a pending binding may come into scope at it, it may open
// or close a bracket, and the scope or macro input it closes ends.
func (w *rustWalk) step(code string, i int) {
	w.fire(code, i)
	switch code[i] {
	case '{':
		w.brace++
	case '}':
		w.brace--
	case '(', '[':
		w.paren++
	case ')', ']':
		w.paren--
	case '!':
		w.enterMacro(code, i)
	}
	w.leave(code[i])
}

// fire brings the innermost pending binding into scope when byte i is its trigger, unless the
// binding already in scope outlasts it.
func (w *rustWalk) fire(code string, i int) {
	n := len(w.pending)
	if n == 0 || !w.pending[n-1].firesAt(code, i, w.brace, w.paren) {
		return
	}
	s := w.pending[n-1].scope(code, i, w.brace, w.paren)
	w.pending = w.pending[:n-1]
	if !w.live || s.outlasts(w.active) {
		w.active, w.live, w.closing = s, true, false
	}
}

// settle decides, at the first token after the block-like body of the arm in scope closed,
// whether the arm ended there. That token may sit on a later line: rustc accepts an else
// there too.
func (w *rustWalk) settle(code string, i int) {
	if !w.closing || strings.IndexByte(" \t\r", code[i]) >= 0 {
		return
	}
	w.closing = false
	if rustArmEnds(code[i:]) {
		w.live = false
	}
}

// leave ends what the byte just walked closed: pending bindings whose block closed before
// their trigger, the binding in scope, and the macro input.
func (w *rustWalk) leave(c byte) {
	for i := 0; i < maxRustPendingBindings && len(w.pending) > 0; i++ {
		if w.brace >= w.pending[len(w.pending)-1].brace {
			break
		}
		w.pending = w.pending[:len(w.pending)-1]
	}
	w.leaveScope(c)
	if w.macro.open && w.macro.closedBy(c, w.brace, w.paren) {
		w.macro.open = false
	}
}

// leaveScope ends the binding in scope when c closed it, or marks the block-like body of its
// arm closed so settle can decide at the next token.
func (w *rustWalk) leaveScope(c byte) {
	switch {
	case !w.live:
	case w.active.endsAt(c, w.brace, w.paren):
		w.live = false
	case w.active.closesBody(c, w.brace, w.paren):
		w.closing = true
	}
}

// enterMacro opens a macro's input at a bang that follows the macro's name and precedes its
// delimiter, unless the macro is a standard expression macro or the walk is already inside
// one.
func (w *rustWalk) enterMacro(code string, i int) {
	name := trailingIdent(code[:i])
	rest := strings.TrimLeft(code[i+1:], " \t")
	if w.macro.open || name == "" || rest == "" || strings.IndexByte("([{", rest[0]) < 0 || rustExpressionMacros[name] {
		return
	}
	w.macro = rustMacroInput{open: true, paren: rest[0] != '{', depth: w.paren}
	if !w.macro.paren {
		w.macro.depth = w.brace
	}
}

// closedBy reports whether c, walked to the given depths, closed the macro's delimiter.
func (m rustMacroInput) closedBy(c byte, brace, paren int) bool {
	if m.paren {
		return (c == ')' || c == ']') && paren <= m.depth
	}
	return c == '}' && brace <= m.depth
}

// firesAt reports whether byte i, walked at the given depths, brings the binding into scope:
// the semicolon ending a let, the brace opening the body a for or if-let heads, the arrow of a
// match arm, or the pipe closing a closure's parameters.
func (s rustScope) firesAt(code string, i, brace, paren int) bool {
	if s.kind == rustClosureBinding {
		return i == s.pipe
	}
	if brace != s.brace || paren > s.paren {
		return false
	}
	switch s.kind {
	case rustLetBinding:
		return code[i] == ';' && paren == s.paren
	case rustHeadBinding:
		return code[i] == '{' && paren == s.paren
	}
	return strings.HasPrefix(code[i:], "=>")
}

// scope returns where the binding is in scope once it fires at byte i, walked at the given
// depths. An arm's arrow is followed by its body, which tells whether a brace can end it.
func (s rustScope) scope(code string, i, brace, paren int) rustScope {
	switch s.kind {
	case rustLetBinding:
		return rustScope{kind: s.kind, brace: brace}
	case rustHeadBinding:
		return rustScope{kind: s.kind, brace: brace + 1}
	case rustArmBinding:
		return rustScope{kind: s.kind, brace: brace, paren: paren, block: rustBlockLed(code[i+len("=>"):])}
	}
	return rustScope{kind: s.kind, brace: brace, paren: paren}
}

// expression reports whether the scope ends with the arm or closure expression it belongs to
// rather than with a block.
func (s rustScope) expression() bool {
	return s.kind == rustArmBinding || s.kind == rustClosureBinding
}

// outlasts reports whether s stays in scope at least as long as t.
func (s rustScope) outlasts(t rustScope) bool {
	return s.brace < t.brace || (s.brace == t.brace && t.expression() && !s.expression())
}

// endsAt reports whether c, walked to the given depths, ends the scope: the block holding it
// closes, or an arm or closure expression ends at a comma, a semicolon or a closing bracket.
// A brace closed inside the expression ends nothing; closesBody covers the arm whose body is
// block-like.
func (s rustScope) endsAt(c byte, brace, paren int) bool {
	switch {
	case brace < s.brace:
		return true
	case !s.expression():
		return false
	case paren < s.paren:
		return true
	}
	return brace == s.brace && paren == s.paren && (c == ',' || c == ';')
}

// closesBody reports whether c, walked to the given depths, closed a block at the depth of an
// arm whose body is block-like: the arm ends there unless the next token carries it on.
func (s rustScope) closesBody(c byte, brace, paren int) bool {
	return s.block && c == '}' && brace == s.brace && paren == s.paren
}

// rustBlockLeaders are the keywords that start a block-like expression, which rustc ends at
// its closing brace when it stands first in a match arm.
var rustBlockLeaders = map[string]bool{
	"if": true, "match": true, "loop": true, "while": true, "for": true, "unsafe": true, "const": true,
}

// rustBlockLed reports whether an arm body starting at text is a block-like expression. A body
// that starts on a later line counts as not block-like, so its arm ends at its comma.
func rustBlockLed(text string) bool {
	text = strings.TrimLeft(text, " \t")
	return strings.HasPrefix(text, "{") || rustBlockLeaders[leadingIdent(text)]
}

// rustArmEnds reports whether the token text starts with ends an arm whose block-like body has
// just closed: a comma, the match's closing brace, or a token the next arm's pattern starts
// with (a name, a literal, a tuple, a slice, an attribute, a range or a path). An else or in,
// a method call, ?, = (an if-let pattern's struct braces) and { (a block in the head) carry
// the expression on. rustc rejects a binary operator there, so reading one as a continuation
// costs nothing on code that compiles.
func rustArmEnds(text string) bool {
	switch word := leadingIdent(text); {
	case word == "else" || word == "in":
		return false
	case word != "":
		return true
	case text == "":
		return false
	}
	return strings.IndexByte(",}([#'\"", text[0]) >= 0 || strings.HasPrefix(text, "..") || strings.HasPrefix(text, "::")
}

// rustNesting returns how many parentheses and brackets text leaves open.
func rustNesting(text string) int {
	return strings.Count(text, "(") + strings.Count(text, "[") - strings.Count(text, ")") - strings.Count(text, "]")
}

// lastIdent returns the index of the last whole-identifier occurrence of name in text, or -1.
func lastIdent(text, name string) int {
	last := -1
	at := nextIdent(text, name, 0)
	for i := 0; i < len(text) && at >= 0; i++ {
		last = at
		at = nextIdent(text, name, at+1)
	}
	return last
}

// trailingIdent returns the identifier text ends with.
func trailingIdent(text string) string {
	start := len(text)
	for start > 0 && isIdentByte(text[start-1]) {
		start--
	}
	return text[start:]
}

// endsWithWord reports whether text ends with word as a whole identifier.
func endsWithWord(text, word string) bool {
	return strings.HasSuffix(text, word) && identAt(text, len(text)-len(word), word)
}
