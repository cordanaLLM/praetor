// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package hiss

import (
	"bytes"
	"fmt"
	slashpath "path"
	"regexp"
	"slices"
	"strings"
)

// POSIX shell and Bash scripts.
//
// The scanner reads .sh and .bash files, and files without an extension whose interpreter line
// names sh, bash, dash or ash (#!/bin/sh, #!/usr/bin/env bash). A .sh or .bash file whose
// interpreter line names another program (zsh, python) is declined. The lexer (shell_lex.go)
// blanks quotes, comments, expansions and here-document bodies, and the commands of each
// logical line are read word by word (shell_commands.go). It decides:
//
//   - HISS-01 direct recursion: a function whose body runs its own name as a command. A call
//     through command, builtin or exec never reaches a function, so it is not reported.
//   - HISS-02 the unbounded while true, while :, until false and for ((;;)) loops, and a curl
//     transfer without --max-time (-m) that no timeout command bounds.
//   - HISS-04 the length of every function whose body is a brace group, from the line its body
//     opens on to the line it closes on.
//   - HISS-07 a script with an interpreter line that never enables errexit and nounset (set -eu)
//     and, under Bash, pipefail, and a failure discarded by || true or || :.
//   - HISS-08 eval, text piped into a shell that reads its script from standard input
//     (curl ... | sh), and a process substitution run as a script (source <(...)).
//
// A file holding a NUL byte is not a script and is declined. So is a file whose quotes,
// substitutions, here-documents or braces the scanner misread: its structure is unknown, so
// none of its findings is reported and no rule's silence over it counts as a pass.

const (
	// maxShellOpenFunctions bounds the stack of open functions (HISS-02).
	maxShellOpenFunctions = 64
	// maxShellClosedFunctions bounds the functions one file records for HISS-01.
	maxShellClosedFunctions = 4096
	// maxShellCommandSites bounds the command names one file records for HISS-01.
	maxShellCommandSites = 1 << 16
	// maxShellJoinedLines bounds the physical lines joined into one logical line.
	maxShellJoinedLines = 64
	// maxShellHeaderAge bounds the lines between a function header and its body brace.
	maxShellHeaderAge = 3
)

// shellFuncHeader matches a function definition at the start of a line: `name()`, `name ()`,
// `function name` and `function name()`, with the blanks after it.
var shellFuncHeader = regexp.MustCompile(`^\s*(?:function\s+([A-Za-z_][\w.:-]*)(?:\s*\(\s*\))?|([A-Za-z_][\w.:-]*)\s*\(\s*\))\s*`)

// shellForEver is the arithmetic for loop without a condition.
var shellForEver = regexp.MustCompile(`\bfor\s*\(\(\s*;\s*;\s*\)\)`)

// shellLanguages are the interpreters whose scripts this scanner reads.
var shellLanguages = map[string]bool{"sh": true, "bash": true, "dash": true, "ash": true}

// shellLanguage reads POSIX shell and Bash scripts.
type shellLanguage struct{}

func (shellLanguage) handles(ext string) bool { return ext == ".sh" || ext == ".bash" }

func (shellLanguage) name() string { return "shell" }

// candidate admits a file without an extension, which only its interpreter line can name.
func (shellLanguage) candidate(_, ext string) bool { return ext == "" }

func (shellLanguage) claims(src sourceFile) bool {
	first, _, _ := strings.Cut(string(src.data), "\n")
	return shellInterpreterLine(first).known
}

func (shellLanguage) scan(src sourceFile, rep *ScanReport, opts ScanOptions) bool {
	if bytes.IndexByte(src.data, 0) >= 0 {
		return false
	}
	lines := src.lfLines()
	interp := shellInterpreterLine(lines[0])
	if interp.shebang && !interp.known {
		return false
	}
	interp.bash = interp.bash || strings.HasSuffix(strings.ToLower(src.rel), ".bash")
	// The findings are collected per file and reported only once the file was read cleanly.
	s := &shellScanner{rel: src.rel, rep: &ScanReport{}, maxLOC: opts.MaxFuncLOC, interp: interp}
	s.strict.observe(interp.args)
	for idx, line := range lines {
		s.observe(idx+1, line)
	}
	s.flush()
	if s.misread() {
		return false
	}
	s.reportRecursion()
	s.reportStrictMode()
	// Recursion and strict mode are decided at the end of the file.
	recordInLineOrder(rep, s.rep)
	return true
}

// recordInLineOrder records a file's findings, collected rule by rule, in line order, so a
// scanner whose rules run in passes reports a file the way a reader walks it.
func recordInLineOrder(rep, file *ScanReport) {
	slices.SortStableFunc(file.Violations, func(a, b InvariantViolation) int { return a.LineNumber - b.LineNumber })
	for _, v := range file.Violations {
		recordViolation(rep, v.RuleID, v.FilePath, v.LineNumber, v.Symbol, v.Message)
	}
}

// shellInterp is what a script's #! line says.
type shellInterp struct {
	// shebang is true when the first line is an interpreter line.
	shebang bool
	// known is true when it names a shell this scanner reads.
	known bool
	// bash is true when that shell is Bash.
	bash bool
	// args are the words after the interpreter, such as -eu.
	args []string
}

// shellInterpreterLine reads a #! line, following /usr/bin/env to the program it starts.
func shellInterpreterLine(first string) shellInterp {
	first = strings.TrimSuffix(first, "\r")
	if !strings.HasPrefix(first, "#!") {
		return shellInterp{}
	}
	words := strings.Fields(first[2:])
	at := 0
	if len(words) > 0 && slashpath.Base(words[0]) == "env" {
		at = envProgram(words)
	}
	if at >= len(words) {
		return shellInterp{shebang: true}
	}
	program := slashpath.Base(words[at])
	return shellInterp{shebang: true, known: shellLanguages[program], bash: program == "bash", args: words[at+1:]}
}

// envProgram returns the index of the program env starts: past env's options (-S, -i, -u NAME)
// and its NAME=value assignments.
func envProgram(words []string) int {
	i := 1
	for i < len(words) {
		switch w := words[i]; {
		case w == "-u":
			i += 2
		case strings.HasPrefix(w, "-"), strings.Contains(w, "="):
			i++
		default:
			return i
		}
	}
	return i
}

// shellFunc is one function whose body is a brace group, with its span in code coordinates.
type shellFunc struct {
	name                string
	startLine, startCol int
	endLine, endCol     int
	depth               int
}

// contains reports whether the code position line:col lies inside the function's body.
func (f shellFunc) contains(line, col int) bool {
	after := line > f.startLine || (line == f.startLine && col > f.startCol)
	before := line < f.endLine || (line == f.endLine && col < f.endCol)
	return after && before
}

// shellSegment maps an offset of a logical line back to the physical line it came from.
type shellSegment struct {
	offset, line int
}

// shellSite is one command name and where it runs.
type shellSite struct {
	name      string
	line, col int
}

// shellStrict records the strict-mode options a script enables.
type shellStrict struct {
	errexit, nounset, pipefail bool
}

// shellScanner carries the cross-line state of one script.
type shellScanner struct {
	rel    string
	rep    *ScanReport
	maxLOC int
	interp shellInterp
	lex    shellLexer
	// depth is the brace-group depth of the code read so far.
	depth      int
	unbalanced bool
	open       []shellFunc
	closed     []shellFunc
	// header is the function whose header line ended before its body brace, when headerSet.
	header    string
	headerSet bool
	headerAge int
	// logical holds the physical lines joined by a trailing backslash or operator.
	logical  strings.Builder
	segments []shellSegment
	sites    []shellSite
	strict   shellStrict
}

// observe feeds one physical line.
func (s *shellScanner) observe(lineNum int, line string) {
	code, heredoc := s.lex.line(line)
	if heredoc {
		return
	}
	masked, bodyAt, name := s.findHeader(code)
	s.braces(masked, lineNum, bodyAt, name)
	s.appendLogical(masked, lineNum)
}

// findHeader finds the function whose body brace is on this line. It returns the line with the
// header blanked, so the definition is not read as a call of its own name, the index of the body
// brace (-1 when none opens here) and the function's name.
func (s *shellScanner) findHeader(code string) (string, int, string) {
	if s.headerSet {
		if at, waiting := s.pendingBody(code); waiting || at >= 0 {
			return code, at, s.header
		}
	}
	m := shellFuncHeader.FindStringSubmatchIndex(code)
	if m == nil {
		return code, -1, ""
	}
	name := headerName(code, m)
	masked := strings.Repeat(" ", m[1]) + code[m[1]:]
	switch rest := code[m[1]:]; {
	case rest == "":
		s.header, s.headerSet, s.headerAge = name, true, 0
	case rest[0] == '{' && reservedOpen(code, m[1]):
		return masked, m[1], name
	}
	return masked, -1, ""
}

// pendingBody reads a line after a header that ended before its body brace. It returns the index
// of the body brace when the line opens it, or reports that the header still waits (a blank or
// comment line); any other line ends the wait, since the body is then not a brace group.
func (s *shellScanner) pendingBody(code string) (int, bool) {
	trimmed := strings.TrimSpace(code)
	if trimmed == "" && s.headerAge < maxShellHeaderAge {
		s.headerAge++
		return -1, true
	}
	s.headerSet = false
	if at := strings.IndexByte(code, '{'); trimmed != "" && trimmed[0] == '{' && reservedOpen(code, at) {
		return at, false
	}
	return -1, false
}

// headerName returns the function name a shellFuncHeader match captured, from whichever of its
// two forms matched.
func headerName(code string, m []int) string {
	if m[2] >= 0 {
		return code[m[2]:m[3]]
	}
	return code[m[4]:m[5]]
}

// reservedOpen reports whether the { at i is the reserved word that opens a brace group, not
// part of a word such as a brace expansion.
func reservedOpen(code string, i int) bool {
	if i < 0 || i >= len(code) || code[i] != '{' {
		return false
	}
	before := i == 0 || strings.IndexByte(" \t;&|()", code[i-1]) >= 0
	return before && (i+1 == len(code) || code[i+1] == ' ' || code[i+1] == '\t')
}

// reservedClose reports whether the } at i is the reserved word that closes a brace group.
func reservedClose(code string, i int) bool {
	before := i == 0 || strings.IndexByte(" \t;&", code[i-1]) >= 0
	return before && (i+1 == len(code) || strings.IndexByte(" \t;&|)<>", code[i+1]) >= 0)
}

// braces follows the brace groups of one line: the function whose body brace is at bodyAt
// opens, and every function whose body closes on the line is measured.
func (s *shellScanner) braces(code string, lineNum, bodyAt int, name string) {
	for i := 0; i < len(code); i++ {
		switch {
		case reservedOpen(code, i):
			s.depth++
			if i == bodyAt && len(s.open) < maxShellOpenFunctions {
				s.open = append(s.open, shellFunc{name: name, startLine: lineNum, startCol: i, depth: s.depth})
			}
		case code[i] == '}' && reservedClose(code, i):
			s.depth--
			if s.depth < 0 {
				s.depth, s.unbalanced = 0, true
			}
			s.closeDeeper(lineNum, i)
		}
	}
}

// closeDeeper closes every open function whose body the brace at line:col ended.
func (s *shellScanner) closeDeeper(lineNum, col int) {
	for i := 0; i < maxShellOpenFunctions && len(s.open) > 0; i++ {
		top := s.open[len(s.open)-1]
		if s.depth >= top.depth {
			return
		}
		top.endLine, top.endCol = lineNum, col
		if length := lineNum - top.startLine + 1; length > s.maxLOC {
			recordViolation(s.rep, "HISS-04", s.rel, top.startLine, top.name,
				fmt.Sprintf("Function '%s' (%d LOC) exceeds HISS-04 / NASA Rule 4 limit of %d LOC", top.name, length, s.maxLOC))
		}
		if len(s.closed) < maxShellClosedFunctions {
			s.closed = append(s.closed, top)
		}
		s.open = s.open[:len(s.open)-1]
	}
}

// appendLogical adds one physical line to the logical line being built. A line ending in a
// backslash, a pipe or && or || continues on the next; any other line completes it.
func (s *shellScanner) appendLogical(code string, lineNum int) {
	text := strings.TrimRight(code, " \t")
	continued := strings.HasSuffix(text, `\`)
	if continued {
		text = text[:len(text)-1]
	}
	s.segments = append(s.segments, shellSegment{offset: s.logical.Len(), line: lineNum})
	s.logical.WriteString(text)
	s.logical.WriteByte(' ')
	continued = continued || strings.HasSuffix(text, "|") || strings.HasSuffix(text, "&&")
	if continued && len(s.segments) < maxShellJoinedLines {
		return
	}
	s.flush()
}

// flush reads the commands of the logical line built so far.
func (s *shellScanner) flush() {
	text := s.logical.String()
	if text == "" {
		return
	}
	if loc := shellForEver.FindStringIndex(text); loc != nil {
		line, _ := s.position(loc[0])
		recordViolation(s.rep, "HISS-02", s.rel, line, "", "Unbounded loop in shell without an explicit scalar bound")
	}
	for _, cmd := range splitShellCommands(text) {
		s.checkCommand(cmd)
	}
	s.logical.Reset()
	s.segments = s.segments[:0]
}

// position maps an offset of the logical line to its physical line and code column.
func (s *shellScanner) position(offset int) (int, int) {
	for i := len(s.segments) - 1; i >= 0; i-- {
		if offset >= s.segments[i].offset {
			return s.segments[i].line, offset - s.segments[i].offset
		}
	}
	return 0, offset
}

// misread reports, at the end of the file, whether its structure was misread somewhere: an open
// quote, substitution or here-document, an unbalanced brace, or a function still open. Such a
// file is declined rather than counted as read, which a gate would take for clean.
func (s *shellScanner) misread() bool {
	return s.lex.misread || s.lex.open() || s.unbalanced || s.depth != 0 || len(s.open) > 0
}

// reportRecursion reports every command that runs a function's own name inside its body.
func (s *shellScanner) reportRecursion() {
	for _, f := range s.closed {
		calls := selfCalls{name: f.name}
		for _, site := range s.sites {
			if site.name == f.name && f.contains(site.line, site.col) {
				calls.addSite(site.line)
			}
		}
		calls.report(s.rep, s.rel)
	}
}

// reportStrictMode reports a script whose interpreter line starts a shell that never enables
// the options that stop it at a failure. A file without one is a library its caller sources,
// which runs under the caller's options.
func (s *shellScanner) reportStrictMode() {
	if !s.interp.shebang {
		return
	}
	var missing []string
	if !s.strict.errexit {
		missing = append(missing, "set -e")
	}
	if !s.strict.nounset {
		missing = append(missing, "set -u")
	}
	if s.interp.bash && !s.strict.pipefail {
		missing = append(missing, "set -o pipefail")
	}
	if len(missing) > 0 {
		recordViolation(s.rep, "HISS-07", s.rel, 1, "", "Shell strict mode incomplete: "+strings.Join(missing, ", ")+
			" not enabled, so a failed command, an unset variable or a failure inside a pipeline passes unchecked")
	}
}
