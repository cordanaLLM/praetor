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
	// RuleFloorNumber fires when a numeric literal of the prose, a table cell, another line
	// outside fenced code or the code of a source or data fence is gone: the Caveman skill
	// copies numbers verbatim like code and ids (#363).
	RuleFloorNumber = "F9 number-lost"
	// RuleFloorCodeToken fires when an identifier, key or word of the code of a source or
	// data fence is gone. Its comments are prose and may change; its code may not (#322).
	RuleFloorCodeToken = "F10 code-token-lost"
)

var (
	// idRe matches rule and record ids such as HISS-17, ADR-0010, BUG-012 and Q-3.
	idRe          = regexp.MustCompile(`\b[A-Z][A-Z0-9]*-\d+\b`)
	linkCaptureRe = regexp.MustCompile(`\]\(([^)\s]+)\)`)
	markerRe      = regexp.MustCompile(`<!--.*?-->`)
	mustRe        = regexp.MustCompile(`\b(?:MUST|SHALL|REQUIRED)\b`)
	prohibitionRe = regexp.MustCompile(`(?i)\b(?:never|do not|don't|must not|no)\b`)
	numberedRe    = regexp.MustCompile(`^\s*\d+\.\s+\*\*`)
	// numberRe matches a numeric literal with its decimal or digit groups: 557.3, 1819,
	// 0.0234, 1,024. No letter, digit or underscore runs into it, and no "." or "," that one
	// of them runs into, so a word holds no number, dotted or not: p99, sha256, p99.94,
	// v8.6.0 and go1.27. A unit after it is not part of the fact, so "24 h" and "24h"
	// compare equal.
	numberRe = regexp.MustCompile(`(?:^|[^\pL\pN_.,]|(?:^|[^\pL\pN_])[.,])(\d+(?:[.,]\d+)*)`)
	// codeTokenRe matches an identifier, key or word of code.
	codeTokenRe = regexp.MustCompile(`[\pL_][\pL\pN_]*`)
	// orderedMarkerRe matches the marker of an ordered list item: renumbering a list loses
	// no fact, and F8 already counts numbered rules.
	orderedMarkerRe = regexp.MustCompile(`^\s*\d+[.)](?:\s|$)`)
)

// setRules are facts that must survive verbatim; countRules are directives whose number
// must not fall. Both lists fix the order in which findings are produced.
var (
	setRules = []string{RuleFloorCodeSpan, RuleFloorCommand, RuleFloorID, RuleFloorLink, RuleFloorMarker,
		RuleFloorNumber, RuleFloorCodeToken}
	countRules = []string{RuleFloorMust, RuleFloorProhibition, RuleFloorNumbered}
)

// shellComments are the line comment markers of a shell fence and of a fence without a
// language, which reads as a script.
var shellComments = []string{"#"}

// lineComments maps the languages of source and data fences to the markers that open a
// line comment in them (util.StripLineComment), so a corrected comment loses no fact
// (#322). A language missing here, such as json or text, has no comments: all of a line is
// code.
var lineComments = map[string][]string{
	"go": {"//"}, "c": {"//"}, "cpp": {"//"}, "csharp": {"//"}, "cs": {"//"}, "java": {"//"},
	"javascript": {"//"}, "js": {"//"}, "jsx": {"//"}, "typescript": {"//"}, "ts": {"//"},
	"tsx": {"//"}, "rust": {"//"}, "rs": {"//"}, "kotlin": {"//"}, "swift": {"//"},
	"scala": {"//"}, "groovy": {"//"}, "dart": {"//"}, "proto": {"//"}, "jsonc": {"//"},
	"json5": {"//"}, "yaml": {"#"}, "yml": {"#"}, "toml": {"#"}, "python": {"#"}, "py": {"#"},
	"ruby": {"#"}, "rb": {"#"}, "perl": {"#"}, "r": {"#"}, "dockerfile": {"#"}, "make": {"#"},
	"makefile": {"#"}, "cmake": {"#"}, "nix": {"#"}, "elixir": {"#"}, "conf": {"#"},
	"gitignore": {"#"}, "hcl": {"#", "//"}, "terraform": {"#", "//"}, "tf": {"#", "//"},
	"php": {"//", "#"}, "ini": {";", "#"}, "sql": {"--"}, "lua": {"--"}, "haskell": {"--"},
	"hs": {"--"}, "mermaid": {"%%"},
}

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
// data fence, a MUST-type directive, a prohibition (never, do not, no) or a numbered rule.
// Findings of lost items sit on their line in before; count findings sit on line 0. Moving
// a fact is fine; dropping it is not.
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
		kept:   map[string]map[string]bool{RuleFloorNumber: {}, RuleFloorCodeToken: {}},
		counts: map[string]int{},
	}
	for _, rule := range setRules {
		f.items[rule] = map[string]int{}
	}
	lines, _ := scan(text)
	for _, span := range codeSpanTexts(lines) {
		f.collect(RuleFloorCodeSpan, span.num, []string{span.text})
		f.keep(RuleFloorCodeToken, codeTokenRe.FindAllString(span.text, -1))
	}
	continued := false
	for _, ln := range lines {
		f.collect(RuleFloorID, ln.num, idRe.FindAllString(ln.text, -1))
		if ln.kind == kindCode {
			continued = f.collectFenced(ln, continued)
			continue
		}
		continued = false
		f.collectLine(ln)
	}
	return f
}

// collectLine records the facts of one line outside fenced code apart from its code spans
// and ids, which extractFacts reads. Every number the line shows outside an HTML marker and
// an ordered list marker, code spans included, stays kept.
func (f facts) collectLine(ln line) {
	f.collect(RuleFloorMarker, ln.num, markerRe.FindAllString(ln.text, -1))
	for _, match := range linkCaptureRe.FindAllStringSubmatch(ln.text, -1) {
		f.collect(RuleFloorLink, ln.num, match[1:])
	}
	f.collect(RuleFloorNumber, ln.num, numbersOf(ln))
	f.keep(RuleFloorNumber, numbersIn(orderedMarkerRe.ReplaceAllString(markerRe.ReplaceAllString(ln.text, " "), " ")))
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

// collectFenced records the facts of a fenced line and reports whether the next line
// continues a command. A command keeps its line verbatim (F2); a line of a source or data
// fence keeps its code tokens (F10) and numbers (F9) with its comments stripped. Every
// fenced line outside its comments keeps the numbers and code tokens it shows.
func (f facts) collectFenced(ln line, continued bool) bool {
	if ln.edge {
		return false
	}
	code := fencedCode(ln)
	f.keep(RuleFloorNumber, numbersIn(code))
	f.keep(RuleFloorCodeToken, codeTokenRe.FindAllString(code, -1))
	if command, ok := fencedCommand(ln, continued); ok {
		f.collect(RuleFloorCommand, ln.num, []string{command})
		return strings.HasSuffix(command, `\`)
	}
	if exampleFence(ln) {
		f.collect(RuleFloorCodeToken, ln.num, codeTokenRe.FindAllString(code, -1))
		f.collect(RuleFloorNumber, ln.num, numbersIn(code))
	}
	return false
}

// exampleFence reports a line of a source or data fence: a fence with a language that is
// neither a shell (util.MarkdownShellFence) nor a Mermaid diagram, whose labels are prose.
func exampleFence(ln line) bool {
	return ln.shell == util.ShellNone && ln.lang != "" && ln.lang != "mermaid"
}

// fencedCode returns a fenced line without its blockquote markers and its line comment,
// read by the markers of its fence language (lineComments).
func fencedCode(ln line) string {
	markers := lineComments[ln.lang]
	if ln.shell != util.ShellNone || ln.lang == "" {
		markers = shellComments
	}
	code := ln.code
	for _, marker := range markers {
		code = util.StripLineComment(code, marker)
	}
	return code
}

// fencedCommand returns the command a fenced line carries. A shell fence carries commands
// (util.MarkdownShellFence, the table the documentation reference check reads): every line
// that is not its delimiter, blank or a "#" comment, in a session only a line after the
// "$ " prompt, and every line that continues a command ending in a backslash. The prompt is
// not part of the command, so a session rewritten as a script keeps its facts. A fence
// without a language reads as a script: nothing says its lines are not commands, so the
// floor stays strict there. A line of any other fence, Go or JSON source, Mermaid or plain
// text, is an example rather than a command: collectFenced holds its code tokens and
// numbers instead, so correcting a comment in it loses no fact while changing its code
// does (#322). A fence inside a blockquote compares without its markers (#356).
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

// numbersOf returns the numeric literals of a line outside fenced code. Code spans, link
// targets, URLs, HTML markers and ids are masked first, since F1 and F3 to F5 already hold
// them, and an ordered list marker is not a fact.
func numbersOf(ln line) []string {
	masked := idRe.ReplaceAllString(markerRe.ReplaceAllString(proseOf(ln), " "), " ")
	return numbersIn(orderedMarkerRe.ReplaceAllString(masked, " "))
}

// numbersIn returns the numeric literals of text (numberRe).
func numbersIn(text string) []string {
	matches := numberRe.FindAllStringSubmatch(text, -1)
	numbers := make([]string, 0, len(matches))
	for _, match := range matches {
		numbers = append(numbers, match[1])
	}
	return numbers
}
