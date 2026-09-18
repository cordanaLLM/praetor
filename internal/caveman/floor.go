package caveman

import (
	"fmt"
	"regexp"
	"strings"
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
)

var (
	// idRe matches rule and record ids such as HISS-17, ADR-0010, BUG-012 and Q-3.
	idRe          = regexp.MustCompile(`\b[A-Z][A-Z0-9]*-\d+\b`)
	linkCaptureRe = regexp.MustCompile(`\]\(([^)\s]+)\)`)
	markerRe      = regexp.MustCompile(`<!--.*?-->`)
	mustRe        = regexp.MustCompile(`\b(?:MUST|SHALL|REQUIRED)\b`)
	prohibitionRe = regexp.MustCompile(`(?i)\b(?:never|do not|don't|must not|no)\b`)
	numberedRe    = regexp.MustCompile(`^\s*\d+\.\s+\*\*`)
)

// setRules are facts that must survive verbatim; countRules are directives whose number
// must not fall. Both lists fix the order in which findings are produced.
var (
	setRules   = []string{RuleFloorCodeSpan, RuleFloorCommand, RuleFloorID, RuleFloorLink, RuleFloorMarker}
	countRules = []string{RuleFloorMust, RuleFloorProhibition, RuleFloorNumbered}
)

// facts is what a reader of a text must still find after a rewrite: verbatim items with the
// line they first appear on, and directive counts.
type facts struct {
	items  map[string]map[string]int
	counts map[string]int
}

// Floor fails when after lost something before carried: a code span, a fenced command, an
// id such as HISS-17, a link target, an HTML marker, a MUST-type directive, a prohibition
// (never, do not, no) or a numbered rule. Findings of lost items sit on their line in before;
// count findings sit on line 0. Moving a fact is fine; dropping it is not.
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
	for _, ln := range lines {
		f.collect(RuleFloorID, ln.num, idRe.FindAllString(ln.text, -1))
		if ln.kind == kindCode {
			if isCommand(ln) {
				f.collect(RuleFloorCommand, ln.num, []string{strings.TrimSpace(ln.text)})
			}
			continue
		}
		f.collect(RuleFloorCodeSpan, ln.num, inlineCodeRe.FindAllString(ln.text, -1))
		f.collect(RuleFloorMarker, ln.num, markerRe.FindAllString(ln.text, -1))
		for _, match := range linkCaptureRe.FindAllStringSubmatch(ln.text, -1) {
			f.collect(RuleFloorLink, ln.num, match[1:])
		}
		f.counts[RuleFloorMust] += len(mustRe.FindAllString(ln.text, -1))
		f.counts[RuleFloorProhibition] += len(prohibitionRe.FindAllString(ln.text, -1))
		if numberedRe.MatchString(ln.text) {
			f.counts[RuleFloorNumbered]++
		}
	}
	return f
}

// collect records items under rule with the first line each appears on.
func (f facts) collect(rule string, num int, items []string) {
	for _, item := range items {
		if _, seen := f.items[rule][item]; !seen {
			f.items[rule][item] = num
		}
	}
}

// isCommand reports a command line inside fenced code: not a fence delimiter, not a
// diagram, not blank and not a shell comment.
func isCommand(ln line) bool {
	trimmed := strings.TrimSpace(ln.text)
	return !ln.edge && ln.lang != "mermaid" && trimmed != "" && !strings.HasPrefix(trimmed, "#")
}
