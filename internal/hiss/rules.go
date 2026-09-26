package hiss

import (
	"fmt"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
)

// The non-Go rules matched exact strings, so one space silenced them: `while ( 1 )`,
// `for ( ;; )`, `loop{`, a labelled `'outer: loop {`, `x. unwrap()` and `while True :` all
// passed a gate that claims to catch them, and a reformatter could erase a finding without
// changing behaviour. These patterns tolerate arbitrary internal spacing instead. They are
// still line matchers rather than parsers, which bounds what they can claim, but they can no
// longer be defeated by whitespace alone.
var (
	nativeUnboundedLoop = regexp.MustCompile(`\bwhile\s*\(\s*(?:1|true)\s*\)|\bfor\s*\(\s*;\s*;\s*\)`)
	rustUnboundedLoop   = regexp.MustCompile(`(?:^|[^\w])loop\s*\{|(?:^|[^\w])while\s+true\s*\{`)
	rustUnwrapCall      = regexp.MustCompile(`\.\s*unwrap\s*\(\s*\)`)
	rustExpectCall      = regexp.MustCompile(`\.\s*expect\s*\(`)
	rustUnsafeBlock     = regexp.MustCompile(`(?:^|[^\w])unsafe\s*\{`)
	pythonWhileTrue     = regexp.MustCompile(`^\s*while\s+True\s*:`)
	pythonBareExcept    = regexp.MustCompile(`^\s*except\s*:`)

	// rustFnHeader is the Rust function-header grammar, matched against a line with literals
	// stripped: an optional visibility (pub, pub(crate), pub(super), pub(in path)), any run of
	// qualifiers (const, async, unsafe, safe, default, extern with or without its ABI string),
	// then fn and the name. A fixed prefix list silently skipped every header it did not
	// spell out, so const, unsafe, extern "C" and pub(super) functions were never measured.
	//
	// Outer attributes may precede the header on the same line (`#[tokio::main] async fn
	// main() {`); anchoring the grammar at fn's visibility left such a function unmeasured and,
	// for fn main, not recognised as the entry point.
	rustFnHeader = regexp.MustCompile(`^(?:#\s*\[[^\]]*\]\s*)*(?:pub\s*(?:\([^)]*\))?\s+)?(?:(?:const|async|unsafe|safe|default|extern)\s+)*fn\s+(?:r#)?([A-Za-z_]\w*)`)
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

func (t *braceTracker) finish(endLine int) {
	t.inFunc = false
	if t.scopeOnly {
		return
	}
	funcLen := endLine - t.start + 1
	if funcLen > t.maxLOC {
		recordViolation(t.rep, "HISS-04", t.rel, t.start, t.name,
			fmt.Sprintf("Function '%s' (%d LOC) exceeds HISS-04 / NASA Rule 4 limit of %d LOC", t.name, funcLen, t.maxLOC))
	}
}

// literalSyntax describes how a language delimits strings and line comments.
type literalSyntax struct {
	// apostropheIsString treats '...' as a string (Python) rather than a character
	// literal (C, Rust), where a lone apostrophe such as the lifetime 'a is plain text.
	apostropheIsString bool
	lineComment        string
}

var (
	cLikeSyntax  = literalSyntax{apostropheIsString: false, lineComment: "//"}
	pythonSyntax = literalSyntax{apostropheIsString: true, lineComment: "#"}
)

// blockCommentClose ends a C-style block comment, the one fence that honours no escapes.
const blockCommentClose = "*/"

// literalStripper strips literals and comments across a whole file, carrying the state that
// a single line cannot hold: a C-style block comment and a Python triple-quoted string both
// span lines, and so does a quoted string whose line ends in a backslash.
//
// Without that state every construct inside a multi-line comment or docstring was scanned as
// code, so documenting a counter-example reported it as a live infraction. A gate that
// punishes explaining the thing it forbids teaches people to stop explaining it.
type literalStripper struct {
	syn literalSyntax
	// fence is the delimiter that closes the span currently open, or empty outside one.
	fence string
}

// strip returns line with the contents of literals and comments removed, updating the
// cross-line state. Each line must be passed exactly once and in order.
func (s *literalStripper) strip(line string) string {
	var b strings.Builder
	for i := 0; i < len(line); {
		if s.fence != "" {
			i = s.consumeFence(line, i)
			continue
		}
		if next, opened := s.openFence(line, i); opened {
			i = next
			continue
		}
		if strings.HasPrefix(line[i:], s.syn.lineComment) {
			return b.String()
		}
		i = s.copyOne(line, i, &b)
	}
	return b.String()
}

// consumeFence skips bytes until the open span's closing delimiter, which may not appear on
// this line at all. Inside a string a backslash escapes the byte after it, so `\"""` does not
// close a triple-quoted string. A single-quoted string carried here by a trailing backslash
// ends with this line unless the line ends in a backslash too, so a string the stripper
// misreads can never hold the rest of the file.
func (s *literalStripper) consumeFence(line string, i int) int {
	if s.fence == blockCommentClose {
		if idx := strings.Index(line[i:], s.fence); idx >= 0 {
			s.fence = ""
			return i + idx + len(blockCommentClose)
		}
		return len(line)
	}
	end, closed, carried := scanQuoted(line, i, s.fence)
	if closed || (len(s.fence) == 1 && !carried) {
		s.fence = ""
	}
	return end
}

// openFence reports whether a multi-line span starts at i and records its closing delimiter.
// Python triple quotes are checked before ordinary quotes so a docstring is never read as an
// empty string followed by code.
func (s *literalStripper) openFence(line string, i int) (int, bool) {
	if s.syn.apostropheIsString {
		for _, fence := range []string{`"""`, `'''`} {
			if strings.HasPrefix(line[i:], fence) {
				s.fence = fence
				return i + len(fence), true
			}
		}
		return i, false
	}
	if strings.HasPrefix(line[i:], "/*") {
		s.fence = blockCommentClose
		return i + 2, true
	}
	return i, false
}

// copyOne copies or skips the token at i, blanking the contents of a single-line literal.
func (s *literalStripper) copyOne(line string, i int, b *strings.Builder) int {
	switch c := line[i]; c {
	case '"', '\'':
		if c == '\'' && !s.syn.apostropheIsString {
			if width := charLiteralWidth(line, i); width > 0 {
				return i + width
			}
			b.WriteByte(c)
			return i + 1
		}
		end, _, carried := scanQuoted(line, i+1, line[i:i+1])
		if carried {
			s.fence = line[i : i+1]
		}
		return end
	default:
		b.WriteByte(c)
		return i + 1
	}
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
	if i+2 < len(line) && line[i+1] != '\\' && line[i+2] == '\'' {
		return 3
	}
	if i+3 < len(line) && line[i+1] == '\\' && line[i+3] == '\'' {
		return 4
	}
	return 0
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
		if (at == 0 || !excluded(line[at-1])) && opensArgs(line, at+len(callee)) {
			return true
		}
		at = nextIdent(line, callee, at+1)
	}
	return false
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
		end := at + len(name)
		if (at == 0 || !isIdentByte(text[at-1])) && (end == len(text) || !isIdentByte(text[end])) {
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

func scanNativeLines(lines []string, rel string, rep *ScanReport, opts ScanOptions) {
	t := &braceTracker{rel: rel, rep: rep, maxLOC: opts.MaxFuncLOC}
	// One stripper for the whole file, and one strip per line: the invariant scan, the
	// header test and the brace tracker must all see the same view, or a block comment
	// closes for one of them and not the others.
	stripper := &literalStripper{syn: cLikeSyntax}
	for idx, line := range lines {
		trimmed := strings.TrimSpace(line)
		code := stripper.strip(line)
		scanNativeLineInvariants(code, rel, idx+1, rep)
		name, isHeader := nativeFuncHeader(code)
		isHeader = !t.inFunc && isHeader
		t.observe(code, trimmed, idx, isHeader, name)
	}
}

// scanNativeLineInvariants inspects one line with literals and comments stripped.
func scanNativeLineInvariants(code, rel string, lineNum int, rep *ScanReport) {
	if nativeUnboundedLoop.MatchString(code) {
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
	if strings.HasPrefix(strings.TrimSpace(code), "goto ") {
		recordViolation(rep, "HISS-01", rel, lineNum, "", "Legacy non-DAG control flow jump (goto)")
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
	abort  pythonAbortScope
	joiner pythonLineJoiner
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
// every enclosing function, so a bare call to that name there reaches the nested def.
func (s *pythonScanner) openScope(trimmed string, idx, indent int) {
	if !opensPythonScope(trimmed) {
		return
	}
	isClass := strings.HasPrefix(trimmed, "class ")
	name := leadingIdent(strings.TrimPrefix(trimmed, "class "))
	if !isClass {
		name = extractPythonFuncName(trimmed)
	}
	for i := 0; i < len(s.open); i++ {
		if s.open[i].name == name {
			s.open[i].calls.bound = true
		}
	}
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

// observeCalls finishes an open def header and hands the body text to every open function.
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
	for i := 0; i < len(s.open); i++ {
		if !s.open[i].class {
			s.open[i].calls.observe(body, lineNum)
		}
	}
}

// closeSignature decides how the def is reached from its own body, and treats a parameter
// sharing an open function's name as a local that shadows it.
func (s *pythonScanner) closeSignature(top *pythonFunc) {
	top.sigOpen = false
	params := ""
	if open := strings.IndexByte(top.sig, '('); open >= 0 {
		params = top.sig[open+1:]
	}
	pythonCallees(&top.calls, top.owner, leadingIdent(params))
	for i := 0; i < len(s.open); i++ {
		if containsIdent(params, s.open[i].name) {
			s.open[i].calls.bound = true
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
			checkPythonFuncLen(top.start, s.lastCode, top.name, s.rel, s.rep, s.maxLOC)
			top.calls.report(s.rep, s.rel)
		}
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
}

func scanRustLines(lines []string, rel string, rep *ScanReport, opts ScanOptions) {
	s := &rustScanner{
		rel:   rel,
		rep:   rep,
		fn:    braceTracker{rel: rel, rep: rep, maxLOC: opts.MaxFuncLOC},
		test:  rustTestScope{file: isRustTestPath(rel), block: braceTracker{scopeOnly: true}},
		strip: literalStripper{syn: cLikeSyntax},
		calls: rustSelfCalls{rel: rel, rep: rep},
	}
	for idx := range lines {
		s.scanLine(lines, idx)
	}
}

// scanLine feeds lines[idx] to every tracker and then checks its invariants. A line belongs
// to the entry point or a test item when it does before or after the trackers see it, so the
// header and closing lines of both count as inside.
func (s *rustScanner) scanLine(lines []string, idx int) {
	line := lines[idx]
	code := s.strip.strip(line)
	codeTrimmed := strings.TrimSpace(code)
	scope := rustLineScope{test: s.test.observe(code, codeTrimmed, idx), entry: s.inEntry()}
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
	scanRustLineInvariants(code, lines, idx, s.rel, s.rep, scope)
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
	return m[1], true
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

// scanRustLineInvariants inspects code, which is lines[idx] already stripped by the caller
// so that block-comment state carries across lines; the raw surrounding lines are consulted
// only for the SAFETY: proof comment.
func scanRustLineInvariants(code string, lines []string, idx int, rel string, rep *ScanReport, scope rustLineScope) {
	trimmed := strings.TrimSpace(code)
	lineNum := idx + 1
	if rustUnboundedLoop.MatchString(code) {
		recordViolation(rep, "HISS-02", rel, lineNum, "", "Legacy unbounded loop {} in Rust without explicit scalar bound")
	}
	if !scope.test {
		checkRustErrorForms(code, rel, lineNum, rep, scope.entry)
	}
	isUnsafe := rustUnsafeBlock.MatchString(code) || strings.HasPrefix(trimmed, "unsafe fn")
	if isUnsafe && !hasSafetyComment(lines, idx) {
		recordViolation(rep, "HISS-09", rel, lineNum, "", "unsafe block without a preceding // SAFETY: proof comment")
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
