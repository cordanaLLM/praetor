// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package testsupport

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/cordanaLLM/praetor/internal/util"
)

// MarkdownLineLimit is markdownlint's default MD013 line length.
const MarkdownLineLimit = 80

// maxMarkdownLines bounds one MarkdownFindings scan (HISS-02).
const maxMarkdownLines = 20000

var (
	markdownHeading  = regexp.MustCompile(`^#{1,6} `)
	markdownListItem = regexp.MustCompile(`^\s*(?:[-*+]|\d+[.)]) `)
	markdownFence    = regexp.MustCompile("^\\s*(`{3,}|~{3,})(.*)$")
	markdownDisable  = regexp.MustCompile(`^<!-- markdownlint-(disable|enable)((?: MD\d{3})*) -->$`)
)

// markdownScan is the state of one pass over a document.
type markdownScan struct {
	lines    []string
	offset   int
	inFence  bool
	marker   string // the opening fence's run of backticks or tildes while inFence
	inList   bool
	disabled map[string]bool
	h1       int
	findings []string
}

// MarkdownFindings reports where md breaks the markdownlint rules Praetor's generators
// must hold under markdownlint's default configuration: blank lines around headings
// (MD022), fences (MD031) and lists (MD032), no repeated blank lines (MD012), no line over
// MarkdownLineLimit with whitespace from the limit's last column on (MD013, default
// strict: false), a single H1 (MD025) and one final newline (MD047). A
// `<!-- markdownlint-disable MDnnn -->` line suspends that rule until the matching enable
// line, as markdownlint does, so a scoped disable is honoured and a file-wide one shows up
// as exactly that. A leading YAML front matter block is skipped, as markdownlint skips it,
// so the first line after it counts as the document's first line.
//
// The documentation gate runs the real markdownlint library (tools/markdownlint) with MD013
// off and generated agent paths excluded; an adopter's own lint may run the defaults on
// every file Praetor writes. This subset holds a generator to those defaults in a unit test
// without the gate's Node toolchain.
func MarkdownFindings(md string) []string {
	lines := strings.Split(strings.TrimSuffix(md, "\n"), "\n")
	if len(lines) > maxMarkdownLines {
		return []string{fmt.Sprintf("document exceeds %d lines", maxMarkdownLines)}
	}
	start, _ := util.FrontMatterEnd(lines, maxMarkdownLines)
	scan := &markdownScan{lines: lines[start:], offset: start, disabled: map[string]bool{}}
	if !strings.HasSuffix(md, "\n") || strings.HasSuffix(md, "\n\n") {
		scan.report(len(scan.lines)-1, "MD047", "document must end with exactly one newline")
	}
	for i := 0; i < len(scan.lines) && i < maxMarkdownLines; i++ {
		scan.line(i)
	}
	return scan.findings
}

// report records one finding unless its rule is disabled at this point.
func (s *markdownScan) report(index int, rule, detail string) {
	if s.disabled[rule] {
		return
	}
	s.findings = append(s.findings, fmt.Sprintf("line %d: %s %s", s.offset+index+1, rule, detail))
}

// blank reports whether line index separates blocks: an empty line, a line holding only an
// HTML comment (markdownlint blanks comment text before its rules run), or a position
// outside the document.
func (s *markdownScan) blank(index int) bool {
	if index < 0 || index >= len(s.lines) {
		return true
	}
	text := strings.TrimSpace(s.lines[index])
	return text == "" || (strings.HasPrefix(text, "<!--") && strings.HasSuffix(text, "-->"))
}

// empty reports whether line index holds nothing at all, the MD012 notion of blank.
func (s *markdownScan) empty(index int) bool {
	return index >= 0 && index < len(s.lines) && strings.TrimSpace(s.lines[index]) == ""
}

// line applies every rule to one line.
func (s *markdownScan) line(i int) {
	text := s.lines[i]
	s.lineLength(i, text)
	if m := markdownFence.FindStringSubmatch(text); m != nil && s.fenceLine(m[1], m[2]) {
		s.fence(i, m[1])
		return
	}
	if s.inFence {
		return
	}
	if m := markdownDisable.FindStringSubmatch(text); m != nil {
		s.toggle(m[1] == "disable", strings.Fields(m[2]))
	}
	if s.blank(i) && !s.empty(i) {
		// A comment-only line separates blocks the way a blank line does.
		s.inList = false
		return
	}
	s.structure(i, text)
}

// lineLength applies MD013 as markdownlint's relaxed pattern `^.{79}.*\s.*$` does: a line
// past the limit fails only when whitespace appears from its last column on, so an
// unbreakable token running past the limit is allowed.
func (s *markdownScan) lineLength(i int, text string) {
	if len(text) > MarkdownLineLimit && strings.ContainsAny(text[MarkdownLineLimit-1:], " \t") {
		s.report(i, "MD013", fmt.Sprintf("line length %d exceeds %d", len(text), MarkdownLineLimit))
	}
}

// fenceLine reports whether a line made of the fence run marker and the text after it opens or
// closes a fence. Inside one, only a run of the same character at least as long as the opening
// run, followed by nothing, closes it; every other fence-like line is content, as in CommonMark,
// so a four-backtick fence can hold a three-backtick one.
func (s *markdownScan) fenceLine(marker, rest string) bool {
	if !s.inFence {
		return true
	}
	return marker[0] == s.marker[0] && len(marker) >= len(s.marker) && strings.TrimSpace(rest) == ""
}

// fence applies MD031 to an opening or closing fence line whose run is marker.
func (s *markdownScan) fence(i int, marker string) {
	if !s.inFence && !s.blank(i-1) {
		s.report(i, "MD031", "fence must follow a blank line")
	}
	if s.inFence && !s.blank(i+1) {
		s.report(i, "MD031", "fence must be followed by a blank line")
	}
	s.inFence = !s.inFence
	s.marker = marker
}

// toggle applies a disable or enable comment; no rule names means every rule.
func (s *markdownScan) toggle(disable bool, rules []string) {
	if len(rules) == 0 {
		rules = []string{"MD012", "MD013", "MD022", "MD025", "MD031", "MD032"}
	}
	for i := 0; i < len(rules); i++ {
		s.disabled[rules[i]] = disable
	}
}

// structure applies the heading, list and blank-line rules to a line outside fences.
func (s *markdownScan) structure(i int, text string) {
	switch {
	case strings.TrimSpace(text) == "":
		if s.empty(i - 1) {
			s.report(i, "MD012", "multiple consecutive blank lines")
		}
	case markdownListItem.MatchString(text):
		if !s.inList && !s.blank(i-1) {
			s.report(i, "MD032", "list must follow a blank line")
		}
		s.inList = true
	default:
		s.leaveList(i, text)
		if markdownHeading.MatchString(text) {
			s.heading(i, text)
		}
	}
}

// leaveList applies MD032 to the first unindented line after a list. It is stricter than
// markdownlint in one place: a paragraph line directly after an item, which CommonMark reads
// as a lazy continuation of that item, is reported too, since a generator that emits one
// has lost track of the item it is writing.
func (s *markdownScan) leaveList(i int, text string) {
	if !s.inList || strings.HasPrefix(text, " ") {
		return
	}
	if !s.blank(i - 1) {
		s.report(i, "MD032", "list must be followed by a blank line")
	}
	s.inList = false
}

// heading applies MD022 and MD025 to one heading line.
func (s *markdownScan) heading(i int, text string) {
	if !s.blank(i - 1) {
		s.report(i, "MD022", "heading must follow a blank line")
	}
	if !s.blank(i + 1) {
		s.report(i, "MD022", "heading must be followed by a blank line")
	}
	if strings.HasPrefix(text, "# ") {
		s.h1++
		if s.h1 > 1 {
			s.report(i, "MD025", "more than one top-level heading")
		}
	}
}
