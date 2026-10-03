// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package hiss

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/cordanaLLM/praetor/internal/util"
)

// JavaScript, TypeScript and Svelte (the <script> blocks of a component).
//
// The scanner is a line scanner like the Rust and C ones: the shared literal stripper removes
// strings, comments, template text and regular expression literals, and what remains is read
// for the invariants and for the braces that open and close each function. It decides:
//
//   - HISS-01 direct recursion: a declared or bound function calling its bare name, and a
//     method or class field calling this.name. A local binding of the name inside the body
//     (a declarator, a nested function, a parameter, a catch or arrow parameter) shadows it.
//   - HISS-02 the unbounded while (true), while (1) and for (;;) loops.
//   - HISS-04 the length of every named function, measured from the line its body opens on.
//   - HISS-07 an empty catch block, a .catch(() => {}) that swallows a rejection, and
//     process.exit or process.abort outside a test file, module scope and a top-level main.
//   - HISS-08 eval, the Function constructor, and setTimeout or setInterval given a string.
//
// A file with a line longer than maxScriptLineBytes is minified or generated output and is
// declined, so the report counts it as unscanned source rather than as clean. So is a file whose
// braces this line scanner misread (they do not balance): its function boundaries are unknown,
// so none of its findings is reported and no rule's silence over it counts as a pass.

const (
	// maxScriptLineBytes is the longest line a hand-written script file is expected to carry.
	maxScriptLineBytes = 1024
	// maxScriptNesting bounds the stack of open functions (HISS-02).
	maxScriptNesting = 64
	// maxSvelteTagsPerLine bounds the script tags read from one component line (HISS-02).
	maxSvelteTagsPerLine = 8
)

var (
	svelteScriptOpen = regexp.MustCompile(`(?i)^\s*<script(?:\s[^>]*)?>`)
	// scriptEmptyCatch is a catch clause whose block is empty on its line; scriptCatchOpens is
	// one whose block opens at the end of the line, empty so far.
	scriptEmptyCatch = regexp.MustCompile(`\bcatch\s*(?:\([^()]*\))?\s*\{\s*\}`)
	scriptCatchOpens = regexp.MustCompile(`\bcatch\s*(?:\([^()]*\))?\s*\{\s*$`)
	// scriptSwallowedRejection is a promise .catch whose handler does nothing.
	scriptSwallowedRejection = regexp.MustCompile(`\.\s*catch\s*\(\s*(?:async\s*)?(?:(?:\(\s*[\w$]*\s*\)|[\w$]+)\s*=>|function\s*[\w$]*\s*\(\s*[\w$]*\s*\))\s*\{\s*\}\s*\)`)
	scriptProcessExit        = regexp.MustCompile(`\bprocess\s*\.\s*(exit|abort)\s*\(`)
	// scriptImpliedEval is setTimeout or setInterval whose first argument was a string literal,
	// which the stripper removed: the runtime evaluates that string as code.
	scriptImpliedEval = regexp.MustCompile(`\bset(?:Timeout|Interval)\s*\(\s*[,)]`)
)

// scriptLanguage reads JavaScript, TypeScript and Svelte components.
type scriptLanguage struct{}

func (scriptLanguage) handles(ext string) bool {
	switch util.SourceLanguage(ext) {
	case "javascript", "typescript", "svelte":
		return true
	}
	return false
}

func (scriptLanguage) name() string { return "javascript" }

func (scriptLanguage) scan(src sourceFile, rep *ScanReport, opts ScanOptions) bool {
	lines := src.lfLines()
	if util.SourceLanguage(src.rel) == "svelte" {
		lines = svelteScriptLines(lines)
	}
	if isMinified(lines) {
		return false
	}
	// The findings are collected per file and reported only once the file's braces balance.
	file := &ScanReport{}
	s := &scriptScanner{rel: src.rel, rep: file, maxLOC: opts.MaxFuncLOC,
		strip: literalStripper{syn: scriptSyntax}, test: isScriptTestPath(src.rel)}
	for idx, line := range lines {
		s.observe(idx+1, line)
	}
	if s.misread() {
		return false
	}
	for _, v := range file.Violations {
		recordViolation(rep, v.RuleID, v.FilePath, v.LineNumber, v.Symbol, v.Message)
	}
	rep.adoptFunctions(file)
	return true
}

// isMinified reports whether any line is longer than hand-written source carries.
func isMinified(lines []string) bool {
	for _, line := range lines {
		if len(line) > maxScriptLineBytes {
			return true
		}
	}
	return false
}

// svelteScriptLines keeps the contents of a component's <script> blocks and blanks every other
// line, so line numbers still match the component. Markup and styles are not script. A block
// opens with a <script> tag at the start of a line, where a component keeps its top-level
// blocks; a <script> written in markup text, a comment or an attribute string opens nothing.
func svelteScriptLines(lines []string) []string {
	out := make([]string, len(lines))
	inScript := false
	for i, line := range lines {
		out[i], inScript = svelteScriptPart(line, inScript)
	}
	return out
}

// svelteScriptPart returns the script text of one component line, given whether a script
// block is open at its start, and whether one is open at its end.
func svelteScriptPart(line string, inScript bool) (string, bool) {
	var b strings.Builder
	for i := 0; i < maxSvelteTagsPerLine && line != ""; i++ {
		if !inScript {
			loc := svelteScriptOpen.FindStringIndex(line)
			if loc == nil {
				return b.String(), false
			}
			line, inScript = line[loc[1]:], true
			continue
		}
		end := strings.Index(strings.ToLower(line), "</script")
		if end < 0 {
			b.WriteString(line)
			return b.String(), true
		}
		b.WriteString(line[:end])
		line, inScript = line[end:], false
	}
	return b.String(), inScript
}

// isScriptTestPath follows the common JavaScript test runners: *.test.* and *.spec.* files, and
// anything under __tests__, test or tests.
func isScriptTestPath(rel string) bool {
	base, inTestDir := testPathParts(rel, "__tests__", "test", "tests")
	return inTestDir || strings.Contains(base, ".test.") || strings.Contains(base, ".spec.")
}

// scriptFunc is one open named function.
type scriptFunc struct {
	name string
	// start is the line the body opens on; HISS-04 measures from there, as for C and Rust.
	start int
	// depth is the brace depth inside the body; the function closes when the depth drops
	// below it.
	depth int
	// from is where this line's text of the body starts: 0, or just past the opening brace on
	// the line the body opens.
	from  int
	calls selfCalls
}

// scriptSignature is a header whose parameter list continues on the lines below it.
type scriptSignature struct {
	header scriptHeader
	params string
	depth  int
	age    int
}

// scriptScanner carries the cross-line state of one script file.
type scriptScanner struct {
	rel    string
	rep    *ScanReport
	maxLOC int
	strip  literalStripper
	test   bool
	// depth is the brace depth of the code read so far.
	depth int
	open  []scriptFunc
	// sig is the header waiting for its parameter list to close, when active.
	sig    scriptSignature
	sigSet bool
	// catchAt is the line of a catch clause whose block opened empty, or 0.
	catchAt int
	// outer is the outermost function open at any point of the current line, or "".
	outer string
	// unbalanced records a closing brace with nothing open, which means the structure was
	// misread and the file is declined.
	unbalanced bool
}

// observe feeds one physical line.
func (s *scriptScanner) observe(lineNum int, line string) {
	code := s.strip.strip(line)
	h, bodyAt, params := s.header(code)
	s.outer = ""
	if len(s.open) > 0 {
		s.outer = s.open[0].name
	}
	var fn scriptFunc
	defined := ""
	if bodyAt >= 0 {
		fn, defined = s.newFunc(h, params, lineNum), h.name
	}
	s.braces(code, lineNum, bodyAt, fn)
	s.checkInvariants(code, lineNum, defined)
}

// header finds the function whose body opens on this line: its header, the index of the body
// brace (-1 when none opens), and the parameter text. A wrapped parameter list waiting for its
// closing parenthesis does not hide a function whose header and body open on one of its lines:
// `name({` starts both a method with a destructured parameter and a call with an object
// argument, and the methods of that object are still functions.
func (s *scriptScanner) header(code string) (scriptHeader, int, string) {
	if !s.sigSet {
		return s.lineHeader(code, true)
	}
	if h, bodyAt, params, closed := s.continueSignature(code); closed {
		return h, bodyAt, params
	}
	return s.lineHeader(code, false)
}

// lineHeader finds a function header written on this line. A header whose parameter list is
// wrapped onto the lines below starts a pending signature when follow is set.
func (s *scriptScanner) lineHeader(code string, follow bool) (scriptHeader, int, string) {
	lead := len(code) - len(strings.TrimLeft(code, " \t"))
	h, ok := matchScriptHeader(strings.TrimSpace(code))
	if !ok {
		return scriptHeader{}, -1, ""
	}
	at := lead + h.at
	if !h.paren {
		return h, bodyBrace(code, at, false), ""
	}
	closing, depth := closeParen(code, at, 1)
	if closing >= 0 {
		return h, bodyBrace(code, closing+1, h.arrow), code[at:closing]
	}
	// A wrapped parameter list starts on the line below its opening parenthesis, or below the
	// brace or bracket of a destructured parameter that opens right after it; any other
	// unclosed header line is a call or an expression this scanner does not follow.
	if follow && depth > 0 && opensOnlyPatterns(code[at:]) {
		s.sig, s.sigSet = scriptSignature{header: h, params: code[at:], depth: depth}, true
	}
	return scriptHeader{}, -1, ""
}

// continueSignature feeds one more line of a wrapped parameter list and reports whether the
// list closed on it.
func (s *scriptScanner) continueSignature(code string) (scriptHeader, int, string, bool) {
	closing, depth := closeParen(code, 0, s.sig.depth)
	if closing >= 0 {
		s.sigSet = false
		params := appendSignature(s.sig.params, code[:closing])
		return s.sig.header, bodyBrace(code, closing+1, s.sig.header.arrow), params, true
	}
	s.sig.params = appendSignature(s.sig.params, code)
	s.sig.depth = depth
	s.sig.age++
	if depth <= 0 || s.sig.age >= maxPendingHeaderLines {
		s.sigSet = false
	}
	return scriptHeader{}, -1, "", false
}

// newFunc builds the function a header opens, deciding the spellings that reach it from its
// own body. The enclosing functions whose name one of its parameters binds are shadowed.
func (s *scriptScanner) newFunc(h scriptHeader, params string, lineNum int) scriptFunc {
	fn := scriptFunc{name: h.name, start: lineNum, calls: selfCalls{name: h.name, excluded: isScriptSelectorByte}}
	switch h.callee {
	case scriptBareCallee:
		fn.calls.callees = []string{h.name}
		if h.alias != "" {
			fn.calls.callees = append(fn.calls.callees, h.alias)
		}
		fn.calls.shadowable = true
		fn.calls.binds = scriptBinds
		fn.calls.bound = containsIdent(params, h.name)
	case scriptThisCallee:
		fn.calls.callees = []string{"this." + h.name}
	}
	for i := range s.open {
		if s.open[i].calls.shadowable && containsIdent(params, s.open[i].name) {
			s.open[i].calls.bound = true
		}
	}
	return fn
}

// isScriptSelectorByte extends isSelectorByte with the bytes that continue a JavaScript
// identifier ($) or mark a private member (#), so $f() and #f() never read as a call of f.
func isScriptSelectorByte(c byte) bool {
	return isSelectorByte(c) || c == '$' || c == '#'
}

// braces follows the braces of one line: the function whose header was found opens at bodyAt,
// and every function whose body closes on the line is measured and decided.
func (s *scriptScanner) braces(code string, lineNum, bodyAt int, fn scriptFunc) {
	for i := 0; i < len(code); i++ {
		switch code[i] {
		case '{':
			s.depth++
			if i == bodyAt {
				s.push(fn, i+1)
			}
		case '}':
			s.depth--
			if s.depth < 0 {
				s.depth, s.unbalanced = 0, true
			}
			s.closeDeeper(code, i, lineNum)
		}
	}
	for i := range s.open {
		s.observeBody(&s.open[i], code[s.open[i].from:], lineNum)
		s.open[i].from = 0
	}
}

// push opens fn with its body starting at from on the current line.
func (s *scriptScanner) push(fn scriptFunc, from int) {
	if len(s.open) >= maxScriptNesting {
		return
	}
	fn.depth, fn.from = s.depth, from
	if len(s.open) == 0 {
		s.outer = fn.name
	}
	s.open = append(s.open, fn)
}

// closeDeeper closes every open function whose body the brace at col ended.
func (s *scriptScanner) closeDeeper(code string, col, lineNum int) {
	for i := 0; i < maxScriptNesting && len(s.open) > 0; i++ {
		top := &s.open[len(s.open)-1]
		if s.depth >= top.depth {
			return
		}
		s.observeBody(top, code[top.from:col], lineNum)
		noteFunction(s.rep, top.name, top.start, lineNum)
		if length := lineNum - top.start + 1; length > s.maxLOC {
			recordViolation(s.rep, "HISS-04", s.rel, top.start, top.name,
				fmt.Sprintf("Function '%s' (%d LOC) exceeds HISS-04 / NASA Rule 4 limit of %d LOC", top.name, length, s.maxLOC))
		}
		top.calls.report(s.rep, s.rel)
		s.open = s.open[:len(s.open)-1]
	}
}

// observeBody hands one line's text of a function body to its self-call tracker.
func (s *scriptScanner) observeBody(fn *scriptFunc, body string, lineNum int) {
	fn.calls.observeBinding(body)
	fn.calls.observeCall(body, lineNum)
}

// misread reports, at the end of the file, whether the braces were misread somewhere: a
// function or span still open, or a closing brace with nothing open. Such a file is declined
// rather than counted as read, which a gate would take for clean.
func (s *scriptScanner) misread() bool {
	return s.unbalanced || s.depth != 0 || len(s.open) > 0 || s.strip.open()
}

// checkInvariants inspects one stripped line. defined is the function the line opens, so a
// method named eval is not reported as a call of the builtin.
func (s *scriptScanner) checkInvariants(code string, lineNum int, defined string) {
	if cFamilyUnboundedLoop.MatchString(code) {
		recordViolation(s.rep, "HISS-02", s.rel, lineNum, "", "Unbounded loop in JavaScript without an explicit scalar bound")
	}
	if defined != "eval" && hasCall(code, "eval", isScriptSelectorByte) {
		recordViolation(s.rep, "HISS-08", s.rel, lineNum, "", "Banned dynamic eval() execution in JavaScript")
	}
	if hasCall(code, "Function", isScriptSelectorByte) {
		recordViolation(s.rep, "HISS-08", s.rel, lineNum, "", "Banned Function constructor compiles code at run time")
	}
	if scriptImpliedEval.MatchString(code) {
		recordViolation(s.rep, "HISS-08", s.rel, lineNum, "", "setTimeout or setInterval given a string evaluates it as code")
	}
	s.checkErrors(code, lineNum)
}

// checkErrors reports the HISS-07 forms: a swallowed error and an abort outside the entry point.
func (s *scriptScanner) checkErrors(code string, lineNum int) {
	trimmed := strings.TrimSpace(code)
	if s.catchAt > 0 && trimmed != "" {
		if strings.HasPrefix(trimmed, "}") {
			recordViolation(s.rep, "HISS-07", s.rel, s.catchAt, "", "Empty catch block swallows the error; handle, rethrow or wrap it")
		}
		s.catchAt = 0
	}
	if scriptEmptyCatch.MatchString(code) {
		recordViolation(s.rep, "HISS-07", s.rel, lineNum, "", "Empty catch block swallows the error; handle, rethrow or wrap it")
	}
	if scriptCatchOpens.MatchString(code) {
		s.catchAt = lineNum
	}
	if scriptSwallowedRejection.MatchString(code) {
		recordViolation(s.rep, "HISS-07", s.rel, lineNum, "", ".catch with an empty handler swallows the rejection; handle it or let it propagate")
	}
	if m := scriptProcessExit.FindStringSubmatch(code); m != nil && !s.mayAbort() {
		recordViolation(s.rep, "HISS-07", s.rel, lineNum, "",
			"process."+m[1]+" ends the process from library code; return an error to the entry point instead")
	}
}

// mayAbort applies the HISS-07 abort policy (owner decision Q-014) to the current line: a test
// file, module scope and a top-level function named main are the script's entry point.
func (s *scriptScanner) mayAbort() bool {
	return s.test || s.outer == "" || s.outer == "main"
}
