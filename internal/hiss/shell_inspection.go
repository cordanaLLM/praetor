// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package hiss

import "strings"

// shellInspection collects executable source scopes alongside the shared lexer's code view.
// Substitutions become placeholders in their parent and separate sources for payload readers.
// Arithmetic bytes are data; substitutions within them still receive their own scope.
type shellInspection struct {
	lexer   shellLexer
	code    strings.Builder
	scopes  []*strings.Builder
	scripts []string
}

const maxInspectionScopes = 4096

func newShellInspection(body bool) *shellInspection {
	scan := &shellInspection{lexer: shellLexer{inspect: true}, scopes: []*strings.Builder{{}}}
	if body {
		scan.lexer.push(shellHeredocText)
	}
	return scan
}

// ShellInspection returns executable code and quote-preserving source scopes, including
// substitutions nested in parameters, arithmetic and expanding heredocs. Literal heredoc
// text never becomes code. Input must already satisfy the caller's byte and physical-line
// bounds before continuation joining; maxLines bounds the logical scan too.
func ShellInspection(script string, maxLines int) (string, []string, bool) {
	physical := strings.Split(strings.ReplaceAll(script, "\r\n", "\n"), "\n")
	if len(physical) > maxLines {
		return "", nil, false
	}
	scan := newShellInspection(false)
	var body *shellInspection
	var code strings.Builder
	for _, text := range physical {
		if len(scan.lexer.bodies) > 0 {
			var ok bool
			body, ok = scan.inspectBody(text, body, &code)
			if !ok {
				return "", nil, false
			}
			continue
		}
		scan.scanText(text)
		code.WriteString(scan.code.String())
		code.WriteByte('\n')
		scan.code.Reset()
		scan.lexer.finishLine()
		scope := scan.scopes[len(scan.scopes)-1]
		scope.WriteByte('\n')
	}
	if scan.lexer.misread || scan.lexer.open() || len(scan.scripts) >= maxInspectionScopes {
		return "", nil, false
	}
	return code.String(), append(scan.scripts, scan.scopes[0].String()), true
}

// inspectBody keeps multiline substitutions open across expanding-heredoc body lines.
// A delimiter reached with an unfinished substitution fails closed; nested heredocs in
// such a substitution are ambiguous to this view and also fail closed.
func (s *shellInspection) inspectBody(text string, body *shellInspection, code *strings.Builder) (*shellInspection, bool) {
	h := s.lexer.bodies[0]
	if h.stripTabs {
		text = strings.TrimLeft(text, "\t")
	}
	if text == h.delimiter {
		if body != nil {
			if body.lexer.misread || len(body.lexer.stack) != 1 {
				return nil, false
			}
			s.scripts = append(s.scripts, body.scripts...)
		}
		s.lexer.bodyLine(text)
		return nil, len(s.scripts) < maxInspectionScopes
	}
	if h.quoted {
		return body, true
	}
	if body == nil {
		body = newShellInspection(true)
	}
	body.scanText(text)
	code.WriteString(body.code.String())
	code.WriteByte('\n')
	body.code.Reset()
	scope := body.scopes[len(body.scopes)-1]
	scope.WriteByte('\n')
	return body, !body.lexer.misread && len(body.lexer.pending) == 0
}

// scanText advances only through lexer-consumed spans, sharing quote and escape semantics.
func (s *shellInspection) scanText(text string) {
	for i := 0; i < len(text) && len(s.scripts) < maxInspectionScopes; {
		before := s.context()
		depth := len(s.lexer.stack)
		codeLen := s.code.Len()
		next := min(s.lexer.step(text, i, &s.code), len(text))
		if text[i] != '#' || s.code.Len() != codeLen || !inspectionExecutable(before) {
			s.sourceSpan(inspectionRawSpan(text, i, next, before), before, depth)
		}
		i = next
	}
	if len(s.scripts) >= maxInspectionScopes {
		s.lexer.misread = true
	}
}

func (s *shellInspection) context() shellContext {
	if frame := s.lexer.top(); frame != nil {
		return frame.ctx
	}
	return shellCode
}

// sourceSpan isolates executable substitutions without corrupting their parent's quotes.
// Arithmetic children remain inspected but never introduce a parent command word.
func (s *shellInspection) sourceSpan(raw string, before shellContext, depth int) {
	after := s.context()
	current := s.scopes[len(s.scopes)-1]
	if len(s.lexer.stack) > depth {
		writeInspectionPlaceholder(current, before, after)
	}
	if len(s.lexer.stack) > depth && (after == shellCommand || after == shellBacktick) {
		s.scopes = append(s.scopes, &strings.Builder{})
		return
	}
	if len(s.lexer.stack) < depth && (before == shellCommand || before == shellBacktick) {
		s.scripts = append(s.scripts, current.String())
		s.scopes = s.scopes[:len(s.scopes)-1]
		return
	}
	if inspectionData(before, after) {
		return
	}
	writeInspectionSource(current, raw, before)
}

// writeInspectionPlaceholder retains expansion word identity, including a$((1))b.
// Arithmetic commands separate scopes; their nested substitutions add no executable word.
func writeInspectionPlaceholder(current *strings.Builder, before, after shellContext) {
	if inspectionData(before, before) {
		return
	}
	switch after {
	case shellCommand, shellBacktick, shellArithExpansion:
		current.WriteString(shellExpansion)
	case shellArithCommand:
		current.WriteString(" ; ")
	}
}

// inspectionData blanks arithmetic and ordinary heredoc bytes only, preserving child code.
func inspectionData(before, after shellContext) bool {
	return before == shellHeredocText || before == shellArithCommand || before == shellArithExpansion ||
		after == shellArithCommand || after == shellArithExpansion
}

// inspectionExecutable distinguishes command source from literal and expansion text.
func inspectionExecutable(ctx shellContext) bool {
	return ctx == shellCode || ctx == shellCommand || ctx == shellBacktick
}

// inspectionRawSpan keeps redirect-both within one command for shell-word readers.
// The separate code view retains &>, so its dialect-sensitive policy still judges it.
func inspectionRawSpan(text string, start, end int, ctx shellContext) string {
	if text[start:end] == "&" && end < len(text) && text[end] == '>' && inspectionExecutable(ctx) {
		return ""
	}
	if inspectionExecutable(ctx) && inspectionBraceSeparator(text, start, end) {
		return " ; "
	}
	return text[start:end]
}

// inspectionBraceSeparator recognizes grouping keywords, preserving braces inside words.
// Payload readers must see expanding executable words intact so they can fail closed.
func inspectionBraceSeparator(text string, start, end int) bool {
	return end == start+1 && (reservedOpen(text, start) || text[start] == '}' && reservedClose(text, start))
}

// inspectionSeparator reports a lone parenthesis, which separates commands in an inspected scope.
func inspectionSeparator(raw string) bool {
	return len(raw) == 1 && strings.ContainsAny(raw, "()")
}

// writeInspectionSource separates syntactic operators, including adjacent redirections.
// Shell-word readers must never read sh<<EOF or sh>out as opaque executable names.
func writeInspectionSource(current *strings.Builder, raw string, ctx shellContext) {
	if inspectionExecutable(ctx) && inspectionSeparator(raw) {
		current.WriteString(" ; ")
		return
	}
	if inspectionExecutable(ctx) && strings.HasPrefix(raw, "<") {
		current.WriteString(" " + raw + " ")
		return
	}
	if inspectionExecutable(ctx) && strings.HasPrefix(raw, ">") {
		current.WriteString(" " + raw + " ")
		return
	}
	current.WriteString(raw)
}
