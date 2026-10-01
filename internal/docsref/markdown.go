// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

// Package docsref checks that the repository's own documentation names only CLI commands,
// subcommands, flags and repository paths that exist, and that a change to a declared
// user-facing surface carries a documentation change (drift.go).
//
// Documentation drifted without anything reporting it: guides named commands that were
// renamed, flags that were removed and files that moved (BUG-992). The drift check asks the
// opposite question -- whether a changed surface carries a documentation change -- so a guide
// that stops matching unchanged code was never examined. This package reads the references
// where a reader copies them from, inline code spans and fenced shell blocks, and checks each
// one against the code instead of against a hand-maintained list.
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

// Directive markers. A block between them is not checked. The reason is mandatory and must
// say something (minReasonWords): a suppression that says nothing about why it exists cannot
// be reviewed, and one that is never closed would silently switch the check off for the rest
// of the document, so both fail. Every accepted block is reported with its reason.
const (
	directiveOff   = "<!-- praetor:docs-references:off"
	directiveOn    = "<!-- praetor:docs-references:on -->"
	minReasonWords = 3
)

// fenceKind says how a fence's lines are read; util.MarkdownShellFence, the one table of
// command fences, decides it. util.ShellNone is any fence that is not a shell (YAML, JSON,
// Go, Mermaid, plain text), where a word after "praetorctl" is not a call; util.ShellScript
// reads every line that is not a comment as a command; util.ShellSession reads only a line
// after the "$ " prompt as one, every other line being the output it printed.
type fenceKind = util.MarkdownShell

// Suppression is one accepted praetor:docs-references:off block.
type Suppression struct {
	Line   int
	Reason string
}

// ScanResult is what one document yields: the text to check, malformed directives, and the
// blocks suppressed by a reasoned directive.
type ScanResult struct {
	Candidates   []Candidate
	Problems     []Finding
	Suppressions []Suppression
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
	kind       fenceKind
	pending    strings.Builder
	pendingAt  int
	suppressed int // line of the open off directive, 0 when checking
	result     ScanResult
}

// Scan returns what one Markdown document yields. doc is the repository-relative name used
// in findings.
func Scan(doc, content string) ScanResult {
	lines := strings.Split(strings.ReplaceAll(content, "\r\n", "\n"), "\n")
	if len(lines) > maxDocumentLines {
		return ScanResult{Problems: []Finding{{Doc: doc, Line: 1, Message: fmt.Sprintf("document exceeds %d lines", maxDocumentLines)}}}
	}
	state := &scanState{}
	for index := 0; index < len(lines) && index < maxDocumentLines; index++ {
		state.line(doc, index+1, lines[index])
	}
	state.flush()
	if state.suppressed > 0 {
		state.result.Problems = append(state.result.Problems, Finding{Doc: doc, Line: state.suppressed,
			Message: "praetor:docs-references:off is never closed by " + directiveOn})
	}
	return state.result
}

// line advances the scan by one physical line.
func (s *scanState) line(doc string, number int, raw string) {
	trimmed := strings.TrimSpace(raw)
	wasOpen := s.fence.Open()
	if s.fence.Inside(trimmed) {
		switch {
		case !wasOpen:
			s.kind = util.MarkdownShellFence(util.MarkdownFenceLanguage(trimmed, s.fence.Marker()))
		case !s.fence.Open():
			s.flush()
		case s.kind != util.ShellNone && s.suppressed == 0:
			s.shellLine(number, trimmed)
		}
		return
	}
	if s.directive(doc, number, trimmed) || s.suppressed > 0 {
		return
	}
	spans := codeSpans(raw)
	for i := 0; i < len(spans) && i < maxSpansPerLine; i++ {
		s.result.Candidates = append(s.result.Candidates, Candidate{Line: number, Text: spans[i]})
	}
}

// directive records an off or on marker and reports whether the line was one.
func (s *scanState) directive(doc string, number int, trimmed string) bool {
	switch {
	case strings.HasPrefix(trimmed, directiveOff):
		reason := strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(trimmed, directiveOff), "-->"))
		switch {
		case !strings.HasSuffix(trimmed, "-->") || len(strings.Fields(reason)) < minReasonWords:
			s.result.Problems = append(s.result.Problems, Finding{Doc: doc, Line: number, Message: fmt.Sprintf(
				"praetor:docs-references:off needs a reason of at least %d words on the same line: <!-- praetor:docs-references:off <reason> -->",
				minReasonWords)})
		case s.suppressed > 0:
			s.result.Problems = append(s.result.Problems, Finding{Doc: doc, Line: number,
				Message: fmt.Sprintf("praetor:docs-references:off repeats the one opened on line %d", s.suppressed)})
		default:
			s.result.Suppressions = append(s.result.Suppressions, Suppression{Line: number, Reason: reason})
		}
		if s.suppressed == 0 {
			s.suppressed = number
		}
		return true
	case trimmed == directiveOn:
		if s.suppressed == 0 {
			s.result.Problems = append(s.result.Problems, Finding{Doc: doc, Line: number,
				Message: "praetor:docs-references:on closes no open praetor:docs-references:off"})
		}
		s.suppressed = 0
		return true
	}
	return false
}

// shellLine adds one fenced shell line, joining backslash continuations into one command. In
// a terminal transcript only a line after the "$ " prompt starts a command; the lines between
// prompts are output, such as "praetorctl version dev", and are not read.
func (s *scanState) shellLine(number int, trimmed string) {
	if s.pending.Len() == 0 {
		command, ok := util.MarkdownShellCommand(s.kind, trimmed)
		if !ok {
			return
		}
		s.pendingAt = number
		trimmed = command
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
	s.result.Candidates = append(s.result.Candidates, Candidate{Line: s.pendingAt, Text: strings.TrimSpace(s.pending.String()), Shell: true})
	s.pending.Reset()
}

// codeSpans returns the content of every inline code span on one line, as
// util.MarkdownCodeSpans reads it: a span opens with a run of backticks and closes at the
// next run of the same length, and one leading and one trailing space are stripped when
// both are present.
func codeSpans(line string) []string {
	found := util.MarkdownCodeSpans(line, maxSpansPerLine)
	spans := make([]string, 0, len(found))
	for _, span := range found {
		spans = append(spans, span.Content(line))
	}
	return spans
}
