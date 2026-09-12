package hiss

import (
	"fmt"
	"path/filepath"
	"strings"
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
	start      int
	name       string
	level      int
}

// observe feeds one line. isHeader reports whether the line opens a function
// signature; name is the function name for that case.
func (t *braceTracker) observe(line, trimmed string, idx int, isHeader bool, name string) {
	code := stripLiterals(line, cLikeSyntax)
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
		if strings.HasSuffix(trimmed, ";") || t.pendingAge >= maxPendingHeaderLines {
			t.pending = false
		}
	case isHeader:
		if strings.HasSuffix(trimmed, ";") {
			return // a declaration without a body (trait method, prototype)
		}
		t.start = idx + 1
		t.name = name
		if opens > 0 {
			t.enter(idx, opens-closes)
			return
		}
		t.pending = true
		t.pendingAge = 0
	}
}

func (t *braceTracker) enter(idx, level int) {
	t.inFunc = true
	t.pending = false
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

// stripLiterals blanks the contents of string and character literals and drops a
// trailing line comment, so that braces or banned identifiers inside "{" or '}' or a
// comment never desynchronise the line-based scanners.
func stripLiterals(line string, syn literalSyntax) string {
	var b strings.Builder
	var quote byte
	n := len(line)
	for i := 0; i < n; i++ {
		c := line[i]
		if quote != 0 {
			switch c {
			case '\\':
				i++
			case quote:
				quote = 0
			}
			continue
		}
		if strings.HasPrefix(line[i:], syn.lineComment) {
			return b.String()
		}
		switch c {
		case '"':
			quote = c
		case '\'':
			if syn.apostropheIsString {
				quote = c
			} else if width := charLiteralWidth(line, i); width > 0 {
				i += width - 1
			} else {
				b.WriteByte(c)
			}
		default:
			b.WriteByte(c)
		}
	}
	return b.String()
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
	needle := name + "("
	offset := 0
	for i := 0; i < len(line); i++ {
		pos := strings.Index(line[offset:], needle)
		if pos < 0 {
			return false
		}
		at := offset + pos
		if at == 0 || !isIdentByte(line[at-1]) {
			return true
		}
		offset = at + 1
	}
	return false
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
	for idx, line := range lines {
		trimmed := strings.TrimSpace(line)
		scanNativeLineInvariants(stripLiterals(line, cLikeSyntax), rel, idx+1, rep)
		isHeader := !t.inFunc && isNativeFuncHeader(line, trimmed)
		name := ""
		if isHeader {
			name = extractNativeFuncName(trimmed, lines, idx)
		}
		t.observe(line, trimmed, idx, isHeader, name)
	}
}

// scanNativeLineInvariants inspects one line with literals and comments stripped.
func scanNativeLineInvariants(code, rel string, lineNum int, rep *ScanReport) {
	if strings.Contains(code, "while (1)") || strings.Contains(code, "while(1)") ||
		strings.Contains(code, "while (true)") || strings.Contains(code, "while(true)") ||
		strings.Contains(code, "for (;;)") || strings.Contains(code, "for(;;)") {
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

func isNativeFuncHeader(line, trimmed string) bool {
	return strings.Contains(stripLiterals(line, cLikeSyntax), "{") && !strings.HasPrefix(trimmed, "//") &&
		!strings.HasPrefix(trimmed, "/*") && !strings.HasPrefix(trimmed, "struct ") &&
		!strings.HasPrefix(trimmed, "enum ") && !strings.HasPrefix(trimmed, "union ") &&
		!strings.HasPrefix(trimmed, "typedef ") && !strings.HasPrefix(trimmed, "class ") &&
		!strings.HasPrefix(trimmed, "#")
}

func extractNativeFuncName(trimmed string, lines []string, lineIdx int) string {
	if name, ok := nativeNameBeforeParen(trimmed); ok {
		return name
	}
	if lineIdx > 0 {
		if name, ok := nativeNameBeforeParen(strings.TrimSpace(lines[lineIdx-1])); ok {
			return name
		}
	}
	return trimmed
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
	return strings.TrimPrefix(parts[len(parts)-1], "*"), true
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
	for idx, line := range lines {
		trimmed := strings.TrimSpace(line)
		scanPythonLineInvariants(stripLiterals(line, pythonSyntax), rel, idx+1, rep)
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
	trimmed := strings.TrimSpace(code)
	if strings.HasPrefix(trimmed, "while True:") {
		recordViolation(rep, "HISS-02", rel, lineNum, "", "Legacy unbounded while True loop in Python")
	}
	if hasBannedCall(code, "eval") || hasBannedCall(code, "exec") {
		recordViolation(rep, "HISS-08", rel, lineNum, "", "Banned dynamic eval/exec execution in Python")
	}
	if trimmed == "except:" || strings.HasPrefix(trimmed, "except: ") {
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
	for idx, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "#[cfg(test)]") {
			testCode = true
		}
		scanRustLineInvariants(lines, idx, rel, rep, testCode)
		isHeader := !t.inFunc && !t.pending && isRustFnHeader(trimmed)
		name := ""
		if isHeader {
			name = extractRustFuncName(trimmed)
		}
		t.observe(line, trimmed, idx, isHeader, name)
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

// scanRustLineInvariants inspects lines[idx] with literals and comments stripped; the
// raw surrounding lines are consulted only for the SAFETY: proof comment.
func scanRustLineInvariants(lines []string, idx int, rel string, rep *ScanReport, testCode bool) {
	code := stripLiterals(lines[idx], cLikeSyntax)
	trimmed := strings.TrimSpace(code)
	lineNum := idx + 1
	if trimmed == "loop {" || strings.HasPrefix(trimmed, "loop { ") {
		recordViolation(rep, "HISS-02", rel, lineNum, "", "Legacy unbounded loop {} in Rust without explicit scalar bound")
	}
	if !testCode && strings.Contains(code, ".unwrap()") {
		recordViolation(rep, "HISS-07", rel, lineNum, "", "Legacy .unwrap() invocation bypassing error propagation")
	}
	if !testCode && strings.Contains(code, ".expect(") {
		recordViolation(rep, "HISS-07", rel, lineNum, "", "Legacy .expect() invocation in production Rust code")
	}
	isUnsafe := strings.Contains(code, "unsafe {") || strings.HasPrefix(trimmed, "unsafe fn")
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
