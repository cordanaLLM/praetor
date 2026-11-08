// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package hiss

import (
	"fmt"
	"strings"
)

const (
	maxPythonIOTracked   = 64
	maxPythonIOPending   = 32
	maxPythonIOCallLines = 60
	maxPythonIOFromItems = 32
	maxPythonIOCallArgs  = 128
	maxPythonIOClauses   = 16
	maxPythonIOLineBytes = 4096
)

type pythonIOName struct {
	local     string
	module    string
	canonical string
}

type pythonIOCall struct {
	canonical string
	openLine  int
	text      string
}

type pythonIOScope struct {
	tracked    []pythonIOName
	pending    []pythonIOCall
	fromModule string
	fromLines  int
}

// observePythonIO feeds one stripped line to the HISS-02 I/O check: imports first, then open
// calls, then new ones.
func (s *pythonScanner) observePythonIO(code string, lineNum int) {
	s.io.observeImports(code)
	s.advancePendingPythonIO(code, lineNum)
	s.scanNewPythonIOCalls(code, lineNum)
}

// observeImports records the subprocess, urllib.request and requests bindings an import statement
// on code creates.
func (s *pythonIOScope) observeImports(code string) {
	if s.fromModule != "" {
		s.continueFromImport(code)
		return
	}
	clauses := strings.Split(code, ";")
	for i := 0; i < len(clauses) && i < maxPythonIOClauses; i++ {
		trimmed := strings.TrimSpace(clauses[i])
		switch leadingIdent(trimmed) {
		case "import":
			s.observePlainImport(strings.TrimSpace(trimmed[len("import"):]))
		case "from":
			s.observeFromImport(strings.TrimSpace(trimmed[len("from"):]))
		}
	}
}

// continueFromImport reads the next line of a parenthesised from-import, bounded by
// maxPythonIOCallLines.
func (s *pythonIOScope) continueFromImport(code string) {
	s.fromLines++
	if closeIdx := strings.IndexByte(code, ')'); closeIdx >= 0 {
		s.trackFromItems(s.fromModule, code[:closeIdx])
		s.fromModule = ""
		s.fromLines = 0
		return
	}
	s.trackFromItems(s.fromModule, code)
	if s.fromLines >= maxPythonIOCallLines {
		s.fromModule = ""
		s.fromLines = 0
	}
}

// observePlainImport tracks import subprocess, import requests and import urllib.request, aliased
// or not.
func (s *pythonIOScope) observePlainImport(rest string) {
	items := strings.Split(rest, ",")
	for i := 0; i < len(items) && i < maxPythonIOFromItems; i++ {
		orig, alias := parsePythonImportItem(items[i])
		if orig == "subprocess" || orig == "requests" || orig == "urllib.request" {
			s.addTracked(pythonIOName{local: alias, module: orig})
		}
	}
}

// observeFromImport tracks the target names a from-import of a tracked module binds, on one line
// or opening a parenthesised list.
func (s *pythonIOScope) observeFromImport(rest string) {
	imp := nextIdent(rest, "import", 0)
	if imp <= 0 {
		return
	}
	mod := strings.TrimSpace(rest[:imp])
	if !isTrackedPythonIOModule(mod) {
		return
	}
	after := strings.TrimSpace(rest[imp+len("import"):])
	if !strings.HasPrefix(after, "(") {
		s.trackFromItems(mod, after)
		return
	}
	body := after[1:]
	if closeIdx := strings.IndexByte(body, ')'); closeIdx >= 0 {
		s.trackFromItems(mod, body[:closeIdx])
		return
	}
	s.trackFromItems(mod, body)
	s.fromModule = mod
	s.fromLines = 1
}

// isTrackedPythonIOModule reports a module whose from-imports can bind a target call.
func isTrackedPythonIOModule(mod string) bool {
	return mod == "subprocess" || mod == "requests" || mod == "urllib" || mod == "urllib.request"
}

// trackFromItems tracks each comma-separated item of a from-import that names a target.
func (s *pythonIOScope) trackFromItems(mod, text string) {
	items := strings.Split(text, ",")
	for i := 0; i < len(items) && i < maxPythonIOFromItems; i++ {
		orig, alias := parsePythonImportItem(items[i])
		if entry, ok := resolveFromImportItem(mod, orig, alias); ok {
			s.addTracked(entry)
		}
	}
}

// resolveFromImportItem maps one from-import item to a tracked binding: a module alias or a bare
// target name.
func resolveFromImportItem(mod, orig, alias string) (pythonIOName, bool) {
	if orig == "" || alias == "" {
		return pythonIOName{}, false
	}
	if mod == "urllib" && orig == "request" {
		return pythonIOName{local: alias, module: "urllib.request"}, true
	}
	if canonical := pythonIOModuleTarget(mod, orig); canonical != "" {
		return pythonIOName{local: alias, canonical: canonical}, true
	}
	return pythonIOName{}, false
}

// parsePythonImportItem splits an import item into its imported name and the local name it binds.
func parsePythonImportItem(item string) (string, string) {
	trimmed := strings.TrimSpace(item)
	if trimmed == "" {
		return "", ""
	}
	if asIdx := nextIdent(trimmed, "as", 0); asIdx > 0 {
		orig := strings.TrimSpace(trimmed[:asIdx])
		alias := leadingIdent(trimmed[asIdx+len("as"):])
		return orig, alias
	}
	return trimmed, trimmed
}

// addTracked records or rebinds a local name, bounded by maxPythonIOTracked.
func (s *pythonIOScope) addTracked(entry pythonIOName) {
	if entry.local == "" {
		return
	}
	for i := 0; i < len(s.tracked) && i < maxPythonIOTracked; i++ {
		if s.tracked[i].local == entry.local {
			s.tracked[i] = entry
			return
		}
	}
	if len(s.tracked) < maxPythonIOTracked {
		s.tracked = append(s.tracked, entry)
	}
}

// pythonIOModuleTarget names the canonical target of fn in module, or empty when fn is no target.
func pythonIOModuleTarget(module, fn string) string {
	switch module {
	case "subprocess":
		if isSubprocessIOTarget(fn) {
			return "subprocess." + fn
		}
	case "urllib.request":
		if fn == "urlopen" {
			return "urllib.request.urlopen"
		}
	case "requests":
		if isRequestsIOTarget(fn) {
			return "requests." + fn
		}
	}
	return ""
}

// isSubprocessIOTarget reports the subprocess functions that accept timeout=.
func isSubprocessIOTarget(fn string) bool {
	return fn == "run" || fn == "call" || fn == "check_call" || fn == "check_output"
}

// isRequestsIOTarget reports the requests verbs that accept timeout=.
func isRequestsIOTarget(fn string) bool {
	switch fn {
	case "get", "post", "put", "patch", "delete", "head", "options", "request":
		return true
	default:
		return false
	}
}

// advancePendingPythonIO feeds code to every call still open from earlier lines and keeps those
// that stay open.
func (s *pythonScanner) advancePendingPythonIO(code string, lineNum int) {
	if len(s.io.pending) == 0 {
		return
	}
	kept := s.io.pending[:0]
	for i := 0; i < len(s.io.pending) && i < maxPythonIOPending; i++ {
		call := s.io.pending[i]
		if s.stepPendingPythonIOCall(&call, code, lineNum) {
			kept = append(kept, call)
		}
	}
	s.io.pending = kept
}

// stepPendingPythonIOCall appends code to an open call and decides it once it closes, or reports
// it past the line bound.
func (s *pythonScanner) stepPendingPythonIOCall(call *pythonIOCall, code string, lineNum int) bool {
	span := lineNum - call.openLine + 1
	if span > maxPythonIOCallLines {
		s.reportPythonIOBound(call.canonical, call.openLine)
		return false
	}
	call.text += " " + code
	args, closeIdx := parseCallArgs(call.text, 0)
	if closeIdx < len(call.text) {
		s.decidePythonIOCall(call.canonical, call.openLine, args)
		return false
	}
	if span >= maxPythonIOCallLines {
		s.reportPythonIOBound(call.canonical, call.openLine)
		return false
	}
	return true
}

// scanNewPythonIOCalls opens a check for each target call whose parenthesis starts on code.
func (s *pythonScanner) scanNewPythonIOCalls(code string, lineNum int) {
	if len(s.io.tracked) == 0 {
		return
	}
	for i := 0; i < len(code) && i < maxPythonIOLineBytes; i++ {
		if code[i] != '(' {
			continue
		}
		canonical := s.io.resolveCallAtParen(code, i)
		if canonical == "" {
			continue
		}
		s.openPythonIOCall(canonical, code[i:], lineNum)
	}
}

// openPythonIOCall decides a call that closes on its own line, or keeps it pending, bounded by
// maxPythonIOPending.
func (s *pythonScanner) openPythonIOCall(canonical, tail string, lineNum int) {
	args, closeIdx := parseCallArgs(tail, 0)
	if closeIdx < len(tail) {
		s.decidePythonIOCall(canonical, lineNum, args)
		return
	}
	if len(s.io.pending) < maxPythonIOPending {
		s.io.pending = append(s.io.pending, pythonIOCall{
			canonical: canonical,
			openLine:  lineNum,
			text:      tail,
		})
	}
}

// resolveCallAtParen names the target called at openParen, or empty for a definition or an
// untracked name.
func (s *pythonIOScope) resolveCallAtParen(code string, openParen int) string {
	qual, fnName, fnStart := splitCallBeforeParen(code, openParen)
	if fnName == "" || isDefAt(code, fnStart) {
		return ""
	}
	if qual == "" {
		return s.lookupBareTarget(fnName)
	}
	return s.lookupQualifiedTarget(qual, fnName)
}

// splitCallBeforeParen returns the dotted qualifier, the function name and its start before
// openParen.
func splitCallBeforeParen(code string, openParen int) (string, string, int) {
	end := skipSpacesLeft(code, openParen)
	fnStart := scanIdentLeft(code, end)
	if fnStart == end {
		return "", "", 0
	}
	beforeFn := skipSpacesLeft(code, fnStart)
	if beforeFn == 0 || code[beforeFn-1] != '.' {
		return "", code[fnStart:end], fnStart
	}
	qualEnd := skipSpacesLeft(code, beforeFn-1)
	qualStart := qualEnd
	for qualStart > 0 && (isIdentByte(code[qualStart-1]) || code[qualStart-1] == '.') {
		qualStart--
	}
	qual := code[qualStart:qualEnd]
	if qual == "" || qual[0] == '.' {
		return "", "", 0
	}
	return qual, code[fnStart:end], fnStart
}

// skipSpacesLeft moves pos left over spaces and tabs.
func skipSpacesLeft(code string, pos int) int {
	for pos > 0 && (code[pos-1] == ' ' || code[pos-1] == '\t') {
		pos--
	}
	return pos
}

// scanIdentLeft moves pos left over identifier bytes.
func scanIdentLeft(code string, pos int) int {
	for pos > 0 && isIdentByte(code[pos-1]) {
		pos--
	}
	return pos
}

// lookupBareTarget names the target a bare call of fnName reaches through a from-import.
func (s *pythonIOScope) lookupBareTarget(fnName string) string {
	for i := 0; i < len(s.tracked) && i < maxPythonIOTracked; i++ {
		if s.tracked[i].module == "" && s.tracked[i].local == fnName {
			return s.tracked[i].canonical
		}
	}
	return ""
}

// lookupQualifiedTarget names the target qual.fnName reaches through a tracked module alias.
func (s *pythonIOScope) lookupQualifiedTarget(qual, fnName string) string {
	for i := 0; i < len(s.tracked) && i < maxPythonIOTracked; i++ {
		if s.tracked[i].module != "" && s.tracked[i].local == qual {
			return pythonIOModuleTarget(s.tracked[i].module, fnName)
		}
	}
	return ""
}

// decidePythonIOCall records a HISS-02 violation for a target call that passes no deadline.
func (s *pythonScanner) decidePythonIOCall(canonical string, openLine int, args []string) {
	if hasPythonIOTimeout(args) || hasPositionalURLOpenTimeout(canonical, args) {
		return
	}
	recordViolation(s.rep, "HISS-02", s.rel, openLine, "",
		fmt.Sprintf("Python %s call passes no timeout=: explicit deadline on every I/O call", canonical))
}

// reportPythonIOBound records a call that stays open past maxPythonIOCallLines, failing closed.
func (s *pythonScanner) reportPythonIOBound(canonical string, openLine int) {
	recordViolation(s.rep, "HISS-02", s.rel, openLine, "",
		fmt.Sprintf("Python %s call exceeds 60-line bound without closing: explicit deadline on every I/O call", canonical))
}

// hasPythonIOTimeout reports a top-level timeout= that is not None, or a top-level ** unpack.
func hasPythonIOTimeout(args []string) bool {
	for i := 0; i < len(args) && i < maxPythonIOCallArgs; i++ {
		if isPythonIOTimeoutArg(args[i]) {
			return true
		}
	}
	return false
}

// urlopenTimeoutPosition is the index of urlopen's timeout parameter, urlopen(url, data=None,
// timeout=...): of the targets it is the only one whose deadline can be passed by position.
const urlopenTimeoutPosition = 2

// hasPositionalURLOpenTimeout reports an urlopen call whose third argument is positional and not
// None, the deadline urlopen(url, data, 10) passes without the timeout= keyword.
func hasPositionalURLOpenTimeout(canonical string, args []string) bool {
	if canonical != "urllib.request.urlopen" || len(args) <= urlopenTimeoutPosition {
		return false
	}
	for i := 0; i <= urlopenTimeoutPosition; i++ {
		if isKeywordOrUnpackArg(args[i]) {
			return false
		}
	}
	val := strings.TrimSpace(args[urlopenTimeoutPosition])
	return val != "" && val != "None"
}

// isKeywordOrUnpackArg reports an argument that is not positional: name=value, *args or **kwargs.
func isKeywordOrUnpackArg(arg string) bool {
	trimmed := strings.TrimSpace(arg)
	if strings.HasPrefix(trimmed, "*") {
		return true
	}
	name := leadingIdent(trimmed)
	if name == "" {
		return false
	}
	rest := strings.TrimSpace(trimmed[len(name):])
	return strings.HasPrefix(rest, "=") && !strings.HasPrefix(rest, "==")
}

// isPythonIOTimeoutArg reports one argument that passes a deadline or cannot be decided.
func isPythonIOTimeoutArg(arg string) bool {
	trimmed := strings.TrimSpace(arg)
	if strings.HasPrefix(trimmed, "**") {
		return len(trimmed) > 2
	}
	if leadingIdent(trimmed) != "timeout" {
		return false
	}
	rest := strings.TrimSpace(trimmed[len("timeout"):])
	if !strings.HasPrefix(rest, "=") || strings.HasPrefix(rest, "==") {
		return false
	}
	val := strings.TrimSpace(rest[1:])
	return val != "" && val != "None"
}
