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
	// RuleFloorNumber fires when a numeric literal of the prose, a table cell or another
	// line outside fenced code is gone: the Caveman skill copies numbers verbatim like code
	// and ids (#363).
	RuleFloorNumber = "F9 number-lost"
)

var (
	// idRe matches rule and record ids such as HISS-17, ADR-0010, BUG-012 and Q-3.
	idRe          = regexp.MustCompile(`\b[A-Z][A-Z0-9]*-\d+\b`)
	linkCaptureRe = regexp.MustCompile(`\]\(([^)\s]+)\)`)
	markerRe      = regexp.MustCompile(`<!--.*?-->`)
	mustRe        = regexp.MustCompile(`\b(?:MUST|SHALL|REQUIRED)\b`)
	prohibitionRe = regexp.MustCompile(`(?i)\b(?:never|do not|don't|must not|no)\b`)
	numberedRe    = regexp.MustCompile(`^\s*\d+\.\s+\*\*`)
	// numberRe matches a numeric literal that no letter, digit or underscore runs into, with
	// its decimal or digit groups: 557.3, 1819, 0.0234, 1,024. A unit after it is not part of
	// the fact, so "24 h" and "24h" compare equal, while a token such as p99 or sha256 is a
	// word rather than a number.
	numberRe = regexp.MustCompile(`(?:^|[^\pL\pN_])(\d+(?:[.,]\d+)*)`)
	// orderedMarkerRe matches the marker of an ordered list item: renumbering a list loses
	// no fact, and F8 already counts numbered rules.
	orderedMarkerRe = regexp.MustCompile(`^\s*\d+[.)](?:\s|$)`)
)

// setRules are facts that must survive verbatim; countRules are directives whose number
// must not fall. Both lists fix the order in which findings are produced.
var (
	setRules   = []string{RuleFloorCodeSpan, RuleFloorCommand, RuleFloorID, RuleFloorLink, RuleFloorMarker, RuleFloorNumber}
	countRules = []string{RuleFloorMust, RuleFloorProhibition, RuleFloorNumbered}
)

// facts is what a reader of a text must still find after a rewrite: verbatim items with the
// line they first appear on, and directive counts.
type facts struct {
	items  map[string]map[string]int
	counts map[string]int
}

// Floor fails when after lost something before carried: a code span, a fenced command, an
// id such as HISS-17, a link target, an HTML marker, a number, a MUST-type directive, a
// prohibition (never, do not, no) or a numbered rule. Findings of lost items sit on their
// line in before; count findings sit on line 0. Moving a fact is fine; dropping it is not.
func Floor(before, after string) Report {
	had, has := extractFacts(before), extractFacts(after)
	var found findings
	for _, rule := range setRules {
		for item, num := range had.items[rule] {
			if _, ok := has.items[rule][item]; !ok {
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
	f := facts{items: map[string]map[string]int{}, counts: map[string]int{}}
	for _, rule := range setRules {
		f.items[rule] = map[string]int{}
	}
	lines, _ := scan(text)
	for _, span := range codeSpanTexts(lines) {
		f.collect(RuleFloorCodeSpan, span.num, []string{span.text})
	}
	continued := false
	for _, ln := range lines {
		f.collect(RuleFloorID, ln.num, idRe.FindAllString(ln.text, -1))
		if ln.kind == kindCode {
			continued = f.collectCommand(ln, continued)
			continue
		}
		continued = false
		f.collectLine(ln)
	}
	return f
}

// collectLine records the facts of one line outside fenced code apart from its code spans
// and ids, which extractFacts reads.
func (f facts) collectLine(ln line) {
	f.collect(RuleFloorMarker, ln.num, markerRe.FindAllString(ln.text, -1))
	for _, match := range linkCaptureRe.FindAllStringSubmatch(ln.text, -1) {
		f.collect(RuleFloorLink, ln.num, match[1:])
	}
	f.collect(RuleFloorNumber, ln.num, numbersOf(ln))
	f.counts[RuleFloorMust] += len(mustRe.FindAllString(ln.text, -1))
	f.counts[RuleFloorProhibition] += len(prohibitionRe.FindAllString(ln.text, -1))
	if numberedRe.MatchString(ln.text) {
		f.counts[RuleFloorNumbered]++
	}
}

// collect records items under rule with the first line each appears on.
func (f facts) collect(rule string, num int, items []string) {
	for _, item := range items {
		if _, seen := f.items[rule][item]; !seen {
			f.items[rule][item] = num
		}
	}
}

// collectCommand records the command a fenced line carries and reports whether the next
// line continues it.
func (f facts) collectCommand(ln line, continued bool) bool {
	command, ok := fencedCommand(ln, continued)
	if ok {
		f.collect(RuleFloorCommand, ln.num, []string{command})
	}
	return ok && strings.HasSuffix(command, `\`)
}

// fencedCommand returns the command a fenced line carries. A shell fence carries commands
// (util.MarkdownShellFence, the table the documentation reference check reads): every line
// that is not its delimiter, blank or a "#" comment, in a session only a line after the
// "$ " prompt, and every line that continues a command ending in a backslash. The prompt is
// not part of the command, so a session rewritten as a script keeps its facts. A fence
// without a language reads as a script: nothing says its lines are not commands, so the
// floor stays strict there. A line of any other fence, Go or JSON source, Mermaid or plain
// text, is an example rather than a command, so correcting a comment in it loses no fact
// (#322). A fence inside a blockquote compares without its markers (#356).
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
	masked = orderedMarkerRe.ReplaceAllString(masked, " ")
	matches := numberRe.FindAllStringSubmatch(masked, -1)
	numbers := make([]string, 0, len(matches))
	for _, match := range matches {
		numbers = append(numbers, match[1])
	}
	return numbers
}
