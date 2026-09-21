package hiss

import (
	"fmt"
	"path/filepath"
	"regexp"
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

// literalStripper strips literals and comments across a whole file, carrying the state that
// a single line cannot hold: a C-style block comment and a Python triple-quoted string both
// span lines.
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
// this line at all.
func (s *literalStripper) consumeFence(line string, i int) int {
	if idx := strings.Index(line[i:], s.fence); idx >= 0 {
		end := i + idx + len(s.fence)
		s.fence = ""
		return end
	}
	return len(line)
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
		s.fence = "*/"
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
		return skipQuoted(line, i, c)
	default:
		b.WriteByte(c)
		return i + 1
	}
}

// skipQuoted returns the index just past a single-line quoted run opened at i, honouring
// backslash escapes. An unterminated quote consumes the rest of the line.
func skipQuoted(line string, i int, quote byte) int {
	for j := i + 1; j < len(line); j++ {
		switch line[j] {
		case '\\':
			j++
		case quote:
			return j + 1
		}
	}
	return len(line)
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
func hasBannedCall(line, name string) bool {
	offset := 0
	for i := 0; i < len(line); i++ {
		pos := strings.Index(line[offset:], name)
		if pos < 0 {
			return false
		}
		at := offset + pos
		if isBannedCallSite(line, at, len(name)) {
			return true
		}
		offset = at + 1
	}
	return false
}

// isBannedCallSite reports whether the occurrence at `at` invokes the bare builtin: a whole
// identifier, not reached through a selector, followed by an argument list.
//
// Whitespace between the identifier and the parenthesis does not change the call, so
// `gets (buf)` must not escape a rule that catches `gets(buf)`. A leading dot means the name
// resolves to a method rather than the builtin, which is why `interpreter.eval(node)` on a
// hand-written AST walker is not dynamic execution.
func isBannedCallSite(line string, at, nameLen int) bool {
	if at > 0 && (isIdentByte(line[at-1]) || line[at-1] == '.') {
		return false
	}
	after := at + nameLen
	for after < len(line) && (line[after] == ' ' || line[after] == '\t') {
		after++
	}
	return after < len(line) && line[after] == '('
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

type pythonFunc struct {
	name   string
	start  int
	indent int
}

// scanPythonLines tracks a stack of open functions so that nested defs do not end
// their enclosing function, and ends every function at its last code line so that
// trailing blank and comment lines are not counted.
func scanPythonLines(lines []string, rel string, rep *ScanReport, opts ScanOptions) {
	var open []pythonFunc
	lastCode := 0
	stripper := &literalStripper{syn: pythonSyntax}
	for idx, line := range lines {
		trimmed := strings.TrimSpace(line)
		scanPythonLineInvariants(stripper.strip(line), rel, idx+1, rep)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		indent := len(line) - len(strings.TrimLeft(line, " \t"))
		open = closePythonFuncs(open, indent, lastCode, rel, rep, opts.MaxFuncLOC)
		lastCode = idx + 1
		if isPythonDef(trimmed) && len(open) < maxPythonNesting {
			open = append(open, pythonFunc{name: extractPythonFuncName(trimmed), start: idx + 1, indent: indent})
		}
	}
	closePythonFuncs(open, -1, lastCode, rel, rep, opts.MaxFuncLOC)
}

func isPythonDef(trimmed string) bool {
	return strings.HasPrefix(trimmed, "def ") || strings.HasPrefix(trimmed, "async def ")
}

// closePythonFuncs pops every open function at or deeper than indent (a code line at
// that indent ends them) and checks its length up to lastCode.
func closePythonFuncs(open []pythonFunc, indent, lastCode int, rel string, rep *ScanReport, maxLOC int) []pythonFunc {
	for i := 0; i < maxPythonNesting && len(open) > 0; i++ {
		top := open[len(open)-1]
		if top.indent < indent {
			break
		}
		checkPythonFuncLen(top.start, lastCode, top.name, rel, rep, maxLOC)
		open = open[:len(open)-1]
	}
	return open
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

func scanRustLines(lines []string, rel string, rep *ScanReport, opts ScanOptions) {
	t := &braceTracker{rel: rel, rep: rep, maxLOC: opts.MaxFuncLOC}
	testCode := isRustTestPath(rel)
	stripper := &literalStripper{syn: cLikeSyntax}
	for idx, line := range lines {
		trimmed := strings.TrimSpace(line)
		code := stripper.strip(line)
		if strings.HasPrefix(trimmed, "#[cfg(test)]") {
			testCode = true
		}
		scanRustLineInvariants(code, lines, idx, rel, rep, testCode)
		isHeader := !t.inFunc && !t.pending && isRustFnHeader(trimmed)
		name := ""
		if isHeader {
			name = extractRustFuncName(trimmed)
		}
		t.observe(code, trimmed, idx, isHeader, name)
	}
}

func isRustFnHeader(trimmed string) bool {
	for _, p := range []string{"fn ", "pub fn ", "pub(crate) fn ", "async fn ", "pub async fn ", "pub(crate) async fn "} {
		if strings.HasPrefix(trimmed, p) {
			return true
		}
	}
	return false
}

// isRustTestPath follows Cargo conventions: integration tests and benches live in
// tests/ and benches/ directories, unit test files end in _test.rs or are named tests.rs.
func isRustTestPath(rel string) bool {
	norm := filepath.ToSlash(rel)
	segments := strings.Split(norm, "/")
	for i := 0; i < len(segments)-1 && i < maxPathSegments; i++ {
		if segments[i] == "tests" || segments[i] == "benches" {
			return true
		}
	}
	base := segments[len(segments)-1]
	return strings.HasSuffix(base, "_test.rs") || base == "tests.rs" || base == "test.rs"
}

// scanRustLineInvariants inspects code, which is lines[idx] already stripped by the caller
// so that block-comment state carries across lines; the raw surrounding lines are consulted
// only for the SAFETY: proof comment.
func scanRustLineInvariants(code string, lines []string, idx int, rel string, rep *ScanReport, testCode bool) {
	trimmed := strings.TrimSpace(code)
	lineNum := idx + 1
	if rustUnboundedLoop.MatchString(code) {
		recordViolation(rep, "HISS-02", rel, lineNum, "", "Legacy unbounded loop {} in Rust without explicit scalar bound")
	}
	if !testCode && rustUnwrapCall.MatchString(code) {
		recordViolation(rep, "HISS-07", rel, lineNum, "", "Legacy .unwrap() invocation bypassing error propagation")
	}
	if !testCode && rustExpectCall.MatchString(code) {
		recordViolation(rep, "HISS-07", rel, lineNum, "", "Legacy .expect() invocation in production Rust code")
	}
	isUnsafe := rustUnsafeBlock.MatchString(code) || strings.HasPrefix(trimmed, "unsafe fn")
	if isUnsafe && !hasSafetyComment(lines, idx) {
		recordViolation(rep, "HISS-09", rel, lineNum, "", "unsafe block without a preceding // SAFETY: proof comment")
	}
}

func extractRustFuncName(trimmed string) string {
	name := trimmed
	for _, p := range []string{"pub(crate) ", "pub ", "async "} {
		name = strings.TrimPrefix(name, p)
	}
	name = strings.TrimPrefix(name, "fn ")
	if idx := strings.Index(name, "("); idx > 0 {
		name = strings.TrimSpace(name[:idx])
	}
	if idx := strings.Index(name, "<"); idx > 0 {
		name = strings.TrimSpace(name[:idx])
	}
	return name
}
