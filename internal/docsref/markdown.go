// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

// Package docsref checks that the repository's own documentation names only CLI commands,
// subcommands, flags and repository paths that exist.
//
// Documentation drifted without anything reporting it: guides named commands that were
// renamed, flags that were removed and files that moved (BUG-992). scripts/docs_drift.py
// asks the opposite question -- whether a changed surface carries a documentation change --
// so a guide that stops matching unchanged code was never examined. This package reads the
// references where a reader copies them from, inline code spans and fenced shell blocks, and
// checks each one against the code instead of against a hand-maintained list.
package docsref

import (
	"fmt"
	"strings"

	"github.com/cordanaLLM/praetor/internal/util"
)

// maxDocumentLines bounds one Markdown scan (HISS-02).
const maxDocumentLines = util.MaxMarkedBlockLines

// maxSpansPerLine bounds the code spans read from one line (HISS-02).
const maxSpansPerLine = 256

// Directive markers. A block between them is not checked. The reason is mandatory: a
// suppression that says nothing about why it exists cannot be reviewed, and one that is never
// closed would silently switch the check off for the rest of the document, so both fail.
const (
	directiveOff = "<!-- praetor:docs-references:off"
	directiveOn  = "<!-- praetor:docs-references:on -->"
)

// shellLanguages are the fence info strings whose content is read as commands. Other
// fences (YAML, JSON, Go, Mermaid, plain text) quote data or program output, where a word
// after "praetorctl" is not an invocation.
var shellLanguages = map[string]bool{
	"bash": true, "sh": true, "shell": true, "console": true, "shell-session": true,
	"zsh": true, "fish": true, "powershell": true, "pwsh": true, "ps1": true,
}

// Candidate is one piece of Markdown text that may hold references.
type Candidate struct {
	// Line is the 1-based line the text starts on.
	Line int
	// Text is an inline code span's content, or one logical line of a shell fence with
	// backslash continuations joined.
	Text string
	// Shell reports that Text came from a shell fence rather than an inline code span.
	Shell bool
}

// scanState carries one ordered pass over a document.
type scanState struct {
	fence      util.MarkdownFence
	shell      bool
	pending    strings.Builder
	pendingAt  int
	suppressed int // line of the open off directive, 0 when checking
	candidates []Candidate
	problems   []Finding
}

// Scan returns the candidates of one Markdown document and any malformed directive. doc is
// the repository-relative name used in findings.
func Scan(doc, content string) ([]Candidate, []Finding) {
	lines := strings.Split(strings.ReplaceAll(content, "\r\n", "\n"), "\n")
	if len(lines) > maxDocumentLines {
		return nil, []Finding{{Doc: doc, Line: 1, Message: fmt.Sprintf("document exceeds %d lines", maxDocumentLines)}}
	}
	state := &scanState{}
	for index := 0; index < len(lines) && index < maxDocumentLines; index++ {
		state.line(doc, index+1, lines[index])
	}
	state.flush()
	if state.suppressed > 0 {
		state.problems = append(state.problems, Finding{Doc: doc, Line: state.suppressed,
			Message: "praetor:docs-references:off is never closed by " + directiveOn})
	}
	return state.candidates, state.problems
}

// line advances the scan by one physical line.
func (s *scanState) line(doc string, number int, raw string) {
	trimmed := strings.TrimSpace(raw)
	wasOpen := s.fence.Open()
	if s.fence.Inside(trimmed) {
		switch {
		case !wasOpen:
			s.shell = shellLanguages[fenceLanguage(trimmed, s.fence.Marker())]
		case !s.fence.Open():
			s.flush()
		case s.shell && s.suppressed == 0:
			s.shellLine(number, trimmed)
		}
		return
	}
	if s.directive(doc, number, trimmed) || s.suppressed > 0 {
		return
	}
	spans := codeSpans(raw)
	for i := 0; i < len(spans) && i < maxSpansPerLine; i++ {
		s.candidates = append(s.candidates, Candidate{Line: number, Text: spans[i]})
	}
}

// directive records an off or on marker and reports whether the line was one.
func (s *scanState) directive(doc string, number int, trimmed string) bool {
	switch {
	case strings.HasPrefix(trimmed, directiveOff):
		reason := strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(trimmed, directiveOff), "-->"))
		switch {
		case !strings.HasSuffix(trimmed, "-->") || reason == "":
			s.problems = append(s.problems, Finding{Doc: doc, Line: number,
				Message: "praetor:docs-references:off needs a reason on the same line: <!-- praetor:docs-references:off <reason> -->"})
		case s.suppressed > 0:
			s.problems = append(s.problems, Finding{Doc: doc, Line: number,
				Message: fmt.Sprintf("praetor:docs-references:off repeats the one opened on line %d", s.suppressed)})
		}
		if s.suppressed == 0 {
			s.suppressed = number
		}
		return true
	case trimmed == directiveOn:
		if s.suppressed == 0 {
			s.problems = append(s.problems, Finding{Doc: doc, Line: number,
				Message: "praetor:docs-references:on closes no open praetor:docs-references:off"})
		}
		s.suppressed = 0
		return true
	}
	return false
}

// shellLine adds one fenced shell line, joining backslash continuations into one command.
func (s *scanState) shellLine(number int, trimmed string) {
	if s.pending.Len() == 0 {
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			return
		}
		s.pendingAt = number
		trimmed = strings.TrimPrefix(trimmed, "$ ")
	}
	body, continued := strings.CutSuffix(trimmed, "\\")
	s.pending.WriteString(body)
	s.pending.WriteByte(' ')
	if !continued {
		s.flush()
	}
}

// flush emits the shell command collected so far, if any.
func (s *scanState) flush() {
	if s.pending.Len() == 0 {
		return
	}
	s.candidates = append(s.candidates, Candidate{Line: s.pendingAt, Text: strings.TrimSpace(s.pending.String()), Shell: true})
	s.pending.Reset()
}

// fenceLanguage returns the first word of an opening fence's info string, lower-cased.
func fenceLanguage(trimmed, marker string) string {
	info := strings.Fields(strings.TrimPrefix(trimmed, marker))
	if len(info) == 0 {
		return ""
	}
	return strings.ToLower(strings.Trim(info[0], "{}."))
}

// codeSpans returns the content of every inline code span on one line, following CommonMark:
// a span opens with a run of backticks and closes at the next run of the same length, and
// one leading and one trailing space are stripped when both are present.
func codeSpans(line string) []string {
	var spans []string
	for pos := 0; pos < len(line) && len(spans) < maxSpansPerLine; {
		open := strings.IndexByte(line[pos:], '`')
		if open < 0 {
			break
		}
		start := pos + open
		run := backtickRun(line, start)
		end := closingRun(line, start+run, run)
		if end < 0 {
			pos = start + run
			continue
		}
		spans = append(spans, stripSpanPadding(line[start+run:end]))
		pos = end + run
	}
	return spans
}

// backtickRun returns the length of the backtick run starting at index.
func backtickRun(line string, index int) int {
	run := 0
	for index+run < len(line) && line[index+run] == '`' {
		run++
	}
	return run
}

// closingRun returns the index of the next backtick run of exactly length run at or after
// from, or -1 when the span is never closed on this line.
func closingRun(line string, from, run int) int {
	for pos := from; pos < len(line); {
		next := strings.IndexByte(line[pos:], '`')
		if next < 0 {
			return -1
		}
		candidate := pos + next
		length := backtickRun(line, candidate)
		if length == run {
			return candidate
		}
		pos = candidate + length
	}
	return -1
}

// stripSpanPadding applies CommonMark's single-space stripping to a span's content.
func stripSpanPadding(content string) string {
	if len(content) >= 2 && content[0] == ' ' && content[len(content)-1] == ' ' && strings.TrimSpace(content) != "" {
		return content[1 : len(content)-1]
	}
	return content
}
