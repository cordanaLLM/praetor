package baseline

import (
	"fmt"
	"slices"
	"strings"

	"github.com/cordanaLLM/praetor/internal/buildid"
)

// Attribution says why the baseline does not record one new violation (#599).
type Attribution string

const (
	// AttributionNone means nothing traced the violation: no attribution ran, or it could not
	// read the baseline's commits. Summary describes it as not in the baseline,
	// never as introduced, because a check added after the baseline was recorded reports
	// unchanged code exactly this way.
	AttributionNone Attribution = ""
	// AttributionIntroduced means the current checks do not report the violation in the files as
	// any of the baseline's commits holds them: the code changed after the baseline was recorded.
	AttributionIntroduced Attribution = "introduced"
	// AttributionCheckChanged means the current checks report the violation at one of the
	// baseline's commits too, yet the baseline does not record it: the code is unchanged, and a check or limit
	// added or changed after the baseline was recorded reports it.
	AttributionCheckChanged Attribution = "check-changed"
	// AttributionMoved means the baseline records the same rule, file, symbol and message under
	// a key no current violation carries, and the debt did not change (#29). For an entry of the
	// line-keyed form, lines above it were added or removed since the baseline was recorded; for
	// an anchored one, the function holding it was renamed or its anchored line edited (key.go).
	// The baseline and the scan tell it without a commit, and a plain re-record, without
	// --allow-increase, clears it.
	AttributionMoved Attribution = "moved"
)

// Commands a rejection names: the read-only listings and the deliberate re-record.
const (
	verifyLister = "'praetorctl baseline --verify --all-violations'"
	auditLister  = "'praetorctl audit --all-violations'"
	recordRemedy = "'praetorctl baseline --record --allow-increase --reason=<why>'"
	reRecord     = "'praetorctl baseline --record'"
	// debtDeltaFlag judges touched files by debt delta (RatchetOptions.DebtDelta).
	debtDeltaFlag = "'praetorctl audit --touched-debt-delta-reason=<why>'"
)

// violationClass is one listed class of a rejection: the tag each line carries, how its
// hidden-remainder marker counts it, and the read-only command that lists every member. A
// non-empty entry of tags replaces tag for the item at its index.
type violationClass struct {
	items  []Infraction
	tags   []string
	tag    string
	one    string
	many   string
	lister string
}

// tagOf is the tag the class's item i carries.
func (c violationClass) tagOf(i int) string {
	if i < len(c.tags) && c.tags[i] != "" {
		return c.tags[i]
	}
	return c.tag
}

// lines renders the class as [rule] file:line - message (tag) lines. A positive limit bounds
// the listing (HISS-02) and a cut listing ends with a marker naming how many lines it hid and
// the command that lists them; zero or less lists every member.
func (c violationClass) lines(limit int) []string {
	shown := len(c.items)
	if limit > 0 && shown > limit {
		shown = limit
	}
	out := make([]string, 0, shown+1)
	for i := 0; i < shown; i++ {
		v := c.items[i]
		out = append(out, fmt.Sprintf("  [%s] %s:%d - %s (%s)", v.RuleID, v.FilePath, v.LineNumber, v.Message, c.tagOf(i)))
	}
	hidden := len(c.items) - shown
	if hidden == 0 {
		return out
	}
	noun := c.many
	if hidden == 1 {
		noun = c.one
	}
	return append(out, fmt.Sprintf("  ... and %d more %s not shown; %s lists every one", hidden, noun, c.lister))
}

// rejectionPartition splits NewViolations by attribution and TouchedCleanViolations by whether
// the baseline records them. Without an attribution for every new entry, all of them are
// unattributed; without a baselined mark for every touched entry, touchedKnown is false and the
// touched entries stay one class.
type rejectionPartition struct {
	introduced, changed, moved, unknown []Infraction
	// movedTags holds, per moved entry, the tag naming the line the baseline records it at.
	movedTags []string
	// touchedNew and touchedBaselined split the touched-file violations once touchedKnown (#348).
	touchedNew, touchedBaselined []Infraction
	touchedKnown                 bool
}

// partition splits the rejection's violations for Summary.
func (r *RatchetResult) partition() rejectionPartition {
	p := r.partitionNew()
	if len(r.TouchedBaselined) != len(r.TouchedCleanViolations) {
		return p
	}
	p.touchedKnown = true
	for i := 0; i < len(r.TouchedCleanViolations); i++ {
		if r.TouchedBaselined[i] {
			p.touchedBaselined = append(p.touchedBaselined, r.TouchedCleanViolations[i])
		} else {
			p.touchedNew = append(p.touchedNew, r.TouchedCleanViolations[i])
		}
	}
	return p
}

func (r *RatchetResult) partitionNew() rejectionPartition {
	var p rejectionPartition
	if len(r.Attribution) != len(r.NewViolations) {
		p.unknown = r.NewViolations
		return p
	}
	for i := 0; i < len(r.NewViolations); i++ {
		switch r.Attribution[i] {
		case AttributionIntroduced:
			p.introduced = append(p.introduced, r.NewViolations[i])
		case AttributionCheckChanged:
			p.changed = append(p.changed, r.NewViolations[i])
		case AttributionMoved:
			p.moved = append(p.moved, r.NewViolations[i])
			p.movedTags = append(p.movedTags, movedTag(r.recordedLine(i)))
		default:
			p.unknown = append(p.unknown, r.NewViolations[i])
		}
	}
	return p
}

// recordedLine is the line the baseline records new violation i at, or zero when unknown.
func (r *RatchetResult) recordedLine(i int) int {
	if i < len(r.RecordedLine) {
		return r.RecordedLine[i]
	}
	return 0
}

// movedTag names the line the baseline records a moved violation at; a baseline entry without a
// line number gets the generic tag.
func movedTag(line int) string {
	if line > 0 {
		return fmt.Sprintf("recorded in the baseline at line %d", line)
	}
	return "recorded in the baseline at another line"
}

// classes lists the rejection's classes in the order Summary prints them.
func (r *RatchetResult) classes(p rejectionPartition) []violationClass {
	return append([]violationClass{
		{items: p.introduced, tag: "new", one: "new violation", many: "new violations", lister: verifyLister},
		{items: p.changed, tag: "check added or changed since the baseline",
			one: "violation from a changed check", many: "violations from changed checks", lister: verifyLister},
		{items: p.moved, tags: p.movedTags, tag: movedTag(0),
			one: "violation the baseline records at another line", many: "violations the baseline records at other lines", lister: verifyLister},
		{items: p.unknown, tag: "not in the baseline",
			one: "unbaselined violation", many: "unbaselined violations", lister: verifyLister},
	}, r.touchedClasses(p)...)
}

// touchedClasses lists the touched-file violations: once the evaluator marked each, the ones the
// baseline does not record first and the baselined ones after them, each class bounded and
// counted on its own (#348); otherwise one class.
func (r *RatchetResult) touchedClasses(p rejectionPartition) []violationClass {
	if !p.touchedKnown {
		return []violationClass{{items: r.TouchedCleanViolations, tag: "touched file must be clean",
			one: "touched-file violation", many: "touched-file violations", lister: auditLister}}
	}
	return []violationClass{
		{items: p.touchedNew, tag: "touched file must be clean, not in the baseline",
			one: "touched-file violation not in the baseline", many: "touched-file violations not in the baseline", lister: auditLister},
		{items: p.touchedBaselined, tag: "touched file must be clean, baselined",
			one: "baselined touched-file violation", many: "baselined touched-file violations", lister: auditLister},
	}
}

// render is Summary with limit lines per class; zero or less lists every violation.
func (r *RatchetResult) render(limit int) string {
	if r == nil {
		return "no ratchet result"
	}
	if r.CountRegressed {
		return fmt.Sprintf("HISS invariant violations introduced: total infractions rose from %d to %d (no new fingerprints)", r.PreviousCount, r.CurrentCount)
	}
	p := r.partition()
	var msgs []string
	for _, class := range r.classes(p) {
		msgs = append(msgs, class.lines(limit)...)
	}
	msgs = append(msgs, r.explanations(p)...)
	if r.CurrentCount > r.PreviousCount {
		msgs = append(msgs, fmt.Sprintf("  total infractions rose from %d to %d", r.PreviousCount, r.CurrentCount))
	}
	return r.header(p) + "\n" + strings.Join(msgs, "\n")
}

// header names the counts. Only a rejection whose every unbaselined violation is traced to a
// code change is called introduced, and one whose every unbaselined violation only moved says
// the baseline records them at other lines; any other says the baseline does not record them.
func (r *RatchetResult) header(p rejectionPartition) string {
	touched := r.touchedFigure(p)
	untraced := len(p.changed) + len(p.unknown)
	switch {
	case untraced == 0 && len(p.moved) == 0:
		return fmt.Sprintf("HISS invariant violations introduced (%d total infractions, %d new unbaselined, %s):",
			r.CurrentCount, len(p.introduced), touched)
	case untraced == 0 && len(p.introduced) == 0:
		return fmt.Sprintf("HISS invariant violations the baseline records at other lines (%d total infractions, %d moved, %s):",
			r.CurrentCount, len(p.moved), touched)
	}
	return fmt.Sprintf("HISS invariant violations the baseline does not record (%d total infractions, %d unbaselined%s, %s):",
		r.CurrentCount, len(r.NewViolations), r.breakdown(p), touched)
}

// touchedFigure names how many violations sit in touched files and, once the evaluator marked
// each, how many of them the baseline does not record and how many it does (#348): the touched
// count alone read as that many new findings when most of them were recorded debt.
func (r *RatchetResult) touchedFigure(p rejectionPartition) string {
	n := len(r.TouchedCleanViolations)
	if !p.touchedKnown || n == 0 {
		return fmt.Sprintf("%d in touched files", n)
	}
	return fmt.Sprintf("%d in touched files (%d not in the baseline, %d baselined)", n, len(p.touchedNew), len(p.touchedBaselined))
}

// breakdown splits the unbaselined count by attribution, once an attribution ran.
func (r *RatchetResult) breakdown(p rejectionPartition) string {
	if r.Attribution == nil {
		return ""
	}
	var parts []string
	if n := len(p.introduced); n > 0 {
		parts = append(parts, fmt.Sprintf("%d introduced", n))
	}
	if n := len(p.changed); n > 0 {
		parts = append(parts, fmt.Sprintf("%d from checks added or changed since the baseline", n))
	}
	if n := len(p.moved); n > 0 {
		parts = append(parts, fmt.Sprintf("%d recorded at another line", n))
	}
	if n := len(p.unknown); n > 0 {
		parts = append(parts, fmt.Sprintf("%d unattributed", n))
	}
	return " (" + strings.Join(parts, ", ") + ")"
}

// explanations says why the violations that are not traced to a code change fail, and what
// records them deliberately, then why the baselined findings in touched files fail too.
func (r *RatchetResult) explanations(p rejectionPartition) []string {
	var out []string
	if n := len(p.changed); n > 0 {
		out = append(out, fmt.Sprintf("  %d of them sit in code the current checks flag at %s, where the baseline was recorded or committed, "+
			"and the baseline does not record them: a check or limit added or changed since the baseline was recorded reports them, "+
			"not a code change; fix them or record them with %s", n, shortCommits(r.AttributionCommits), recordRemedy))
	}
	if n := len(p.moved); n > 0 {
		out = append(out, fmt.Sprintf("  %d of them the baseline records under another key of the same file: an entry keyed by its line moved, "+
			"or the function or line an entry is anchored to was renamed or edited, "+
			"and the debt did not change; re-record the baseline with %s, which needs no --allow-increase for them", n, reRecord))
	}
	if n := len(p.unknown); n > 0 {
		reason := "no commit was compared"
		if r.AttributionNote != "" {
			reason = r.AttributionNote
		}
		out = append(out, fmt.Sprintf("  %d of them were not traced to a code change (%s); a check added after the baseline was recorded "+
			"reports unchanged code this way too; fix them or record them with %s", n, reason, recordRemedy))
	}
	if n := len(p.touchedBaselined); n > 0 {
		out = append(out, r.touchedExplanation(n))
	}
	return out
}

// touchedExplanation says why n baselined findings in touched files fail anyway, under the rule
// that judged their files, and what clears them (#348).
func (r *RatchetResult) touchedExplanation(n int) string {
	if r.DebtDelta {
		return fmt.Sprintf("  %d of the touched-file violations are baselined: they fail because a rule's count in their file rose above "+
			"the baseline's, and pass again once no rule's count in the file does", n)
	}
	return fmt.Sprintf("  %d of the touched-file violations are baselined: touching a file revokes its baseline exemptions, so they fail too; "+
		"fix them, or judge a provably mechanical change by debt delta with %s", n, debtDeltaFlag)
}

// StaleNotice says how many baseline entries no current violation accounts for and how to
// tighten the baseline (#349), or "" when none is stale. A stale entry is debt a cleanup removed
// without a re-record: the ratchet still passes, and the entry is room a new finding can take.
// Callers decide whether a count fails; the ratchet verdict never does.
func (r *RatchetResult) StaleNotice() string {
	if r == nil || r.Stale <= 0 {
		return ""
	}
	entries := "baseline entries match"
	if r.Stale == 1 {
		entries = "baseline entry matches"
	}
	return fmt.Sprintf("%d %s nothing in the tree: per file and rule the baseline records more infractions than the scan found, "+
		"and each stale entry is room for a new finding; tighten the baseline with %s", r.Stale, entries, reRecord)
}

// shortCommits abbreviates the compared commits for a rejection line, joined by "or": a finding
// at any of them counts.
func shortCommits(commits []string) string {
	short := make([]string, 0, len(commits))
	for i := 0; i < len(commits); i++ {
		short = append(short, buildid.Short(commits[i]))
	}
	return strings.Join(short, " or ")
}

// CommitFindings is what the current checks report, under the ratchet's scan policy, in the new
// violations' files as one of the baseline's commits holds them.
type CommitFindings struct {
	Commit   string
	Findings []Infraction
}

// Attribute classifies each new violation against compared (#599): the findings at the
// baseline's commits, the one the baseline b was recorded at and the one that last committed the
// baseline file. A baseline recorded on a work tree and committed with the code it scanned holds
// that code only at the second; a squash merge leaves only the second in a fresh clone. current
// is the scan the ratchet judged.
//
// A violation the baseline records at another line (a moved fingerprint) was known to the
// recorder: AttributionMoved, with that line in RecordedLine. One the current checks also report
// at any compared commit sits in code that has not changed since, so a recorder that had the
// check would have recorded it: AttributionCheckChanged. Anything else is AttributionIntroduced.
// Findings match on rule, file, symbol and message, never on the line, and each finding explains
// at most one violation; a finding at several commits counts once, as often as the commit
// holding it most often reports it. Without a compared commit nothing is attributed beyond the
// moved violations.
func (r *RatchetResult) Attribute(b *Baseline, current []Infraction, compared []CommitFindings) {
	if r == nil || b == nil {
		return
	}
	r.attribute(b, current, unionFindings(compared))
	r.AttributionCommits = make([]string, 0, len(compared))
	for i := 0; i < len(compared); i++ {
		r.AttributionCommits = append(r.AttributionCommits, compared[i].Commit)
	}
	r.AttributionNote = ""
}

// unionFindings counts the findings reported at any compared commit by their line-independent
// identity, each as often as the commit holding it most often reports it. No compared commit is
// nil: nothing was compared.
func unionFindings(compared []CommitFindings) map[string]int {
	if len(compared) == 0 {
		return nil
	}
	union := make(map[string]int)
	for i := 0; i < len(compared); i++ {
		for key, n := range countBy(compared[i].Findings, findingKey) {
			union[key] = max(union[key], n)
		}
	}
	return union
}

// AttributeUntraced records note, why no commit could be compared, and still tags the new
// violations the baseline records at another line: the baseline b and current, the scan the
// ratchet judged, tell those without a commit. Every other new violation stays AttributionNone,
// and Attribution stays nil when none moved.
func (r *RatchetResult) AttributeUntraced(b *Baseline, current []Infraction, note string) {
	if r == nil {
		return
	}
	r.AttributionCommits = nil
	r.AttributionNote = note
	if b == nil {
		return
	}
	r.attribute(b, current, nil)
	if !slices.Contains(r.Attribution, AttributionMoved) {
		r.Attribution, r.RecordedLine = nil, nil
	}
}

// attribute fills Attribution and RecordedLine. existed counts the findings at the compared
// commit; nil means no commit was compared, and a violation that did not move stays
// AttributionNone.
func (r *RatchetResult) attribute(b *Baseline, current []Infraction, existed map[string]int) {
	moved := movedRecorded(b.Infractions, current)
	r.Attribution = make([]Attribution, len(r.NewViolations))
	r.RecordedLine = make([]int, len(r.NewViolations))
	for i := 0; i < len(r.NewViolations); i++ {
		r.Attribution[i], r.RecordedLine[i] = attributeOne(findingKey(r.NewViolations[i]), moved, existed)
	}
}

// attributeOne classifies one new violation, consumes the findings that explain it and returns
// the line the baseline records a moved one at.
func attributeOne(key string, moved map[string][]int, existed map[string]int) (Attribution, int) {
	atCommit := take(existed, key)
	if line, ok := takeLine(moved, key); ok {
		return AttributionMoved, line
	}
	switch {
	case existed == nil:
		return AttributionNone, 0
	case atCommit:
		return AttributionCheckChanged, 0
	default:
		return AttributionIntroduced, 0
	}
}

// take consumes one count of key and reports whether there was one. A nil counts has none.
func take(counts map[string]int, key string) bool {
	if counts[key] <= 0 {
		return false
	}
	counts[key]--
	return true
}

// takeLine consumes the first recorded line of key and reports whether there was one.
func takeLine(lines map[string][]int, key string) (int, bool) {
	queue := lines[key]
	if len(queue) == 0 {
		return 0, false
	}
	lines[key] = queue[1:]
	return queue[0], true
}

// movedRecorded lists, per finding, the lines of the recorded infractions no current violation
// matches (scannedKeys): findings the recorder saw under a key they no longer carry, a line for
// an entry of the line-keyed form, an anchor for any other.
func movedRecorded(recorded, current []Infraction) map[string][]int {
	present := indexScanned(current)
	moved := make(map[string][]int)
	for i := 0; i < len(recorded); i++ {
		if !present.carries(recorded[i]) {
			key := findingKey(recorded[i])
			moved[key] = append(moved[key], recorded[i].LineNumber)
		}
	}
	return moved
}

// countBy counts findings by key: findingKey for their line-independent identity, fingerprintOf
// for the line-bound one.
func countBy(findings []Infraction, key func(Infraction) string) map[string]int {
	counts := make(map[string]int, len(findings))
	for i := 0; i < len(findings); i++ {
		counts[key(findings[i])]++
	}
	return counts
}

// findingKey identifies a finding without its line, which shifts when unrelated lines above it
// change: rule, file, symbol and message.
func findingKey(v Infraction) string {
	return strings.Join([]string{v.RuleID, NormalizePath(v.FilePath), v.Symbol, v.Message}, "\x00")
}
