package baseline

import (
	"fmt"
	"strings"
)

// Attribution says why the baseline does not record one new violation (#599).
type Attribution string

const (
	// AttributionNone means nothing traced the violation: no attribution ran, or it could not
	// read the commit the baseline was recorded at, or the baseline records the same finding at
	// another line. Summary describes it as not in the baseline, never as introduced, because a
	// check added after the baseline was recorded reports unchanged code exactly this way.
	AttributionNone Attribution = ""
	// AttributionIntroduced means the current checks do not report the violation in the files as
	// the baseline's commit holds them: the code changed after the baseline was recorded.
	AttributionIntroduced Attribution = "introduced"
	// AttributionCheckChanged means the current checks report the violation at the baseline's
	// commit too, yet the baseline does not record it: the code is unchanged, and a check or limit
	// added or changed after the baseline was recorded reports it.
	AttributionCheckChanged Attribution = "check-changed"
)

// Commands a rejection names: the read-only listings and the deliberate re-record.
const (
	verifyLister = "'praetorctl baseline --verify --all-violations'"
	auditLister  = "'praetorctl audit --all-violations'"
	recordRemedy = "'praetorctl baseline --record --allow-increase --reason=<why>'"
)

// shortCommitLen is how many characters of the compared commit a rejection prints.
const shortCommitLen = 12

// violationClass is one listed class of a rejection: the tag each line carries, how its
// hidden-remainder marker counts it, and the read-only command that lists every member.
type violationClass struct {
	items  []Infraction
	tag    string
	one    string
	many   string
	lister string
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
		out = append(out, fmt.Sprintf("  [%s] %s:%d - %s (%s)", v.RuleID, v.FilePath, v.LineNumber, v.Message, c.tag))
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

// newPartition splits NewViolations by attribution. Without an attribution for every entry,
// all of them are unattributed.
type newPartition struct {
	introduced, changed, unknown []Infraction
}

func (r *RatchetResult) partitionNew() newPartition {
	var p newPartition
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
		default:
			p.unknown = append(p.unknown, r.NewViolations[i])
		}
	}
	return p
}

// classes lists the rejection's classes in the order Summary prints them.
func (r *RatchetResult) classes(p newPartition) []violationClass {
	return []violationClass{
		{items: p.introduced, tag: "new", one: "new violation", many: "new violations", lister: verifyLister},
		{items: p.changed, tag: "check added or changed since the baseline",
			one: "violation from a changed check", many: "violations from changed checks", lister: verifyLister},
		{items: p.unknown, tag: "not in the baseline",
			one: "violation not in the baseline", many: "violations not in the baseline", lister: verifyLister},
		{items: r.TouchedCleanViolations, tag: "touched file must be clean",
			one: "touched-file violation", many: "touched-file violations", lister: auditLister},
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
	p := r.partitionNew()
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
// code change is called introduced; any other says the baseline does not record them.
func (r *RatchetResult) header(p newPartition) string {
	touched := len(r.TouchedCleanViolations)
	if len(p.changed) == 0 && len(p.unknown) == 0 {
		return fmt.Sprintf("HISS invariant violations introduced (%d total infractions, %d new unbaselined, %d in touched files):",
			r.CurrentCount, len(p.introduced), touched)
	}
	return fmt.Sprintf("HISS invariant violations the baseline does not record (%d total infractions, %d unbaselined%s, %d in touched files):",
		r.CurrentCount, len(r.NewViolations), r.breakdown(p), touched)
}

// breakdown splits the unbaselined count by attribution, once an attribution ran.
func (r *RatchetResult) breakdown(p newPartition) string {
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
	if n := len(p.unknown); n > 0 {
		parts = append(parts, fmt.Sprintf("%d unattributed", n))
	}
	return " (" + strings.Join(parts, ", ") + ")"
}

// explanations says why the violations that are not traced to a code change fail, and what
// records them deliberately.
func (r *RatchetResult) explanations(p newPartition) []string {
	var out []string
	if n := len(p.changed); n > 0 {
		out = append(out, fmt.Sprintf("  %d of them sit in code the current checks flag at %s, the commit the baseline was recorded at, "+
			"and the baseline does not record them: a check or limit added or changed since the baseline was recorded reports them, "+
			"not a code change; fix them or record them with %s", n, shortCommit(r.AttributionCommit), recordRemedy))
	}
	if n := len(p.unknown); n > 0 {
		reason := "no commit was compared"
		if r.AttributionNote != "" {
			reason = r.AttributionNote
		}
		out = append(out, fmt.Sprintf("  %d of them were not traced to a code change (%s); a check added after the baseline was recorded "+
			"reports unchanged code this way too; fix them or record them with %s", n, reason, recordRemedy))
	}
	return out
}

// shortCommit abbreviates a commit for a rejection line.
func shortCommit(commit string) string {
	if len(commit) > shortCommitLen {
		return commit[:shortCommitLen]
	}
	return commit
}

// Attribute classifies each new violation against atCommit (#599): what the current checks
// report, under the same scan policy, in the new violations' files as they stood at commit, the
// commit the baseline b was recorded at. current is the scan the ratchet judged.
//
// A violation the baseline records at another line (a moved fingerprint) was known to the
// recorder and stays AttributionNone. One the current checks also report at commit sits in code
// that has not changed since, so a recorder that had the check would have recorded it:
// AttributionCheckChanged. Anything else is AttributionIntroduced. Findings match on rule, file,
// symbol and message, never on the line, and each finding explains at most one violation.
func (r *RatchetResult) Attribute(b *Baseline, current, atCommit []Infraction, commit string) {
	if r == nil || b == nil {
		return
	}
	moved := movedRecorded(b.Infractions, current)
	existed := countFindings(atCommit)
	r.Attribution = make([]Attribution, len(r.NewViolations))
	for i := 0; i < len(r.NewViolations); i++ {
		r.Attribution[i] = attributeOne(findingKey(r.NewViolations[i]), moved, existed)
	}
	r.AttributionCommit = commit
	r.AttributionNote = ""
}

// attributeOne classifies one new violation and consumes the findings that explain it.
func attributeOne(key string, moved, existed map[string]int) Attribution {
	atCommit := take(existed, key)
	switch {
	case take(moved, key):
		return AttributionNone
	case atCommit:
		return AttributionCheckChanged
	default:
		return AttributionIntroduced
	}
}

// take consumes one count of key and reports whether there was one.
func take(counts map[string]int, key string) bool {
	if counts[key] <= 0 {
		return false
	}
	counts[key]--
	return true
}

// movedRecorded counts, per finding, the recorded infractions whose fingerprint no current
// violation carries: findings the recorder saw at a line they no longer occupy.
func movedRecorded(recorded, current []Infraction) map[string]int {
	present := make(map[string]struct{}, len(current))
	for i := 0; i < len(current); i++ {
		present[NormalizePath(current[i].Fingerprint)] = struct{}{}
	}
	moved := make(map[string]int)
	for i := 0; i < len(recorded); i++ {
		if _, ok := present[NormalizePath(recorded[i].Fingerprint)]; !ok {
			moved[findingKey(recorded[i])]++
		}
	}
	return moved
}

// countFindings counts findings by their line-independent identity.
func countFindings(findings []Infraction) map[string]int {
	counts := make(map[string]int, len(findings))
	for i := 0; i < len(findings); i++ {
		counts[findingKey(findings[i])]++
	}
	return counts
}

// findingKey identifies a finding without its line, which shifts when unrelated lines above it
// change: rule, file, symbol and message.
func findingKey(v Infraction) string {
	return strings.Join([]string{v.RuleID, NormalizePath(v.FilePath), v.Symbol, v.Message}, "\x00")
}
