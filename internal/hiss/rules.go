package hiss

import (
	"fmt"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/cordanaLLM/praetor/internal/util"
)

// The non-Go rules matched exact strings, so one space silenced them: `while ( 1 )`,
// `for ( ;; )`, `loop{`, a labelled `'outer: loop {`, `x. unwrap()` and `while True :` all
// passed a gate that claims to catch them, and a reformatter could erase a finding without
// changing behaviour. These patterns tolerate arbitrary internal spacing instead. They are
// still line matchers rather than parsers, which bounds what they can claim, but they can no
// longer be defeated by whitespace alone.
var (
	// cFamilyUnboundedLoop is the unbounded loop shape shared by C, C++ and JavaScript, whose
	// while and for statements have the same syntax, so both scanners read one pattern.
	cFamilyUnboundedLoop = regexp.MustCompile(`\bwhile\s*\(\s*(?:1|true)\s*\)|\bfor\s*\(\s*;\s*;\s*\)`)
	rustUnboundedLoop    = regexp.MustCompile(`(?:^|[^\w])loop\s*\{|(?:^|[^\w])while\s+true\s*\{`)
	rustUnwrapCall       = regexp.MustCompile(`\.\s*unwrap\s*\(\s*\)`)
	rustExpectCall       = regexp.MustCompile(`\.\s*expect\s*\(`)
	rustUnsafeBlock      = regexp.MustCompile(`(?:^|[^\w])unsafe\s*\{`)
	pythonWhileTrue      = regexp.MustCompile(`^\s*while\s+True\s*:`)
	pythonBareExcept     = regexp.MustCompile(`^\s*except\s*:`)

	// rustFnHeader is the Rust function-header grammar, matched against a line with literals
	// stripped: an optional visibility (pub, pub(crate), pub(super), pub(in path)), any run of
	// qualifiers (const, async, unsafe, safe, default, extern with or without its ABI string),
	// then fn and the name. A fixed prefix list silently skipped every header it did not
	// spell out, so const, unsafe, extern "C" and pub(super) functions were never measured.
	//
	// Outer attributes may precede the header on the same line (`#[tokio::main] async fn
	// main() {`); anchoring the grammar at fn's visibility left such a function unmeasured and,
	// for fn main, not recognised as the entry point.
	//
	// The first group is the qualifier run, which says whether the function is unsafe (HISS-09);
	// the second is the name.
	rustFnHeader = regexp.MustCompile(`^(?:#\s*\[[^\]]*\]\s*)*(?:pub\s*(?:\([^)]*\))?\s+)?((?:(?:const|async|unsafe|safe|default|extern)\s+)*)fn\s+(?:r#)?([A-Za-z_]\w*)`)
	// rustTestAttr opens an item compiled only for tests: #[cfg(test)], #[test] and a
	// path-qualified test attribute such as #[tokio::test].
	rustTestAttr = regexp.MustCompile(`^#\s*\[\s*(?:cfg\s*\(\s*test\s*\)|(?:[A-Za-z_]\w*\s*::\s*)*test\s*[\](])`)
	// rustInnerTestAttr is #![cfg(test)], which makes the whole file test code.
	rustInnerTestAttr = regexp.MustCompile(`^#\s*!\s*\[\s*cfg\s*\(\s*test\s*\)\s*\]`)
	// rustAbortMacro and rustProcessExit are the Rust abort forms of the HISS-07 abort policy.
	// A macro call needs its delimiter after the bang, so an identifier compared with `!=`
	// (`if todo != 0`) is not a call.
	rustAbortMacro  = regexp.MustCompile(`\b(panic|todo|unimplemented|unreachable)\s*!\s*[(\[{]`)
	rustProcessExit = regexp.MustCompile(`\bprocess\s*::\s*(exit|abort)\s*\(`)
	// pythonSysExit is the Python abort form of the HISS-07 abort policy.
	pythonSysExit = regexp.MustCompile(`\bsys\s*\.\s*exit\s*\(`)
	// pythonEntry opens a script's entry point: the `if __name__ == "__main__":` block, or a
	// top-level def main, the function a console-script entry point names. It is matched on
	// the raw line because the literal stripper removes the __main__ string.
	pythonEntry = regexp.MustCompile(`^(?:if\s+(?:__name__\s*==\s*['"]__main__['"]|['"]__main__['"]\s*==\s*__name__)\s*:|(?:async\s+)?def\s+main\s*\()`)
)

const (
	// maxPendingHeaderLines bounds how far a wrapped function signature may run before
	// its opening brace (HISS-02).
	maxPendingHeaderLines = 32
	// maxSafetyLookback bounds the comment block searched for a SAFETY proof (HISS-02).
	maxSafetyLookback = 8
	// maxPythonNesting bounds the stack of open Python functions (HISS-02).
	maxPythonNesting = 64
)

// braceTracker follows one brace-delimited function at a time for the line-based
// scanners and records HISS-04 when the function closes. Braces inside string and
// character literals and line comments are never counted.
type braceTracker struct {
	rel    string
	rep    *ScanReport
	maxLOC int
	// scopeOnly follows a brace-delimited item without measuring it, for callers that only
	// need to know whether a line lies inside the item (the Rust test scope).
	scopeOnly bool

	inFunc     bool
	pending    bool
	pendingAge int
	// header is the 1-based line the function signature starts on, recorded by noteFunction so
	// FunctionSpan.Start names the header line rather than the opening brace line.
	header int
	// start is the line the body opens on -- the line carrying the opening brace, never the
	// line the signature starts on. HISS-04 measures the brace-delimited body, so a
	// signature parked on its own line above the brace lends the function no extra LOC.
	start int
	name  string
	level int
}

// observe feeds one line, already stripped of literals and comments by the caller so the
// tracker shares the caller's cross-line comment state. isHeader reports whether the line
// opens a function signature; name is the function name for that case.
func (t *braceTracker) observe(code, trimmed string, idx int, isHeader bool, name string) {
	opens := strings.Count(code, "{")
	closes := strings.Count(code, "}")
	switch {
	case t.inFunc:
		t.level += opens - closes
		if t.level <= 0 {
			t.finish(idx + 1)
		}
	case t.pending:
		t.pendingAge++
		if opens > 0 {
			t.enter(idx, opens-closes)
			return
		}
		if endsStatement(trimmed) || t.pendingAge >= maxPendingHeaderLines {
			t.pending = false
		}
	case isHeader:
		if endsStatement(trimmed) {
			return // a declaration without a body (trait method, prototype)
		}
		t.name = name
		t.header = idx + 1
		if opens > 0 {
			t.enter(idx, opens-closes)
			return
		}
		t.pending = true
		t.pendingAge = 0
	}
}

func endsStatement(trimmed string) bool {
	line := strings.TrimSpace(trimmed)
	line = strings.TrimSpace(strings.TrimSuffix(line, `\`))
	return strings.HasSuffix(line, ";")
}

// enter opens the body at idx, the line the first unbalanced brace is on. Detection may
// have happened earlier, on the signature line, so that a definition whose signature and
// brace sit on separate lines is still found and still named; the measurement window
// nonetheless starts here, at the brace. Keeping the two apart is what lets the scanner
// recognise more functions without silently inflating any of them.
func (t *braceTracker) enter(idx, level int) {
	t.inFunc = true
	t.pending = false
	t.start = idx + 1
	t.level = level
	if t.level <= 0 {
		t.finish(idx + 1)
	}
}

// finish records the completed function span from its header line and enforces HISS-04 from its brace line.
func (t *braceTracker) finish(endLine int) {
	t.inFunc = false
	if t.scopeOnly {
		return
	}
	noteFunction(t.rep, t.name, t.header, endLine)
	funcLen := endLine - t.start + 1
	if funcLen > t.maxLOC {
		recordViolation(t.rep, "HISS-04", t.rel, t.start, t.name,
			fmt.Sprintf("Function '%s' (%d LOC) exceeds HISS-04 / NASA Rule 4 limit of %d LOC", t.name, funcLen, t.maxLOC))
	}
}

// LexClass identifies the lexical state at the start or end of a source line.
type LexClass int

const (
	// LexBlank marks a line holding whitespace only outside multi-line constructs.
	LexBlank LexClass = iota
	// LexCode marks executable source code outside comments and literals.
	LexCode
	// LexLineComment marks a single-line comment such as // or #.
	LexLineComment
	// LexBlockComment marks a /* ... */ block comment.
	LexBlockComment
	// LexDocComment marks a ///, //!, /** ... */ or /*! ... */ documentation comment.
	LexDocComment
	// LexLiteralString marks an ordinary or byte string literal.
	LexLiteralString
	// LexLiteralRawString marks a Rust, C++ or Go raw string literal.
	LexLiteralRawString
	// LexLiteralTripleString marks a triple-quoted or multiline string literal.
	LexLiteralTripleString
	// LexLiteralTemplate marks a JavaScript or TypeScript template literal.
	LexLiteralTemplate
	// LexLiteralChar marks a character literal.
	LexLiteralChar
	// LexLiteralRegex marks a JavaScript or TypeScript regular expression literal.
	LexLiteralRegex
)

// LexLine records the stripped code and lexical state of one source line from a forward file scan.
type LexLine struct {
	Code            string
	StartClass      LexClass
	EndClass        LexClass
	StartBlockDepth int
	EndBlockDepth   int
	StartInLiteral  bool
	EndInLiteral    bool
	Unclosed        bool
	CommentOnly     bool
}

// lineLexTracker accumulates the lexical classification of a single line during literalStripper.strip.
type lineLexTracker struct {
	startClass LexClass
	lastClass  LexClass
	sawComment bool
	sawCode    bool
	unclosed   bool
}

// noteToken records one token's lexical class and whether it is a comment token.
func (t *lineLexTracker) noteToken(class LexClass, isComment bool) {
	if t.startClass == LexBlank {
		t.startClass = class
	}
	t.lastClass = class
	if isComment {
		t.sawComment = true
	} else {
		t.sawCode = true
	}
}

// literalSyntax describes how a language delimits strings and comments.
type literalSyntax struct {
	// apostropheIsString treats '...' as a string (Python, JavaScript) rather than a character
	// literal (C, Rust), where a lone apostrophe such as the lifetime 'a is plain text.
	apostropheIsString bool
	lineComment        string
	// tripleQuotes opens Python's """ and ''' strings, which span lines.
	tripleQuotes bool
	// tripleDoubleQuotes enables Java, Kotlin and Swift triple double quotes only.
	tripleDoubleQuotes bool
	// kotlinStrings and swiftStrings enable their interpolation and delimiter rules.
	kotlinStrings bool
	swiftStrings  bool
	// pythonStrings recognises prefixes and replacement fields.
	pythonStrings bool
	// blockComments opens a C-style /* */ comment, which spans lines.
	blockComments bool
	// nestedBlockComments nests /* ... */ block comments (Rust).
	nestedBlockComments bool
	// multilineQuotes carries an ordinary "..." string across raw line ends (Rust).
	multilineQuotes bool
	// rustStrings opens Rust r"...", r#"..."#, b"...", br"...", br#"..."# and b'x' literals.
	rustStrings bool
	// cppRawStrings opens C++ R"delim(...)delim" raw string literals.
	cppRawStrings bool
	// backtickStrings opens Go raw strings without interpreting escapes.
	backtickStrings bool
	// translatedEscapes rejects Java source Unicode translation pending a preprocessing pass.
	translatedEscapes bool
	// cTrigraphs marks C translation-phase trigraphs as unsupported.
	cTrigraphs bool
	// cSplices rejects preprocessing line splices outside ordinary strings.
	cSplices bool
	// multiCharacter permits native multicharacter constants.
	multiCharacter bool
	// legacyComments recognises ECMAScript Annex B HTML-like line comments.
	legacyComments bool
	// templates opens a JavaScript `...` template literal, which spans lines and carries code
	// in its ${...} substitutions. That code is kept, so a call written there is still seen.
	templates bool
	// regexLiterals skips a JavaScript /.../ regular expression literal where an expression may
	// start, so a quote or brace inside the pattern is never read as code.
	regexLiterals bool
	// quotesEndWithLine reads a quote that does not close on its line, on a line that does not
	// end in a backslash, as text rather than as a string running to the end of the line. A
	// JavaScript string cannot span lines, and the apostrophe of JSX text (<p>Don't</p>) opens
	// none; read as a string, it swallowed the braces after it.
	quotesEndWithLine bool
	// urlText reads the // of a URL written in JSX text (<p>see https://example.com {n}</p>) as
	// text rather than as a line comment: the // follows a word and a colon directly and a
	// non-blank byte follows it, which a comment written in code does not.
	urlText bool
}

var (
	cLikeSyntax  = literalSyntax{lineComment: "//", blockComments: true, multiCharacter: true}
	goSyntax     = literalSyntax{lineComment: "//", blockComments: true, backtickStrings: true}
	cppSyntax    = literalSyntax{lineComment: "//", blockComments: true, cppRawStrings: true, cSplices: true, multiCharacter: true}
	rustSyntax   = literalSyntax{lineComment: "//", blockComments: true, nestedBlockComments: true, multilineQuotes: true, rustStrings: true}
	pythonSyntax = literalSyntax{apostropheIsString: true, lineComment: "#", tripleQuotes: true, pythonStrings: true}
	javaSyntax   = literalSyntax{lineComment: "//", blockComments: true, tripleDoubleQuotes: true, translatedEscapes: true}
	kotlinSyntax = literalSyntax{lineComment: "//", blockComments: true, nestedBlockComments: true, tripleDoubleQuotes: true, kotlinStrings: true}
	swiftSyntax  = literalSyntax{lineComment: "//", blockComments: true, nestedBlockComments: true, tripleDoubleQuotes: true, swiftStrings: true}

	scriptSyntax = literalSyntax{apostropheIsString: true, lineComment: "//", blockComments: true,
		templates: true, regexLiterals: true, quotesEndWithLine: true, urlText: true, legacyComments: true}
)

const (
	// templateFence is the delimiter of a JavaScript template literal.
	templateFence = "`"
	// maxTemplateNesting bounds embedded expressions across interpolated literals (HISS-02).
	// A deeper expression rejects documentation association through EOF.
	maxTemplateNesting = 32
	// maxCPPRawDelimiterBytes bounds a C++ raw-string d-char-sequence per [lex.string].
	maxCPPRawDelimiterBytes = 16
)

// blockCommentClose ends a C-style block comment, the one fence that honours no escapes.
const blockCommentClose = "*/"

// literalState holds the closing delimiter and text rules of one open literal.
type literalState struct {
	// fence is the delimiter that closes the span currently open, or empty outside one.
	fence string
	// fenceClass names the lexical class of the currently open multi-line span.
	fenceClass LexClass
	// rawFence reports that fence closes a raw string literal without backslash escaping.
	rawFence bool
	// interpolation opens an embedded expression; escape prefixes an escaped text byte.
	interpolation string
	escape        string
	// formatText closes a Python replacement field rather than the whole literal.
	formatText bool
}

// literalSubstitution saves a literal while its embedded expression is scanned as code.
type literalSubstitution struct {
	literalState
	depth int
	close byte
}

// literalStripper strips literals and comments across a whole file, carrying the state that
// a single line cannot hold: a C-style block comment and a Python triple-quoted string both
// span lines, and so does a quoted string whose line ends in a backslash.
//
// Without that state every construct inside a multi-line comment or docstring was scanned as
// code, so documenting a counter-example reported it as a live infraction. A gate that
// punishes explaining the thing it forbids teaches people to stop explaining it.
type literalStripper struct {
	syn literalSyntax
	literalState
	// blockDepth is the nesting depth of open /* ... */ block comments.
	blockDepth int
	// substitutions holds, per open ${...} of a template literal, the depth of the braces the
	// substitution's own code opened, so its closing brace is told apart from theirs.
	substitutions []literalSubstitution
	// unclosed reports a single-line quoted literal that ended without its closing quote.
	unclosed bool
	// line tracks the per-line lexical classification during strip.
	line lineLexTracker
	// quarantined suppresses documentation after a recognised unsupported construct.
	quarantined bool
}

// open reports whether a multi-line span is still open: a comment, a string or a template
// literal, including one whose ${...} substitution is being read as code.
func (s *literalStripper) open() bool {
	return s.fence != "" || s.blockDepth > 0 || len(s.substitutions) > 0
}

// inLiteral reports whether a multi-line literal or template substitution is currently open.
func (s *literalStripper) inLiteral() bool {
	return (s.fence != "" && s.fence != blockCommentClose) || len(s.substitutions) > 0
}

// activeLiteralClass returns the LexClass of the currently open multi-line literal.
func (s *literalStripper) activeLiteralClass() LexClass {
	if s.fence != "" {
		return s.fenceClass
	}
	if len(s.substitutions) > 0 {
		return s.substitutions[len(s.substitutions)-1].fenceClass
	}
	return LexBlank
}

// scanLine strips literals and comments from line and returns its recorded LexLine state.
func (s *literalStripper) scanLine(line string) LexLine {
	if s.quarantined || s.unsupportedSourceLine(line) {
		s.rejectLexicalForm()
		return LexLine{StartClass: LexCode, EndClass: LexCode, Unclosed: true}
	}
	startBlock := s.blockDepth
	startLit := s.inLiteral()
	carry := s.carryClass(startBlock, startLit)
	s.line = lineLexTracker{
		startClass: carry,
		lastClass:  carry,
		sawComment: startBlock > 0,
		sawCode:    startLit,
	}
	code := s.strip(line)
	endLit := s.inLiteral()
	endClass := s.resolveEndClass(endLit)
	return LexLine{
		Code:            code,
		StartClass:      s.line.startClass,
		EndClass:        endClass,
		StartBlockDepth: startBlock,
		EndBlockDepth:   s.blockDepth,
		StartInLiteral:  startLit,
		EndInLiteral:    endLit,
		Unclosed:        s.line.unclosed,
		CommentOnly:     s.line.sawComment && !s.line.sawCode && !startLit && !s.line.unclosed,
	}
}

// unsupportedSourceLine detects translation phases not modelled by the lexical pass.
func (s *literalStripper) unsupportedSourceLine(line string) bool {
	if s.syn.translatedEscapes && strings.Contains(line, "\\u") {
		return true
	}
	return s.syn.cTrigraphs && containsCTrigraph(line)
}

// containsCTrigraph recognises the nine C trigraph spellings without translating source positions.
func containsCTrigraph(line string) bool {
	for i := 0; i+2 < len(line); i++ {
		if strings.HasPrefix(line[i:], "??") && strings.ContainsRune("=/'()!<>-", rune(line[i+2])) {
			return true
		}
	}
	return false
}

// carryClass returns the lexical class carried into the start of a line from an open multi-line span.
func (s *literalStripper) carryClass(startBlock int, startLit bool) LexClass {
	if startBlock > 0 {
		return s.fenceClass
	}
	if startLit {
		return s.activeLiteralClass()
	}
	return LexBlank
}

// resolveEndClass returns the lexical class at the end of the line just scanned.
func (s *literalStripper) resolveEndClass(endLit bool) LexClass {
	if s.blockDepth > 0 {
		return s.fenceClass
	}
	if endLit {
		return s.activeLiteralClass()
	}
	if s.line.unclosed {
		return LexLiteralString
	}
	return s.line.lastClass
}

// noteCarriedFence updates the per-line tracker when consuming an active multi-line fence.
func (s *literalStripper) noteCarriedFence() {
	if s.fence == blockCommentClose {
		s.line.noteToken(s.fenceClass, true)
		return
	}
	s.line.noteToken(s.activeLiteralClass(), false)
}

// lineCommentClass classifies a line comment token as a doc comment or ordinary line comment.
func lineCommentClass(syn literalSyntax, rest string) LexClass {
	if syn.lineComment == "//" && (strings.HasPrefix(rest, "///") || strings.HasPrefix(rest, "//!")) {
		return LexDocComment
	}
	return LexLineComment
}

// blockCommentClass classifies a block comment opener as a doc comment or ordinary block comment.
func blockCommentClass(rest string) LexClass {
	if (strings.HasPrefix(rest, "/**") && !strings.HasPrefix(rest, "/**/")) || strings.HasPrefix(rest, "/*!") {
		return LexDocComment
	}
	return LexBlockComment
}

// strip returns line with the contents of literals and comments removed, updating the
// cross-line state. Each line must be passed exactly once and in order.
func (s *literalStripper) strip(line string) string {
	if s.syn.legacyComments && strings.HasPrefix(line, "#!") {
		s.line.noteToken(LexLineComment, false)
		return ""
	}
	var b strings.Builder
	for i := 0; i < len(line); {
		if s.quarantined {
			return ""
		}
		if s.fence != "" {
			s.noteCarriedFence()
			i = s.consumeFence(line, i)
			continue
		}
		if next, opened := s.openFence(line, i); opened {
			i = next
			continue
		}
		if s.startsLineComment(line, i) {
			next, stop := s.consumeLineComment(line, i)
			if stop {
				return b.String()
			}
			i = next
			continue
		}
		i = s.copyOne(line, i, &b)
	}
	return b.String()
}

// consumeLineComment consumes a comment or preserves the slash pair of a JSX URL.
func (s *literalStripper) consumeLineComment(line string, i int) (int, bool) {
	if s.urlAt(line, i) {
		s.line.noteToken(LexCode, false)
		return i + len(s.syn.lineComment), false
	}
	s.line.noteToken(lineCommentClass(s.syn, line[i:]), true)
	if s.syn.cSplices && strings.HasSuffix(strings.TrimSuffix(line, "\r"), "\\") {
		s.rejectLexicalForm()
	}
	return len(line), true
}

// startsLineComment recognises native line comments and ECMAScript legacy line forms.
func (s *literalStripper) startsLineComment(line string, i int) bool {
	if s.syn.legacyComments && strings.HasPrefix(line[i:], "<!--") {
		return true
	}
	if s.syn.legacyComments && strings.TrimSpace(line[:i]) == "" && strings.HasPrefix(line[i:], "-->") {
		return true
	}
	return s.syn.lineComment != "" && strings.HasPrefix(line[i:], s.syn.lineComment)
}

// rejectLexicalForm fails closed until EOF after an unsupported source construct.
func (s *literalStripper) rejectLexicalForm() {
	s.quarantined = true
	s.unclosed = true
	s.line.unclosed = true
	s.line.noteToken(LexCode, false)
}

// consumeFence skips bytes until the open span's closing delimiter, which may not appear on
// this line at all. Inside a string a backslash escapes the byte after it, so `\"""` does not
// close a triple-quoted string. A single-quoted string carried here by a trailing backslash
// ends with this line unless the line ends in a backslash too, so a string the stripper
// misreads can never hold the rest of the file.
func (s *literalStripper) consumeFence(line string, i int) int {
	if s.interpolation != "" {
		return s.consumeInterpolated(line, i)
	}
	if s.fence == blockCommentClose {
		return s.consumeBlockComment(line, i)
	}
	if s.rawFence {
		return s.consumeRawString(line, i)
	}
	end, closed, carried := scanQuoted(line, i, s.fence)
	if closed || (len(s.fence) == 1 && !carried && !s.syn.multilineQuotes) {
		if !closed {
			s.unclosed = true
			s.line.unclosed = true
		}
		s.fence = ""
	}
	return end
}

// consumeBlockComment advances through an open /* ... */ comment, tracking nesting when enabled.
func (s *literalStripper) consumeBlockComment(line string, i int) int {
	if !s.syn.nestedBlockComments {
		if idx := strings.Index(line[i:], blockCommentClose); idx >= 0 {
			s.blockDepth = 0
			s.fence = ""
			return i + idx + len(blockCommentClose)
		}
		return len(line)
	}
	if s.blockDepth <= 0 {
		s.blockDepth = 1
	}
	for j := i; j < len(line); j++ {
		if strings.HasPrefix(line[j:], "/*") {
			s.blockDepth++
			j++
		} else if strings.HasPrefix(line[j:], blockCommentClose) {
			s.blockDepth--
			if s.blockDepth == 0 {
				s.fence = ""
				return j + len(blockCommentClose)
			}
			j++
		}
	}
	return len(line)
}

// skipRawString skips a raw string body starting at i with closing delimiter fence.
func (s *literalStripper) skipRawString(line string, i int, fence string) int {
	s.fence = fence
	s.fenceClass = LexLiteralRawString
	s.rawFence = true
	return s.consumeRawString(line, i)
}

// consumeRawString advances through a raw string body up to s.fence without backslash escapes.
func (s *literalStripper) consumeRawString(line string, i int) int {
	if idx := strings.Index(line[i:], s.fence); idx >= 0 {
		end := i + idx + len(s.fence)
		s.fence = ""
		s.rawFence = false
		return end
	}
	return len(line)
}

// openFence reports whether a multi-line span starts at i and records its closing delimiter.
// Python triple quotes are checked before ordinary quotes so a docstring is never read as an
// empty string followed by code.
func (s *literalStripper) openFence(line string, i int) (int, bool) {
	if next, ok := s.openLanguageString(line, i); ok {
		return next, true
	}
	if fence, _ := s.syn.quoteAt(line, i); len(fence) == 3 {
		s.fence = fence
		s.fenceClass = LexLiteralTripleString
		s.line.noteToken(LexLiteralTripleString, false)
		return i + len(fence), true
	}
	if s.syn.blockComments && strings.HasPrefix(line[i:], "/*") {
		s.fence = blockCommentClose
		s.blockDepth = 1
		s.fenceClass = blockCommentClass(line[i:])
		s.line.noteToken(s.fenceClass, true)
		return i + 2, true
	}
	if s.syn.backtickStrings && line[i] == '`' {
		s.line.noteToken(LexLiteralRawString, false)
		return s.skipRawString(line, i+1, "`"), true
	}
	if s.syn.templates && line[i] == '`' {
		s.fence = templateFence
		s.fenceClass = LexLiteralTemplate
		s.interpolation = "${"
		s.escape = "\\"
		s.line.noteToken(LexLiteralTemplate, false)
		return i + 1, true
	}
	return s.openPrefixedLiteral(line, i)
}

// openLanguageString dispatches languages with interpolated or extended quoted literals.
func (s *literalStripper) openLanguageString(line string, i int) (int, bool) {
	switch {
	case s.syn.pythonStrings:
		return s.openPythonString(line, i)
	case s.syn.swiftStrings:
		return s.openSwiftString(line, i)
	case s.syn.kotlinStrings && line[i] == '"':
		fence, width := s.syn.quoteAt(line, i)
		escape := "\\"
		if width == 3 {
			escape = ""
		}
		return s.startInterpolated(i+width, fence, "${", escape), true
	default:
		return i, false
	}
}

// startInterpolated installs a literal's text rules before the next bounded scanner step.
func (s *literalStripper) startInterpolated(body int, fence, interpolation, escape string) int {
	class := LexLiteralString
	if strings.HasPrefix(fence, "\"\"\"") {
		class = LexLiteralTripleString
	}
	s.literalState = literalState{fence: fence, fenceClass: class, interpolation: interpolation, escape: escape}
	s.line.noteToken(class, false)
	return body
}

// openPythonString recognises the reference prefix allow-list, including f/t replacement fields.
func (s *literalStripper) openPythonString(line string, i int) (int, bool) {
	at := i
	prefix := ""
	if line[i] != '\'' && line[i] != '"' {
		if i > 0 && isIdentByte(line[i-1]) {
			return i, false
		}
		at, prefix = pythonPrefixEnd(line, i)
		if at == i {
			return i, false
		}
	}
	fence, width := s.syn.quoteAt(line, at)
	if fence == "" {
		return i, false
	}
	interpolation := ""
	if strings.ContainsAny(prefix, "ft") {
		interpolation = "{"
	}
	if interpolation != "" {
		return s.startInterpolated(at+width, fence, interpolation, "\\"), true
	}
	if width == 3 {
		s.literalState = literalState{fence: fence, fenceClass: LexLiteralTripleString}
		s.line.noteToken(LexLiteralTripleString, false)
		return at + width, true
	}
	s.line.noteToken(LexLiteralString, false)
	return s.skipQuoted(line, at), true
}

// pythonPrefixEnd consumes only prefixes permitted by Python lexical analysis section 2.5.2.
func pythonPrefixEnd(line string, i int) (int, string) {
	for _, p := range [...]string{"br", "rb", "fr", "rf", "tr", "rt", "r", "u", "b", "f", "t"} {
		end := i + len(p)
		if end < len(line) && strings.EqualFold(line[i:end], p) && strings.ContainsRune("\"'", rune(line[end])) {
			return end, p
		}
	}
	return i, ""
}

// openSwiftString recognises ordinary and multiline strings with matching extended delimiters.
func (s *literalStripper) openSwiftString(line string, i int) (int, bool) {
	at := i
	for at < len(line) && line[at] == '#' {
		at++
	}
	if at == len(line) || line[at] != '"' {
		return i, false
	}
	hashes := line[i:at]
	fence, width := s.syn.quoteAt(line, at)
	escape := "\\" + hashes
	return s.startInterpolated(at+width, fence+hashes, escape+"(", escape), true
}

// openPrefixedLiteral recognises Rust r/br/b string and byte-char literals and C++ R raw strings.
func (s *literalStripper) openPrefixedLiteral(line string, i int) (int, bool) {
	if i > 0 && (isIdentByte(line[i-1]) || line[i-1] == '\'') {
		return i, false
	}
	if s.syn.rustStrings {
		if next, ok := s.openRustPrefixedLiteral(line, i); ok {
			return next, true
		}
	}
	if s.syn.cppRawStrings {
		if fence, bodyStart, ok := cppRawStringFence(line, i); ok {
			s.line.noteToken(LexLiteralRawString, false)
			return s.skipRawString(line, bodyStart, fence), true
		}
	}
	return i, false
}

// openRustPrefixedLiteral recognises Rust byte, C, raw, raw byte and raw C literals.
func (s *literalStripper) openRustPrefixedLiteral(line string, i int) (int, bool) {
	if strings.HasPrefix(line[i:], "b'") {
		if w := charLiteralWidth(line, i+1); w > 0 {
			s.line.noteToken(LexLiteralChar, false)
			return i + 1 + w, true
		}
	}
	if strings.HasPrefix(line[i:], "b\"") || strings.HasPrefix(line[i:], "c\"") {
		s.line.noteToken(LexLiteralString, false)
		return s.skipQuoted(line, i+1), true
	}
	if fence, bodyStart, ok := rustRawStringFence(line, i); ok {
		s.line.noteToken(LexLiteralRawString, false)
		return s.skipRawString(line, bodyStart, fence), true
	}
	return i, false
}

// rustRawStringFence matches Rust r, br or cr raw-string openers at i.
func rustRawStringFence(line string, i int) (string, int, bool) {
	prefix := 0
	if line[i] == 'r' {
		prefix = 1
	} else if strings.HasPrefix(line[i:], "br") || strings.HasPrefix(line[i:], "cr") {
		prefix = 2
	}
	if prefix == 0 {
		return "", 0, false
	}
	j := i + prefix
	for j < len(line) && line[j] == '#' {
		j++
	}
	if j >= len(line) || line[j] != '"' {
		return "", 0, false
	}
	hashes := j - (i + prefix)
	return "\"" + strings.Repeat("#", hashes), j + 1, true
}

// cppRawStringFence matches a C++ R"delim(...)" raw string opener (with optional L, u8, u, U prefix) at i.
func cppRawStringFence(line string, i int) (string, int, bool) {
	delimStart := cppRawPrefixEnd(line, i)
	if delimStart < 0 {
		return "", 0, false
	}
	for k := 0; k <= maxCPPRawDelimiterBytes && delimStart+k < len(line); k++ {
		ch := line[delimStart+k]
		if ch == '(' {
			delim := line[delimStart : delimStart+k]
			return ")" + delim + "\"", delimStart + k + 1, true
		}
		if isInvalidCPPRawDelimByte(ch) {
			return "", 0, false
		}
	}
	return "", 0, false
}

// cppRawPrefixEnd returns the index after the opening quote of a C++ raw string prefix at i, or -1.
func cppRawPrefixEnd(line string, i int) int {
	for _, p := range [...]string{"R\"", "LR\"", "uR\"", "UR\"", "u8R\""} {
		if strings.HasPrefix(line[i:], p) {
			return i + len(p)
		}
	}
	return -1
}

// isInvalidCPPRawDelimByte reports whether ch is forbidden in a C++ raw-string d-char-sequence.
func isInvalidCPPRawDelimByte(ch byte) bool {
	return ch <= ' ' || ch == '(' || ch == ')' || ch == '\\' || ch > '~'
}

// consumeInterpolated skips literal text, pausing at an embedded expression or closing fence.
func (s *literalStripper) consumeInterpolated(line string, i int) int {
	for j := i; j < len(line); {
		next, stopped := s.stepInterpolated(line, j)
		if stopped {
			return next
		}
		j = next
	}
	return len(line)
}

// stepInterpolated consumes one bounded text token without descending recursively into expressions.
func (s *literalStripper) stepInterpolated(line string, i int) (int, bool) {
	rest := line[i:]
	if strings.HasPrefix(rest, s.fence) {
		end := s.interpolatedFenceEnd(line, i)
		s.closeInterpolated()
		return end, true
	}
	if s.interpolation == "{" && strings.HasPrefix(rest, "{{") {
		return i + 2, false
	}
	if strings.HasPrefix(rest, s.interpolation) {
		return s.openSubstitution(i), true
	}
	if s.syn.pythonStrings && strings.HasPrefix(rest, "\\{") {
		return i + 1, false
	}
	if s.escape != "" && strings.HasPrefix(rest, s.escape) {
		return min(len(line), i+len(s.escape)+1), false
	}
	return i + 1, false
}

// interpolatedFenceEnd consumes Kotlin's maximal trailing quote run as one closing token.
func (s *literalStripper) interpolatedFenceEnd(line string, i int) int {
	end := i + len(s.fence)
	if s.syn.kotlinStrings && s.fence == "\"\"\"" {
		for end < len(line) && line[end] == '"' {
			end++
		}
	}
	return end
}

// closeInterpolated closes literal text or restores the parent of a Python format field.
func (s *literalStripper) closeInterpolated() {
	if s.formatText {
		s.restoreLiteral()
		return
	}
	s.literalState = literalState{}
}

// openSubstitution saves the literal state and starts its embedded expression.
func (s *literalStripper) openSubstitution(i int) int {
	if len(s.substitutions) >= maxTemplateNesting {
		width := len(s.interpolation)
		s.rejectLexicalForm()
		s.literalState = literalState{}
		s.substitutions = nil
		return i + width
	}
	end := i + len(s.interpolation)
	closeByte := byte('}')
	if strings.HasSuffix(s.interpolation, "(") {
		closeByte = ')'
	}
	s.substitutions = append(s.substitutions, literalSubstitution{literalState: s.literalState, close: closeByte})
	s.literalState = literalState{}
	return end
}

// restoreLiteral resumes the literal whose embedded expression just ended.
func (s *literalStripper) restoreLiteral() {
	top := len(s.substitutions) - 1
	s.literalState = s.substitutions[top].literalState
	s.substitutions = s.substitutions[:top]
}

// copyOne copies or skips the token at i, blanking the contents of a single-line literal.
func (s *literalStripper) copyOne(line string, i int, b *strings.Builder) int {
	if s.unsupportedToken(line, i) {
		s.rejectLexicalForm()
		return len(line)
	}
	if next, ok := s.copySubstitutionToken(line[i], i, b); ok {
		return next
	}
	switch c := line[i]; c {
	case '{', '}':
		s.line.noteToken(LexCode, false)
		b.WriteByte(c)
		return i + 1
	case '/':
		if end := s.regexLiteralEnd(line, i, b.String()); end > i {
			s.line.noteToken(LexLiteralRegex, false)
			return end
		}
		s.line.noteToken(LexCode, false)
		b.WriteByte(c)
		return i + 1
	case '"', '\'':
		return s.copyQuote(line, i, b)
	default:
		if c != ' ' && c != '\t' && c != '\r' {
			s.line.noteToken(LexCode, false)
		}
		b.WriteByte(c)
		return i + 1
	}
}

// unsupportedToken identifies Swift regex syntax and native preprocessing splices outside literals.
func (s *literalStripper) unsupportedToken(line string, i int) bool {
	if s.syn.swiftStrings && line[i] == '/' {
		return true
	}
	return s.syn.cSplices && line[i] == '\\' && escapesLineBreak(line, i)
}

// copyQuote skips a quoted string or character, preserving nonliteral apostrophes.
func (s *literalStripper) copyQuote(line string, i int, b *strings.Builder) int {
	fence, width := s.syn.quoteAt(line, i)
	if fence != "" {
		s.line.noteToken(LexLiteralString, false)
		return s.skipQuoted(line, i)
	}
	if width > 0 {
		s.line.noteToken(LexLiteralChar, false)
		return i + width
	}
	s.line.noteToken(LexCode, false)
	b.WriteByte(line[i])
	return i + 1
}

// skipQuoted skips the quoted string opening at i and returns the index past it. A string that
// does not close on its line either continues on the next, when a backslash escapes the line
// break or multilineQuotes holds, or, under quotesEndWithLine, was never a string: the quote is dropped.
func (s *literalStripper) skipQuoted(line string, i int) int {
	end, closed, carried := scanQuoted(line, i+1, line[i:i+1])
	if carried || (!closed && s.syn.multilineQuotes) {
		s.fence = line[i : i+1]
		s.fenceClass = LexLiteralString
		return end
	}
	if !closed {
		s.unclosed = true
		s.line.unclosed = true
		if s.syn.quotesEndWithLine {
			return i + 1
		}
	}
	return end
}

// urlAt reports whether the line comment marker at i is the // of a URL in JSX text under
// urlText: a word and a colon directly before it (https:) and a non-blank byte after it.
func (s *literalStripper) urlAt(line string, i int) bool {
	after := i + len(s.syn.lineComment)
	return s.syn.urlText && i >= 2 && line[i-1] == ':' && isIdentByte(line[i-2]) &&
		after < len(line) && line[after] != ' ' && line[after] != '\t'
}

// copySubstitutionToken tracks delimiters and Python format fields inside embedded code.
func (s *literalStripper) copySubstitutionToken(c byte, i int, b *strings.Builder) (int, bool) {
	if len(s.substitutions) == 0 {
		return i, false
	}
	top := &s.substitutions[len(s.substitutions)-1]
	if c == top.close && top.depth == 0 {
		s.restoreLiteral()
		s.line.noteToken(s.fenceClass, false)
		return i + 1, true
	}
	if c == ':' && top.depth == 0 && s.syn.pythonStrings {
		s.literalState = literalState{fence: "}", fenceClass: top.fenceClass, interpolation: "{", formatText: true}
		return i + 1, true
	}
	if !strings.ContainsRune("()[]{}", rune(c)) {
		return i, false
	}
	if strings.ContainsRune("([{", rune(c)) {
		top.depth++
	} else {
		top.depth--
	}
	s.line.noteToken(LexCode, false)
	b.WriteByte(c)
	return i + 1, true
}

// regexKeywords are the keywords after which a slash starts a regular expression literal rather
// than a division.
var regexKeywords = []string{"return", "typeof", "case", "do", "else", "in", "of", "new", "delete",
	"void", "throw", "yield", "await", "instanceof"}

// regexLiteralEnd returns the index just past a regular expression literal starting at i, or i
// when the slash there is a division or the syntax has no regular expression literals. copied
// is the code copied from the line so far: a slash opens a literal only where an expression may
// start, which is after an operator, an opening bracket, a keyword such as return, or at the
// start of the line. A literal that does not close on its line is a division after all.
func (s *literalStripper) regexLiteralEnd(line string, i int, copied string) int {
	if !s.syn.regexLiterals || !regexMayStart(copied) || closesJSXTag(line, i, copied) {
		return i
	}
	return regexBodyEnd(line, i)
}

// regexBodyEnd returns the index just past the regular expression literal opening at i and its
// flags, or i when the line ends before the closing slash. A slash inside a character class
// does not close the literal.
func regexBodyEnd(line string, i int) int {
	inClass := false
	for j := i + 1; j < len(line); j++ {
		switch c := line[j]; {
		case c == '\\':
			j++
		case c == '[':
			inClass = true
		case c == ']':
			inClass = false
		case c == '/' && !inClass:
			end := j + 1
			for end < len(line) && isIdentByte(line[end]) {
				end++
			}
			return end
		}
	}
	return i
}

// regexMayStart reports whether an expression may start after copied, the code before a slash.
func regexMayStart(copied string) bool {
	prev := strings.TrimRight(copied, " \t")
	if prev == "" {
		return true
	}
	if strings.IndexByte("(,=:[!&|?{;+-*%<>~^", prev[len(prev)-1]) >= 0 {
		return true
	}
	return slices.Contains(regexKeywords, trailingIdent(prev))
}

// closesJSXTag reports whether the slash at i belongs to JSX markup rather than starting a
// regular expression: the `</` of a closing tag or the `/>` of a self-closing one.
func closesJSXTag(line string, i int, copied string) bool {
	return strings.HasSuffix(copied, "<") || (i+1 < len(line) && line[i+1] == '>')
}

// scanQuoted scans a string body from i for its closing delimiter, honouring backslash
// escapes. It returns the index just past the delimiter and closed=true, or len(line) when
// the line ends first, with carried=true when a backslash escapes the line break itself: the
// string then continues on the next line, in Python, C and Rust alike.
func scanQuoted(line string, i int, closing string) (end int, closed, carried bool) {
	for j := i; j < len(line); j++ {
		if line[j] == '\\' {
			if escapesLineBreak(line, j) {
				return len(line), false, true
			}
			j++
			continue
		}
		if strings.HasPrefix(line[j:], closing) {
			return j + len(closing), true, false
		}
	}
	return len(line), false, false
}

// escapesLineBreak reports whether the backslash at j is the last byte of the line, ignoring
// the carriage return of a CRLF line ending.
func escapesLineBreak(line string, j int) bool {
	return j == len(line)-1 || (j == len(line)-2 && line[len(line)-1] == '\r')
}

// charLiteralWidth returns the byte width of a character literal starting at i ('x' or
// '\x'), or 0 when the quote is not a literal.
func charLiteralWidth(line string, i int) int {
	if i+1 >= len(line) {
		return 0
	}
	_, width := utf8.DecodeRuneInString(line[i+1:])
	end := i + 1 + width
	if line[i+1] == '\\' {
		end = characterEscapeEnd(line, i+1)
	}
	if end < len(line) && line[end] == '\'' {
		return end - i + 1
	}
	return 0
}

// characterEscapeEnd locates a quoted escape's end, including Unicode and hexadecimal forms.
func characterEscapeEnd(line string, slash int) int {
	end := min(len(line), slash+2)
	for j := end; j < len(line) && j-slash <= 12; j++ {
		if line[j] == '\'' {
			return j
		}
		if !strings.ContainsRune("0123456789abcdefABCDEFuUx{}_", rune(line[j])) {
			return end
		}
	}
	return end
}

// hasBannedCall reports whether line calls name (name followed by '(') as a whole
// identifier, so fgets( never matches gets( and retrieval( never matches eval(.
//
// Whitespace between the identifier and the parenthesis does not change the call, so
// `gets (buf)` must not escape a rule that catches `gets(buf)`. A leading dot means the name
// resolves to a method rather than the builtin, which is why `interpreter.eval(node)` on a
// hand-written AST walker is not dynamic execution.
func hasBannedCall(line, name string) bool {
	return hasCall(line, name, isSelectorByte)
}

// hasCall reports whether line invokes callee: a whole-identifier occurrence followed, after
// optional blanks, by an argument list, and not preceded by a byte for which excluded reports
// true. The banned-builtin rules and the direct-recursion rule share it so the two can never
// disagree about what a call looks like.
func hasCall(line, callee string, excluded func(byte) bool) bool {
	at := nextIdent(line, callee, 0)
	for i := 0; i < len(line) && at >= 0; i++ {
		if callAt(line, at, callee, excluded) {
			return true
		}
		at = nextIdent(line, callee, at+1)
	}
	return false
}

// callAt reports whether line invokes callee at byte at, by the same test hasCall applies to
// each occurrence; the Rust scanner uses it to place each call against the bindings in scope.
func callAt(line string, at int, callee string, excluded func(byte) bool) bool {
	return identAt(line, at, callee) && (at == 0 || !excluded(line[at-1])) && opensArgs(line, at+len(callee))
}

// identAt reports whether name stands at byte at of text as a whole identifier, with no
// identifier byte on either side: the test nextIdent applies to each match while searching.
func identAt(text string, at int, name string) bool {
	end := at + len(name)
	return name != "" && strings.HasPrefix(text[at:], name) &&
		(at == 0 || !isIdentByte(text[at-1])) && (end == len(text) || !isIdentByte(text[end]))
}

// opensArgs reports whether an argument list opens at after, allowing blanks before it.
func opensArgs(line string, after int) bool {
	for after < len(line) && (line[after] == ' ' || line[after] == '\t') {
		after++
	}
	return after < len(line) && line[after] == '('
}

// nextIdent returns the index of the first occurrence of name at or after from that stands as
// a whole identifier, with no identifier byte on either side, or -1 when there is none.
func nextIdent(text, name string, from int) int {
	if name == "" {
		return -1
	}
	for i := 0; i < len(text) && from <= len(text); i++ {
		pos := strings.Index(text[from:], name)
		if pos < 0 {
			return -1
		}
		at := from + pos
		if identAt(text, at, name) {
			return at
		}
		from = at + 1
	}
	return -1
}

// containsIdent reports whether name occurs in text as a whole identifier.
func containsIdent(text, name string) bool {
	return nextIdent(text, name, 0) >= 0
}

// isSelectorByte reports whether a name preceded by c is reached through a selector or is
// the tail of a longer identifier, and so does not resolve to the bare name.
func isSelectorByte(c byte) bool {
	return isIdentByte(c) || c == '.'
}

// isPathByte extends isSelectorByte with ':' for Rust, where other::f names a different item.
func isPathByte(c byte) bool {
	return isSelectorByte(c) || c == ':'
}

func isIdentByte(c byte) bool {
	return c == '_' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9')
}

// hasSafetyComment reports whether the unsafe construct at lines[idx] carries a
// SAFETY: proof: on the same line, or in the comment block immediately preceding it.
func hasSafetyComment(lines []string, idx int) bool {
	if strings.Contains(lines[idx], "SAFETY:") {
		return true
	}
	for back := 1; back <= maxSafetyLookback && idx-back >= 0; back++ {
		trimmed := strings.TrimSpace(lines[idx-back])
		if !isCommentLine(trimmed) {
			return false
		}
		if strings.Contains(trimmed, "SAFETY:") {
			return true
		}
	}
	return false
}

func isCommentLine(trimmed string) bool {
	return strings.HasPrefix(trimmed, "//") || strings.HasPrefix(trimmed, "/*") ||
		strings.HasPrefix(trimmed, "*") || strings.HasPrefix(trimmed, "#")
}

// ---------------------------------------------------------------------------
// C / C++ / CUDA
// ---------------------------------------------------------------------------

// nativeScanSyntax shares literal rules while preserving candidate extraction after preprocessing syntax.
// Documentation association rejects unsupported translation; invariant scans still inspect subsequent code.
func nativeScanSyntax(rel string) literalSyntax {
	syn := syntaxForLanguage(util.SourceLanguage(rel))
	syn.cSplices = false
	syn.cTrigraphs = false
	syn.translatedEscapes = false
	return syn
}

// scanNativeLines scans native function bodies and invariants with language-specific literal rules.
func scanNativeLines(lines []string, rel string, rep *ScanReport, opts ScanOptions) {
	t := &braceTracker{rel: rel, rep: rep, maxLOC: opts.MaxFuncLOC}
	// One stripper for the whole file, and one strip per line: the invariant scan, the
	// header test and the brace tracker must all see the same view, or a block comment
	// closes for one of them and not the others.
	stripper := &literalStripper{syn: nativeScanSyntax(rel)}
	gotos := &nativeGotos{rel: rel, rep: rep, allow: opts.CleanupGoto}
	for idx, line := range lines {
		trimmed := strings.TrimSpace(line)
		code := stripper.strip(line)
		scanNativeLineInvariants(code, rel, idx+1, rep)
		inBody := t.inFunc
		gotos.observe(code, idx+1, inBody, t.level)
		name, isHeader := nativeFuncHeader(code)
		isHeader = !t.inFunc && isHeader
		t.observe(code, trimmed, idx, isHeader, name)
		if inBody && !t.inFunc {
			gotos.close()
		}
	}
	// A body the file never closes still ends here: its held gotos are decided on what was
	// seen, so an unbalanced file cannot keep one from being reported.
	gotos.close()
}

// scanNativeLineInvariants inspects one line with literals and comments stripped. HISS-01's
// `goto` depends on the function around it under the cleanup-goto exception, so nativeGotos
// decides it.
func scanNativeLineInvariants(code, rel string, lineNum int, rep *ScanReport) {
	if cFamilyUnboundedLoop.MatchString(code) {
		recordViolation(rep, "HISS-02", rel, lineNum, "", "Legacy unbounded loop in native code")
	}
	if hasBannedCall(code, "gets") {
		recordViolation(rep, "HISS-08", rel, lineNum, "", "Banned unsafe gets() invocation")
	}
	if hasBannedCall(code, "strcpy") {
		recordViolation(rep, "HISS-08", rel, lineNum, "", "Banned unsafe strcpy() invocation; bounded string copy required")
	}
	if hasBannedCall(code, "sprintf") {
		recordViolation(rep, "HISS-08", rel, lineNum, "", "Banned unsafe sprintf() invocation; snprintf required")
	}
}

func nativeFuncHeader(code string) (string, bool) {
	header := strings.TrimSpace(code)
	if brace := strings.Index(header, "{"); brace >= 0 {
		header = strings.TrimSpace(header[:brace])
	}
	if strings.HasPrefix(header, "struct ") || strings.HasPrefix(header, "enum ") ||
		strings.HasPrefix(header, "union ") || strings.HasPrefix(header, "typedef ") ||
		strings.HasPrefix(header, "class ") || strings.HasPrefix(header, "#") {
		return "", false
	}
	name, ok := nativeNameBeforeParen(header)
	if !ok || isNativeControlName(name) {
		return "", false
	}
	return name, true
}

func isNativeControlName(name string) bool {
	switch name {
	case "if", "for", "while", "switch", "catch":
		return true
	default:
		return false
	}
}

func nativeNameBeforeParen(text string) (string, bool) {
	idx := strings.Index(text, "(")
	if idx <= 0 {
		return "", false
	}
	parts := strings.Fields(text[:idx])
	if len(parts) == 0 {
		return "", false
	}
	name := strings.TrimLeft(parts[len(parts)-1], "*&")
	if name == "" || !isNativeDeclaratorName(name) {
		return "", false
	}
	return name, true
}

func isNativeDeclaratorName(name string) bool {
	for idx := 0; idx < len(name); idx++ {
		c := name[idx]
		if isIdentByte(c) || c == ':' || c == '~' {
			continue
		}
		return strings.HasPrefix(name, "operator")
	}
	return true
}

// ---------------------------------------------------------------------------
// Python
// ---------------------------------------------------------------------------

// pythonFunc is one open def, or one open class body when class is set. A class is kept on
// the same stack only so a def knows whether it is a method: it is never measured.
type pythonFunc struct {
	name   string
	start  int
	indent int
	class  bool
	// owner is the class the def sits directly in, empty for a plain function.
	owner string
	// sigOpen holds until the header's closing colon, which may be lines below the def.
	sigOpen bool
	sig     string
	calls   selfCalls
	// outerBinds has bit i set when this scope binds the name of the open function at stack
	// index i below it, so its calls to that name reach its own local.
	outerBinds uint64
	// relayed holds this scope's calls to enclosing functions, passed outward when it closes.
	relayed []relayedCall
	guards  []string
}

// pythonScanner carries the cross-line state of one Python file.
type pythonScanner struct {
	rel      string
	rep      *ScanReport
	maxLOC   int
	stripper literalStripper
	open     []pythonFunc
	lastCode int
	// depth is the bracket depth left open by earlier lines.
	depth int
	// abort and joiner place each line against the HISS-07 abort policy.
	abort        pythonAbortScope
	joiner       pythonLineJoiner
	moduleGuards []string
	io           pythonIOScope
}

// scanPythonLines tracks a stack of open functions so that nested defs do not end
// their enclosing function, and ends every function at its last code line so that
// trailing blank and comment lines are not counted.
func scanPythonLines(lines []string, rel string, rep *ScanReport, opts ScanOptions) {
	s := &pythonScanner{rel: rel, rep: rep, maxLOC: opts.MaxFuncLOC, stripper: literalStripper{syn: pythonSyntax},
		abort: pythonAbortScope{file: isPythonTestPath(rel) || isPythonEntryFile(rel)}}
	for idx, line := range lines {
		s.observe(idx, line)
	}
	s.finishPythonIO()
	s.close(-1)
}

// observe feeds one physical line.
//
// A line that starts inside an open bracket or a string continues the statement above it, so
// its indentation says nothing about which function it belongs to. Reading it as a dedent
// closed a black-formatted function at its `) -> T:` line and a function holding a column-0
// multi-line string at that string, leaving the rest of the body in no function.
//
// A statement such as def, class or return can never start a line inside a bracket, so one met
// at a carried depth means the depth is wrong (a bracket inside an f-string replacement field
// that reuses its quote, say). The depth resets there, so one misread bracket costs at most
// the lines up to the next such statement rather than every function below it.
func (s *pythonScanner) observe(idx int, line string) {
	trimmed := strings.TrimSpace(line)
	if s.depth > 0 && s.stripper.fence == "" && beginsPythonStatement(trimmed) {
		s.depth = 0
	}
	continued := s.stripper.fence != "" || s.depth > 0
	code := s.stripper.strip(line)
	scanPythonLineInvariants(code, s.rel, idx+1, s.rep)
	if !s.abort.observe(line, code, s.joiner.continues(code)) {
		checkPythonAbort(code, s.rel, idx+1, s.rep)
	}
	if trimmed == "" || strings.HasPrefix(trimmed, "#") {
		return
	}
	colon, depth := pythonBrackets(code, s.depth)
	s.depth = depth
	if !continued {
		indent := lineIndent(line)
		s.close(indent)
		s.openScope(trimmed, idx, indent)
	}
	s.lastCode = idx + 1
	s.observeCalls(code, colon, idx+1)
	s.observePythonGuards(code)
	s.observePythonSignals(code, idx+1)
	s.observePythonIO(code, idx+1)
}

// lineIndent returns the width of line's leading spaces and tabs.
func lineIndent(line string) int {
	return len(line) - len(strings.TrimLeft(line, " \t"))
}

// pythonAbortScope decides where the Python abort form is allowed under the HISS-07 abort
// policy (owner decision Q-014): anywhere in a test file or a package's __main__.py, and
// inside the entry point of any other file, which is the top-level
// `if __name__ == "__main__":` block or the top-level def main.
type pythonAbortScope struct {
	file  bool
	entry bool
}

// observe feeds one raw line with its stripped code and reports whether that line may
// abort. A code line at column zero opens the entry point when it is one and closes it
// otherwise; indented, blank, comment and docstring lines keep the current state, and so
// does a continuation line (continued), which Python lets start at any column.
func (s *pythonAbortScope) observe(line, code string, continued bool) bool {
	if s.file {
		return true
	}
	if !continued && strings.TrimSpace(code) != "" && lineIndent(line) == 0 {
		s.entry = pythonEntry.MatchString(strings.TrimSpace(line))
	}
	return s.entry
}

// pythonLineJoiner joins physical lines into Python's logical lines. A line continues the
// one before it while a bracket opened earlier is still open or the line before ended with
// a backslash; Python ignores the indentation of such a line, so black's closing
// `) -> int:` of a wrapped def header sits at column zero without ending the def.
type pythonLineJoiner struct {
	depth     int
	backslash bool
}

// continues feeds one line's code, already stripped of literals and comments so brackets
// inside strings never count, and reports whether that line continues the one before it.
// Each line must be passed exactly once and in order. An unbalanced closer leaves the depth
// at zero, never below it, so malformed code cannot hide the next bracket that opens.
func (j *pythonLineJoiner) continues(code string) bool {
	continued := j.depth > 0 || j.backslash
	for i := 0; i < len(code); i++ {
		switch code[i] {
		case '(', '[', '{':
			j.depth++
		case ')', ']', '}':
			j.depth = max(j.depth-1, 0)
		}
	}
	j.backslash = strings.HasSuffix(strings.TrimRight(code, " \t"), `\`)
	return continued
}

// checkPythonAbort reports sys.exit outside the places the abort policy allows it.
func checkPythonAbort(code, rel string, lineNum int, rep *ScanReport) {
	if pythonSysExit.MatchString(code) {
		recordViolation(rep, "HISS-07", rel, lineNum, "",
			"sys.exit ends the process from library code; return an error to the __main__ entry point instead")
	}
}

// isPythonTestPath follows pytest's default discovery: test_*.py and *_test.py modules,
// conftest.py, and anything under a tests/ or test/ directory.
func isPythonTestPath(rel string) bool {
	base, inTestDir := testPathParts(rel, "tests", "test")
	return inTestDir || strings.HasPrefix(base, "test_") || strings.HasSuffix(base, "_test.py") || base == "conftest.py"
}

// isPythonEntryFile reports a package's __main__.py, which is the entry point as a whole.
func isPythonEntryFile(rel string) bool {
	return filepath.Base(rel) == "__main__.py"
}

func isPythonDef(trimmed string) bool {
	return strings.HasPrefix(trimmed, "def ") || strings.HasPrefix(trimmed, "async def ")
}

// opensPythonScope reports whether a statement line opens a def or class.
func opensPythonScope(trimmed string) bool {
	return isPythonDef(trimmed) || strings.HasPrefix(trimmed, "class ")
}

// pythonStatementOnly holds the keywords that only ever begin a statement. None of them can
// start a line inside an open bracket, unlike if, else, for, from, lambda or await, which an
// expression continued across lines may start with.
var pythonStatementOnly = map[string]bool{
	"def": true, "class": true, "return": true, "import": true, "raise": true, "del": true,
	"pass": true, "break": true, "continue": true, "global": true, "nonlocal": true,
	"assert": true, "while": true, "with": true, "try": true, "except": true, "finally": true,
	"elif": true,
}

// beginsPythonStatement reports whether a line starts with a statement-only keyword. async is
// one only before def or with: `async for` may continue a comprehension.
func beginsPythonStatement(trimmed string) bool {
	word := leadingIdent(trimmed)
	if word == "async" {
		next := leadingIdent(trimmed[len(word):])
		return next == "def" || next == "with"
	}
	return pythonStatementOnly[word]
}

// openScope pushes the def or class this line opens, if any. A nested def binds its name in
// the scope it sits directly in, so a bare call to that name in that function reaches the
// nested def. Functions further out, and a class body, keep their own binding of the name.
func (s *pythonScanner) openScope(trimmed string, idx, indent int) {
	if !opensPythonScope(trimmed) {
		return
	}
	isClass := strings.HasPrefix(trimmed, "class ")
	name := leadingIdent(strings.TrimPrefix(trimmed, "class "))
	if !isClass {
		name = extractPythonFuncName(trimmed)
	}
	s.bindName(name)
	if len(s.open) >= maxPythonNesting {
		return
	}
	owner := ""
	if n := len(s.open); n > 0 && s.open[n-1].class {
		owner = s.open[n-1].name
	}
	s.open = append(s.open, pythonFunc{name: name, start: idx + 1, indent: indent, class: isClass,
		owner: owner, sigOpen: !isClass, calls: selfCalls{name: name}})
}

// observeCalls finishes an open def header and hands the body text to every open function: a
// call anywhere inside a function re-enters it, unless a scope between the call and the
// function binds the name (observeScopedCalls).
func (s *pythonScanner) observeCalls(code string, colon, lineNum int) {
	body := code
	if n := len(s.open); n > 0 && s.open[n-1].sigOpen {
		top := &s.open[n-1]
		if colon < 0 {
			top.sig = appendSignature(top.sig, code)
			return
		}
		top.sig = appendSignature(top.sig, code[:colon])
		s.closeSignature(top)
		body = code[colon+1:]
	}
	s.observeScopedBindings(body)
	s.observeScopedCalls(body, lineNum)
}

// closeSignature decides how the def is reached from its own body, and treats a parameter
// sharing an open function's name as a local of this def: it shadows that name for the calls
// this def makes, never for the enclosing function's own.
func (s *pythonScanner) closeSignature(top *pythonFunc) {
	top.sigOpen = false
	params := ""
	if open := strings.IndexByte(top.sig, '('); open >= 0 {
		params = top.sig[open+1:]
	}
	pythonCallees(&top.calls, top.owner, leadingIdent(params))
	j := len(s.open) - 1
	for i := 0; i <= j; i++ {
		if containsIdent(params, s.open[i].name) {
			s.markBound(j, i)
		}
	}
}

// close pops every open scope at or deeper than indent (a code line at that indent ends
// them), checks each function's length up to the last code line, and decides its recursion.
func (s *pythonScanner) close(indent int) {
	for i := 0; i < maxPythonNesting && len(s.open) > 0; i++ {
		top := s.open[len(s.open)-1]
		if top.indent < indent {
			break
		}
		if !top.class {
			noteFunction(s.rep, qualifiedName(top.owner, top.name), top.start, s.lastCode)
			checkPythonFuncLen(top.start, s.lastCode, top.name, s.rel, s.rep, s.maxLOC)
			top.calls.report(s.rep, s.rel)
		}
		s.passOutward(len(s.open) - 1)
		s.open = s.open[:len(s.open)-1]
	}
}

// scanPythonLineInvariants inspects one line with literals and comments stripped.
func scanPythonLineInvariants(code, rel string, lineNum int, rep *ScanReport) {
	if pythonWhileTrue.MatchString(code) {
		recordViolation(rep, "HISS-02", rel, lineNum, "", "Legacy unbounded while True loop in Python")
	}
	// Defining a method named eval or exec is not invoking the builtin. A hand-written AST
	// interpreter with an `eval` method executes nothing dynamically, and reporting its
	// definition makes the rule unusable for exactly the programs most likely to have one.
	isDefinition := strings.HasPrefix(strings.TrimSpace(code), "def ") ||
		strings.HasPrefix(strings.TrimSpace(code), "async def ")
	if !isDefinition && (hasBannedCall(code, "eval") || hasBannedCall(code, "exec")) {
		recordViolation(rep, "HISS-08", rel, lineNum, "", "Banned dynamic eval/exec execution in Python")
	}
	if pythonBareExcept.MatchString(code) {
		recordViolation(rep, "HISS-07", rel, lineNum, "", "Bare except catches and suppresses unhandled exceptions")
	}
}

func extractPythonFuncName(trimmed string) string {
	name := strings.TrimPrefix(trimmed, "async ")
	name = strings.TrimPrefix(name, "def ")
	if idx := strings.Index(name, "("); idx > 0 {
		name = strings.TrimSpace(name[:idx])
	}
	return name
}

func checkPythonFuncLen(start, end int, name, rel string, rep *ScanReport, maxLOC int) {
	funcLen := end - start + 1
	if funcLen > maxLOC {
		recordViolation(rep, "HISS-04", rel, start, name,
			fmt.Sprintf("Function '%s' (%d LOC) exceeds HISS-04 / NASA Rule 4 limit of %d LOC", name, funcLen, maxLOC))
	}
}

// ---------------------------------------------------------------------------
// Rust
// ---------------------------------------------------------------------------

// rustScanner carries the state the Rust line scanner needs across lines: the HISS-04
// function tracker, the test scope and the literal stripper, which must all see one view.
type rustScanner struct {
	rel   string
	rep   *ScanReport
	fn    braceTracker
	test  rustTestScope
	strip literalStripper
	// entry reports that fn follows the top-level fn main, the binary entry point.
	entry bool
	// calls follows the function fn has open for HISS-01, and blocks the impl and trait
	// bodies around it.
	calls  rustSelfCalls
	blocks rustBlockScope
	// proof follows the statement and the rustdoc a HISS-09 proof may attach to.
	proof rustProofScope
}

// scanRustLines scans valid Rust literals and retains conservative candidates in malformed files.
func scanRustLines(lines []string, rel string, rep *ScanReport, opts ScanOptions) {
	first := len(rep.functions)
	scanner := scanRustWithSyntax(lines, rel, rep, opts, rustSyntax)
	if !scanner.strip.open() && !scanner.strip.unclosed {
		return
	}
	// An unclosed literal must still expose candidate functions for the documentation check
	// to reject. Their comments remain governed by the strict forward lexical pass.
	candidates := newScanReport(MaxInfractionsCap)
	scanRustWithSyntax(lines, rel, candidates, opts, cLikeSyntax)
	rep.functions = append(rep.functions[:first], candidates.functions...)
}

// scanRustWithSyntax feeds one Rust scanner with an explicit literal syntax and returns its final state.
func scanRustWithSyntax(lines []string, rel string, rep *ScanReport, opts ScanOptions, syn literalSyntax) *rustScanner {
	s := &rustScanner{
		rel:   rel,
		rep:   rep,
		fn:    braceTracker{rel: rel, rep: rep, maxLOC: opts.MaxFuncLOC},
		test:  rustTestScope{file: isRustTestPath(rel), block: braceTracker{scopeOnly: true}},
		strip: literalStripper{syn: syn},
		calls: rustSelfCalls{rel: rel, rep: rep},
	}
	for idx := range lines {
		s.scanLine(lines, idx)
	}
	return s
}

// scanLine feeds lines[idx] to every tracker and then checks its invariants. A line belongs
// to the entry point or a test item when it does before or after the trackers see it, so the
// header and closing lines of both count as inside.
func (s *rustScanner) scanLine(lines []string, idx int) {
	line := lines[idx]
	code := s.strip.strip(line)
	codeTrimmed := strings.TrimSpace(code)
	scope := rustLineScope{test: s.test.observe(code, codeTrimmed, idx), entry: s.inEntry()}
	site := s.proof.observe(line, codeTrimmed, idx)
	member := s.blocks.member()
	name, isHeader := "", false
	if !s.fn.inFunc && !s.fn.pending {
		name, isHeader = rustFnHeaderName(codeTrimmed)
	}
	if isHeader {
		// Only an unindented fn main is the entry point; rustfmt indents a method of the
		// same name inside its impl block.
		s.entry = name == "main" && lineIndent(line) == 0
	}
	wasOpen := s.fn.inFunc
	s.fn.observe(code, strings.TrimSpace(line), idx, isHeader, name)
	s.calls.observe(rustLine{code: code, num: idx + 1, header: isHeader, name: name, body: s.blocks.kind(),
		entered: !wasOpen && s.fn.start == idx+1, open: s.fn.inFunc, pending: s.fn.pending})
	s.blocks.observe(code)
	// The header line of fn main is part of it even when the body closes on that same line
	// (`fn main() { std::process::exit(run()) }`), where the tracker is done before and after.
	scope.entry = scope.entry || s.inEntry() || (isHeader && s.entry)
	scanRustLineInvariants(code, idx, s.rel, s.rep, scope)
	checkRustUnsafe(lines, idx, code, site, member, s.rel, s.rep)
}

func (s *rustScanner) inEntry() bool {
	return s.entry && (s.fn.inFunc || s.fn.pending)
}

// rustFnHeaderName reports whether code, a trimmed line with literals stripped, opens a
// function header, and returns the function's name.
func rustFnHeaderName(code string) (string, bool) {
	m := rustFnHeader.FindStringSubmatch(code)
	if m == nil {
		return "", false
	}
	return m[2], true
}

// rustTestScope decides which lines are test code. A Cargo test path or an inner
// #![cfg(test)] makes the whole file test code; otherwise a test attribute makes test code of
// exactly the item it annotates, up to the brace that closes it.
//
// A flat flag set at the first #[cfg(test)] and never cleared hid every production .unwrap()
// and .expect() written below a test module, which is where Rust files commonly keep helpers
// added after the tests.
type rustTestScope struct {
	file  bool
	block braceTracker
}

// observe feeds one line, already stripped, and reports whether it is test code.
func (s *rustTestScope) observe(code, codeTrimmed string, idx int) bool {
	if !s.file && rustInnerTestAttr.MatchString(codeTrimmed) {
		s.file = true
	}
	if s.file {
		return true
	}
	before := s.block.inFunc || s.block.pending
	isAttr := !before && rustTestAttr.MatchString(codeTrimmed)
	s.block.observe(code, codeTrimmed, idx, isAttr, "")
	return before || isAttr || s.block.inFunc || s.block.pending
}

// testPathParts splits rel into its base name and whether any directory segment is one of
// testDirs.
func testPathParts(rel string, testDirs ...string) (string, bool) {
	segments := strings.Split(filepath.ToSlash(rel), "/")
	base := segments[len(segments)-1]
	for i := 0; i < len(segments)-1 && i < maxPathSegments; i++ {
		if slices.Contains(testDirs, segments[i]) {
			return base, true
		}
	}
	return base, false
}

// isRustTestPath follows Cargo conventions: integration tests and benches live in
// tests/ and benches/ directories, unit test files end in _test.rs or are named tests.rs.
func isRustTestPath(rel string) bool {
	base, inTestDir := testPathParts(rel, "tests", "benches")
	return inTestDir || strings.HasSuffix(base, "_test.rs") || base == "tests.rs" || base == "test.rs"
}

// rustLineScope says which HISS-07 exemptions apply to one line.
type rustLineScope struct {
	// test marks test code, where unwrap, expect and the abort forms are all allowed.
	test bool
	// entry marks the body of the binary entry point fn main, where only the abort forms are
	// allowed (owner decision Q-014); unwrap and expect stay refused there.
	entry bool
}

// scanRustLineInvariants inspects code, which is line idx already stripped by the caller so
// that block-comment state carries across lines. HISS-09 is checkRustUnsafe's, which also
// needs the raw lines around the line.
func scanRustLineInvariants(code string, idx int, rel string, rep *ScanReport, scope rustLineScope) {
	lineNum := idx + 1
	if rustUnboundedLoop.MatchString(code) {
		recordViolation(rep, "HISS-02", rel, lineNum, "", "Legacy unbounded loop {} in Rust without explicit scalar bound")
	}
	if !scope.test {
		checkRustErrorForms(code, rel, lineNum, rep, scope.entry)
	}
}

// checkRustErrorForms reports the HISS-07 forms on one line of production code: unwrap and
// expect everywhere, and the abort forms everywhere but the entry point.
func checkRustErrorForms(code, rel string, lineNum int, rep *ScanReport, entry bool) {
	if rustUnwrapCall.MatchString(code) {
		recordViolation(rep, "HISS-07", rel, lineNum, "", "Legacy .unwrap() invocation bypassing error propagation")
	}
	if rustExpectCall.MatchString(code) {
		recordViolation(rep, "HISS-07", rel, lineNum, "", "Legacy .expect() invocation in production Rust code")
	}
	if !entry {
		checkRustAbort(code, rel, lineNum, rep)
	}
}

// checkRustAbort enforces the HISS-07 abort policy (owner decision Q-014) on one line of
// production code: panic!, todo!, unimplemented!, unreachable! and process::exit or
// process::abort end the program instead of returning an error to the caller.
func checkRustAbort(code, rel string, lineNum int, rep *ScanReport) {
	if m := rustAbortMacro.FindStringSubmatch(code); m != nil {
		recordViolation(rep, "HISS-07", rel, lineNum, "",
			m[1]+"! aborts production Rust code; return an error to the caller instead")
	}
	if m := rustProcessExit.FindStringSubmatch(code); m != nil {
		recordViolation(rep, "HISS-07", rel, lineNum, "",
			"process::"+m[1]+" ends the process from library code; return an error to fn main instead")
	}
}

// quoteAt classifies a quote using the scanner's language syntax, leaving lifetime
// apostrophes as code and returning a complete character literal's width separately.
func (syn literalSyntax) quoteAt(text string, i int) (string, int) {
	if text[i] != '\'' && text[i] != '"' {
		return "", 0
	}
	if syn.hasTripleQuote(text[i]) {
		fence := strings.Repeat(text[i:i+1], 3)
		if strings.HasPrefix(text[i:], fence) {
			return fence, 3
		}
	}
	if text[i] == '\'' && !syn.apostropheIsString {
		return "", syn.characterWidth(text, i)
	}
	return text[i : i+1], 1
}

// characterWidth recognises Unicode characters, escapes, and native multicharacter constants.
func (syn literalSyntax) characterWidth(text string, i int) int {
	width := charLiteralWidth(text, i)
	if width > 0 || !syn.multiCharacter {
		return width
	}
	end, closed, _ := scanQuoted(text, i+1, "'")
	if closed {
		return end - i
	}
	return 0
}

// hasTripleQuote reports whether this quote byte can introduce a multiline literal.
func (syn literalSyntax) hasTripleQuote(quote byte) bool {
	return syn.tripleQuotes || (syn.tripleDoubleQuotes && quote == '"')
}

// syntaxForLanguage selects the lexical allow-list for a source language.
func syntaxForLanguage(language string) literalSyntax {
	switch language {
	case "rust":
		return rustSyntax
	case "c":
		syn := cLikeSyntax
		syn.cSplices = true
		syn.cTrigraphs = true
		return syn
	case "go":
		return goSyntax
	case "cpp", "cuda", "objective-c++":
		return cppSyntax
	case "python":
		return pythonSyntax
	case "java":
		return javaSyntax
	case "kotlin":
		return kotlinSyntax
	case "swift":
		return swiftSyntax
	case "javascript", "typescript", "svelte":
		return scriptSyntax
	default:
		return cLikeSyntax
	}
}

// ScanLines scans lines forward once with the language's lexical allow-list, returning each
// line's stripped code and start/end lexical state, and whether all constructs closed.
func ScanLines(lines []string, language string) ([]LexLine, bool) {
	if language == "svelte" {
		lines = svelteScriptLines(lines)
	}
	s := literalStripper{syn: syntaxForLanguage(language)}
	out := make([]LexLine, len(lines))
	for i := 0; i < len(lines); i++ {
		out[i] = s.scanLine(lines[i])
	}
	return out, !s.open() && !s.unclosed
}

// StripLiterals returns lines with string, character, template and regular-expression
// literals and comments removed by the scanner's per-language literalStripper, and
// reports whether every literal and comment closed within lines.
func StripLiterals(lines []string, language string) ([]string, bool) {
	lexed, ok := ScanLines(lines, language)
	out := make([]string, len(lexed))
	for i := 0; i < len(lexed); i++ {
		out[i] = lexed[i].Code
	}
	return out, ok
}
