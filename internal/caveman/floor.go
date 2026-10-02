package caveman

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/cordanaLLM/praetor/internal/util"
)

// Floor rule identifiers: the clarity floor a rewrite must hold.
const (
	RuleFloorCodeSpan    = "F1 code-span-lost"
	RuleFloorCommand     = "F2 command-lost"
	RuleFloorID          = "F3 id-lost"
	RuleFloorLink        = "F4 link-lost"
	RuleFloorMarker      = "F5 marker-lost"
	RuleFloorMust        = "F6 must-dropped"
	RuleFloorProhibition = "F7 prohibition-dropped"
	RuleFloorNumbered    = "F8 numbered-rule-dropped"
	// RuleFloorNumber fires when a number of the prose, a table cell, another line outside
	// fenced code, the code of a source or data fence or the output of a terminal session is
	// gone: the Caveman skill copies numbers, versions included, verbatim like code and ids
	// (#363). A word that holds a digit is one number fact (numberFact).
	RuleFloorNumber = "F9 number-lost"
	// RuleFloorCodeWord fires when an identifier, key or word of the code of a source or
	// data fence, or of the output of a terminal session, is gone. Its comments are prose and
	// may change; its code may not (#322).
	RuleFloorCodeWord = "F10 code-token-lost"
)

var (
	// idRe matches rule and record ids such as HISS-17, ADR-0010, BUG-012 and Q-3.
	idRe          = regexp.MustCompile(`\b[A-Z][A-Z0-9]*-\d+\b`)
	linkCaptureRe = regexp.MustCompile(`\]\(([^)\s]+)\)`)
	markerRe      = regexp.MustCompile(`<!--.*?-->`)
	mustRe        = regexp.MustCompile(`\b(?:MUST|SHALL|REQUIRED)\b`)
	prohibitionRe = regexp.MustCompile(`(?i)\b(?:never|do not|don't|must not|no)\b`)
	numberedRe    = regexp.MustCompile(`^\s*\d+\.\s+\*\*`)
	// numberTokenRe matches a word that may carry a number: letters, digits and underscores
	// joined by single dots, commas or hyphens, with the sign before it. A word that holds a
	// digit is one number fact, whole (numberFact): 557.3, 1,024, -5, v8.6.0, p99.94, utf-8.
	numberTokenRe = regexp.MustCompile(`(?:^|[^\pL\pN_])([+-]?[\pL\pN_]+(?:[.,-][\pL\pN_]+)*)`)
	// numericLiteralRe matches the numeric literal that opens a word, sign included.
	numericLiteralRe = regexp.MustCompile(`^[+-]?\d+(?:[.,]\d+)*`)
	digitRe          = regexp.MustCompile(`\d`)
	// codeTokenRe matches an identifier, key or word of code.
	codeTokenRe = regexp.MustCompile(`[\pL_][\pL\pN_]*`)
	// Markdown syntax whose digits are labels a rewrite may change, not facts of the text: an
	// ordered list marker, also inside a blockquote; the section number of a heading; the
	// label of a reference link, a reference definition and a footnote. HTML entities
	// (runtimeHTMLEntityRe) render as a character, so their code is no number a reader sees.
	orderedMarkerRe = regexp.MustCompile(`^[\s>]*\d+[.)](?:\s|$)`)
	headingNumberRe = regexp.MustCompile(`^([\s>]*#{1,6}[ \t]+)(?:\d+\.)+\d*(?:[ \t]|$)`)
	refLabelRe      = regexp.MustCompile(`\]\[[^\]]*\]|\[\^[^\]]*\]|^[\s>]*\[[^\]]+\]:`)
)

// setRules are facts that must survive verbatim; countRules are directives whose number
// must not fall. Both lists fix the order in which findings are produced.
var (
	setRules = []string{RuleFloorCodeSpan, RuleFloorCommand, RuleFloorID, RuleFloorLink, RuleFloorMarker,
		RuleFloorNumber, RuleFloorCodeWord}
	countRules = []string{RuleFloorMust, RuleFloorProhibition, RuleFloorNumbered}
)

// facts is what a reader of a text must still find after a rewrite: verbatim items with the
// line they first appear on, and directive counts. kept holds, for the rules whose items may
// move into code, every item the text shows a reader anywhere outside a comment: a number
// in prose that moves into a code span or a command, or a code token that moves into a code
// span, is still there.
type facts struct {
	items  map[string]map[string]int
	kept   map[string]map[string]bool
	counts map[string]int
}

// Floor fails when after lost something before carried: a code span, a fenced command, an
// id such as HISS-17, a link target, an HTML marker, a number, a code token of a source or
// data fence or of session output, a MUST-type directive, a prohibition (never, do not, no)
// or a numbered rule. Findings of lost items sit on their line in before; count findings sit
// on line 0. Moving a fact is fine; dropping it is not.
func Floor(before, after string) Report {
	had, has := extractFacts(before), extractFacts(after)
	var found findings
	for _, rule := range setRules {
		for item, num := range had.items[rule] {
			if !has.holds(rule, item) {
				found.add(num, rule, item)
			}
		}
	}
	for _, rule := range countRules {
		if has.counts[rule] < had.counts[rule] {
			found.add(0, rule, fmt.Sprintf("%d -> %d", had.counts[rule], has.counts[rule]))
		}
	}
	return Report{Findings: found.sorted()}
}

func extractFacts(text string) facts {
	f := facts{
		items:  map[string]map[string]int{},
		kept:   map[string]map[string]bool{RuleFloorNumber: {}, RuleFloorCodeWord: {}},
		counts: map[string]int{},
	}
	for _, rule := range setRules {
		f.items[rule] = map[string]int{}
	}
	lines, _ := scan(text)
	for _, span := range codeSpanTexts(lines) {
		f.collect(RuleFloorCodeSpan, span.num, []string{span.text})
		f.keep(RuleFloorCodeWord, codeTokenRe.FindAllString(span.text, -1))
	}
	var fence fenceState
	for _, ln := range lines {
		f.collect(RuleFloorID, ln.num, idRe.FindAllString(ln.text, -1))
		if ln.kind == kindCode {
			fence = f.collectFenced(ln, fence)
			continue
		}
		fence = fenceState{}
		f.collectLine(ln)
	}
	return f
}

// fenceState is what one fenced line leaves the next: whether a command continues on it, and
// the closer of a block comment still open (util.StripComments).
type fenceState struct {
	continued bool
	comment   string
}

// collectLine records the facts of one line outside fenced code apart from its code spans
// and ids, which extractFacts reads. Every number the line shows a reader, outside an HTML
// marker and an entity, stays kept: in a code span, a link label, a list marker or a heading
// number too, so prose steps rewritten as a numbered list keep their numbers.
func (f facts) collectLine(ln line) {
	f.collect(RuleFloorMarker, ln.num, markerRe.FindAllString(ln.text, -1))
	for _, match := range linkCaptureRe.FindAllStringSubmatch(ln.text, -1) {
		f.collect(RuleFloorLink, ln.num, match[1:])
	}
	f.collect(RuleFloorNumber, ln.num, numbersOf(ln))
	f.keep(RuleFloorNumber, numbersIn(runtimeHTMLEntityRe.ReplaceAllString(markerRe.ReplaceAllString(ln.text, " "), " ")))
	f.counts[RuleFloorMust] += len(mustRe.FindAllString(ln.text, -1))
	f.counts[RuleFloorProhibition] += len(prohibitionRe.FindAllString(ln.text, -1))
	if numberedRe.MatchString(ln.text) {
		f.counts[RuleFloorNumbered]++
	}
}

// collect records items under rule with the first line each appears on.
func (f facts) collect(rule string, num int, items []string) {
	f.keep(rule, items)
	for _, item := range items {
		if _, seen := f.items[rule][item]; !seen {
			f.items[rule][item] = num
		}
	}
}

// keep records items a reader still finds under a rule that tracks them (facts.kept).
func (f facts) keep(rule string, items []string) {
	kept, ok := f.kept[rule]
	if !ok {
		return
	}
	for _, item := range items {
		kept[item] = true
	}
}

// holds reports whether the text still carries item: for a number or code token anywhere a
// reader finds it outside a comment, for every other rule where that rule reads it.
func (f facts) holds(rule, item string) bool {
	if kept, ok := f.kept[rule]; ok {
		return kept[item]
	}
	_, ok := f.items[rule][item]
	return ok
}

// collectFenced records the facts of a fenced line and returns the state the next line
// starts from. A command keeps its line verbatim (F2). A line of a source or data fence, and
// a line of a terminal session that is not a command, the output it printed, keep their code
// tokens (F10) and numbers (F9) with their comments stripped (exampleLine). Every fenced
// line outside its comments keeps the numbers and code tokens it shows.
func (f facts) collectFenced(ln line, state fenceState) fenceState {
	if ln.edge {
		return fenceState{}
	}
	code, open := util.StripComments(ln.code, commentSyntax(ln), state.comment)
	f.keep(RuleFloorNumber, numbersIn(code))
	f.keep(RuleFloorCodeWord, codeTokenRe.FindAllString(code, -1))
	if command, ok := fencedCommand(ln, state.continued); ok {
		f.collect(RuleFloorCommand, ln.num, []string{command})
		return fenceState{continued: strings.HasSuffix(command, `\`), comment: open}
	}
	if exampleLine(ln) {
		f.collect(RuleFloorCodeWord, ln.num, codeTokenRe.FindAllString(code, -1))
		f.collect(RuleFloorNumber, ln.num, numbersIn(code))
	}
	return fenceState{comment: open}
}

// exampleLine reports a fenced line that is not a command and still holds facts: a line of
// a source or data fence, whose language is neither a shell (util.MarkdownShellFence) nor a
// Mermaid diagram, whose labels are prose; or a line of a terminal session, the output its
// commands printed, such as a test count or a timing.
func exampleLine(ln line) bool {
	if ln.shell == util.ShellSession {
		return true
	}
	return ln.shell == util.ShellNone && ln.lang != "" && ln.lang != "mermaid"
}

// fencedCommand returns the command a fenced line carries. A shell fence carries commands
// (util.MarkdownShellFence, the table the documentation reference check reads): every line
// that is not its delimiter, blank or a comment, in a session only a line after the "$ "
// prompt, and every line that continues a command ending in a backslash. The prompt is not
// part of the command, so a session rewritten as a script keeps its facts. A fence without a
// language reads as a script: nothing says its lines are not commands, so the floor stays
// strict there, where the documentation reference check reads nothing. A line of any other
// fence, Go or JSON source, Mermaid or plain text, is an example rather than a command:
// collectFenced holds its code tokens and numbers instead, so correcting a comment in it
// loses no fact while changing its code does (#322). A fence inside a blockquote compares
// without its markers (#356).
func fencedCommand(ln line, continued bool) (string, bool) {
	shell := ln.shell
	if ln.lang == "" {
		shell = util.ShellScript
	}
	trimmed := strings.TrimSpace(ln.code)
	if ln.edge || shell == util.ShellNone || trimmed == "" {
		return "", false
	}
	if continued {
		return trimmed, true
	}
	return util.MarkdownShellCommand(shell, trimmed)
}

// numbersOf returns the number facts of a line outside fenced code. Code spans, link
// targets, URLs, HTML markers and ids are masked first, since F1 and F3 to F5 already hold
// them, and so are the labels of Markdown syntax (orderedMarkerRe, headingNumberRe,
// refLabelRe) and HTML entities, which are no facts of the text. ANSI escape sequences
// (CSI, OSC and two-byte escapes) are masked before URLs so their parameters are no
// numbers and their URIs do not misparse (#713).
func numbersOf(ln line) []string {
	clean := ln
	clean.text = maskANSI(ln.text)
	masked := markerRe.ReplaceAllString(proseOf(clean), " ")
	masked = runtimeHTMLEntityRe.ReplaceAllString(idRe.ReplaceAllString(masked, " "), " ")
	masked = refLabelRe.ReplaceAllString(headingNumberRe.ReplaceAllString(masked, "$1 "), " ")
	return numbersIn(orderedMarkerRe.ReplaceAllString(masked, " "))
}

// maskANSI replaces terminated ANSI escape sequences with spaces of the same byte length,
// keeping line byte offsets and code span bounds intact while removing escape parameters.
// Unterminated escapes stay text.
func maskANSI(text string) string {
	return ansiRe.ReplaceAllStringFunc(text, func(m string) string {
		return strings.Repeat(" ", len(m))
	})
}

// numbersIn returns the number facts of text: every word that holds a digit (numberTokenRe),
// read by numberFact. Escape sequences (CSI, OSC and two-byte escapes) are ignored before digits
// are collected, through the one ANSI pattern Compress uses (ansiRe, #713). An unterminated
// escape stays text.
func numbersIn(text string) []string {
	text = ansiRe.ReplaceAllString(text, "")
	matches := numberTokenRe.FindAllStringSubmatch(text, -1)
	numbers := make([]string, 0, len(matches))
	for _, match := range matches {
		if digitRe.MatchString(match[1]) {
			numbers = append(numbers, numberFact(match[1]))
		}
	}
	return numbers
}

// numberFact returns the fact a word that holds a digit carries. A number followed by a unit
// and no further digit carries the number alone, so "24 h" equals "24h" and "64-bit" carries
// 64. Every other such word is one fact, whole: v8.6.0 turning into v8.7.0, p99.94 into p99.9
// or -5 into 5 fails, and the digits of utf-8 or x86-64 are no numbers of their own.
func numberFact(word string) string {
	literal := numericLiteralRe.FindString(word)
	if literal != "" && !digitRe.MatchString(word[len(literal):]) {
		return literal
	}
	return word
}
