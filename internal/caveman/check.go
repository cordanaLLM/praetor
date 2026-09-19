package caveman

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// Check rule identifiers. The thresholds come from measurements on real inputs
// (2026-09-18): AGENTS.md carries 8.4 articles per 100 prose words, its hand-written
// caveman rewrite 0.3, so 2.0 separates the two with room on both sides.
const (
	RuleArticleDensity = "C1 article-density"
	RuleFiller         = "C2 filler"
	RuleHedge          = "C3 hedge"
	RuleTerminalNoise  = "C4 terminal-noise"
	RuleLongSentence   = "C5 long-sentence"
	RuleUnclosedOff    = "C6 unclosed-off-region"
	// RuleWordCeiling fires when Options.MaxProseWords is set and the text carries more
	// prose words than the ceiling. It is opt-in (a zero or negative MaxProseWords disables
	// it), unlike C1-C6, because the ceiling is per surface (600 words for a persona or a
	// skill; text-register.md), not a property of caveman prose in general.
	RuleWordCeiling = "C7 word-ceiling"
	// RuleTokenCeiling fires when Options.MaxTokens is set and EstimateTokens of the whole
	// input (prose, code and structured lines together, since a dispatch pays for all of
	// it) exceeds the ceiling. Opt-in like RuleWordCeiling: it makes the evidence-pointer
	// bound (register.evidence, default 1500 tokens) and any other per-surface token budget
	// enforceable wherever the text is a file 'praetorctl caveman check' can read, per
	// text-register.md's "What is not enforced" list.
	RuleTokenCeiling = "C8 token-ceiling"
)

const (
	// DefaultMaxArticleDensity is the article ceiling per 100 prose words.
	DefaultMaxArticleDensity = 2.0
	// DefaultMinProseWords is the prose size below which density is not judged: a short
	// text has too few words for a rate to mean anything.
	DefaultMinProseWords = 40
	// DefaultMaxSentenceWords is the longest sentence without a `;`, `->` or `:` break.
	DefaultMaxSentenceWords = 30
	// excerptWords is how much of a long sentence a finding quotes.
	excerptWords = 6
)

var (
	fillerRe = regexp.MustCompile(`(?i)\b(based on|i think|note that|it is important|in order to|as requested|let me|please)\b`)
	hedgeRe  = regexp.MustCompile(`(?i)\b(probably|seems|might|basically|simply|just|really|actually)\b`)
	quotedRe = regexp.MustCompile(`"[^"\n]*"|“[^”\n]*”`)
	// sentenceBreakRe ends a sentence, or a clause the reader can parse on its own.
	sentenceBreakRe = regexp.MustCompile(`[.!?]+(?:\s|$)|;|->|→|:(?:\s|$)`)
	listItemRe      = regexp.MustCompile(`^(?:[-*+]|\d+[.)])\s`)
)

// Options tunes Check. A zero or negative field takes its default, except MaxProseWords:
// zero or negative there means no ceiling, since most callers (AGENTS.md, MCP text, hook
// messages) have no per-file word budget and only a surface that defines one should turn
// it on.
type Options struct {
	MaxArticleDensity float64
	MinProseWords     int
	MaxSentenceWords  int
	// MaxProseWords bounds Report.ProseWords when positive. It is independent of
	// MinProseWords/MaxArticleDensity: a text under the density judgment threshold can
	// still break a word ceiling.
	MaxProseWords int
	// MaxTokens bounds Report.EstimatedTokens when positive: the whole input, not prose
	// alone, since a dispatch or an evidence bound pays for code and structure too.
	MaxTokens int
}

func (o Options) withDefaults() Options {
	if o.MaxArticleDensity <= 0 {
		o.MaxArticleDensity = DefaultMaxArticleDensity
	}
	if o.MinProseWords <= 0 {
		o.MinProseWords = DefaultMinProseWords
	}
	if o.MaxSentenceWords <= 0 {
		o.MaxSentenceWords = DefaultMaxSentenceWords
	}
	return o
}

// Finding is one rule violation. Line is 1-based; 0 means the whole text.
type Finding struct {
	Line    int
	Rule    string
	Excerpt string
}

func (f Finding) String() string {
	return fmt.Sprintf("%d: %s: %s", f.Line, f.Rule, f.Excerpt)
}

// Report is the result of Check or Floor. The counts print on a clean run too: a report
// that lists only failures cannot be told apart from one that read nothing.
type Report struct {
	Findings   []Finding
	ProseWords int
	Articles   int
	OffRegions int
	// EstimatedTokens is EstimateTokens of the whole input text, always computed (it is one
	// Fields() pass) so a caller can read the cost even when Options.MaxTokens is unset.
	EstimatedTokens int
}

// Passed reports whether the text broke no rule.
func (r Report) Passed() bool {
	return len(r.Findings) == 0
}

// Density returns articles per 100 prose words, 0 for a text without prose.
func (r Report) Density() float64 {
	if r.ProseWords == 0 {
		return 0
	}
	return float64(r.Articles) * 100 / float64(r.ProseWords)
}

// findings collects each distinct finding once and returns them in a stable order.
type findings struct {
	seen map[Finding]bool
	list []Finding
}

func (f *findings) add(num int, rule, excerpt string) {
	finding := Finding{Line: num, Rule: rule, Excerpt: excerpt}
	if f.seen == nil {
		f.seen = map[Finding]bool{}
	}
	if !f.seen[finding] {
		f.seen[finding] = true
		f.list = append(f.list, finding)
	}
}

func (f *findings) sorted() []Finding {
	sort.Slice(f.list, func(i, j int) bool {
		a, b := f.list[i], f.list[j]
		if a.Line != b.Line {
			return a.Line < b.Line
		}
		if a.Rule != b.Rule {
			return a.Rule < b.Rule
		}
		return a.Excerpt < b.Excerpt
	})
	return f.list
}

// Check lints agent-facing text. Fenced code, inline code, link targets, URLs, headings,
// tables, HTML comments, ledger field rows, hook protocol lines, evidence pointers and
// caveman:off regions are not prose and never trip a prose rule. Findings are sorted by
// line, rule and excerpt, so equal input yields an equal report.
func Check(text string, opts Options) Report {
	opts = opts.withDefaults()
	lines, s := scan(text)
	report := Report{OffRegions: s.offRegions, EstimatedTokens: EstimateTokens(text)}
	var found findings
	for _, ln := range lines {
		checkNoise(&found, ln)
		if ln.kind != kindProse {
			continue
		}
		prose := proseOf(ln.text)
		report.count(prose)
		checkPhrases(&found, ln.num, quotedRe.ReplaceAllString(prose, " "))
	}
	checkSentences(&found, paragraphs(lines), opts.MaxSentenceWords)
	if report.ProseWords >= opts.MinProseWords && report.Density() > opts.MaxArticleDensity {
		found.add(0, RuleArticleDensity, fmt.Sprintf("%.1f articles per 100 prose words (%d/%d), limit %.1f",
			report.Density(), report.Articles, report.ProseWords, opts.MaxArticleDensity))
	}
	if opts.MaxProseWords > 0 && report.ProseWords > opts.MaxProseWords {
		found.add(0, RuleWordCeiling, fmt.Sprintf("%d prose words, limit %d", report.ProseWords, opts.MaxProseWords))
	}
	if opts.MaxTokens > 0 && report.EstimatedTokens > opts.MaxTokens {
		found.add(0, RuleTokenCeiling, fmt.Sprintf("%d estimated tokens, limit %d", report.EstimatedTokens, opts.MaxTokens))
	}
	if s.off {
		found.add(s.offOpen, RuleUnclosedOff, OffMarker+" without "+OnMarker)
	}
	report.Findings = found.sorted()
	return report
}

// count adds the prose words and articles of one masked prose line.
func (r *Report) count(prose string) {
	for _, word := range proseWords(prose) {
		r.ProseWords++
		if word == "a" || word == "an" || word == "the" {
			r.Articles++
		}
	}
}

// checkPhrases reports filler and hedge words. Quoted spans are masked by the caller, so
// a rule that names a banned phrase in quotes does not trip itself.
func checkPhrases(found *findings, num int, prose string) {
	for _, match := range fillerRe.FindAllString(prose, -1) {
		found.add(num, RuleFiller, strings.ToLower(match))
	}
	for _, match := range hedgeRe.FindAllString(prose, -1) {
		found.add(num, RuleHedge, strings.ToLower(match))
	}
}

// checkNoise reports terminal decoration. An ANSI escape is noise anywhere, fenced logs
// included; box drawing and emoji are noise outside code.
func checkNoise(found *findings, ln line) {
	if ln.kind == kindOff {
		return
	}
	if strings.Contains(ln.text, "\x1b") {
		found.add(ln.num, RuleTerminalNoise, "ANSI escape")
	}
	if ln.kind == kindCode {
		return
	}
	for _, r := range inlineCodeRe.ReplaceAllString(ln.text, " ") {
		if isNoiseRune(r) {
			found.add(ln.num, RuleTerminalNoise, fmt.Sprintf("U+%04X", r))
			return
		}
	}
}

// isNoiseRune matches box drawing (U+2500-257F), the pictograph and dingbat blocks and the
// emoji variation selector.
func isNoiseRune(r rune) bool {
	return r >= 0x2500 && r <= 0x257F || r >= 0x2600 && r <= 0x27BF ||
		r >= 0x1F000 && r <= 0x1FAFF || r == 0xFE0F
}

// paragraph is a run of prose lines read as one text; start is its first line.
type paragraph struct {
	start int
	text  strings.Builder
}

// paragraphs joins wrapped prose lines. A blank or non-prose line ends a paragraph and a
// list marker starts a new one, so each list item is judged on its own.
func paragraphs(lines []line) []*paragraph {
	var out []*paragraph
	var current *paragraph
	for _, ln := range lines {
		if ln.kind != kindProse {
			current = nil
			continue
		}
		if current == nil || listItemRe.MatchString(strings.TrimSpace(ln.text)) {
			current = &paragraph{start: ln.num}
			out = append(out, current)
		}
		current.text.WriteString(proseOf(ln.text))
		current.text.WriteByte(' ')
	}
	return out
}

// checkSentences reports every sentence longer than limit prose words. The finding sits on
// the first line of the paragraph that holds the sentence.
func checkSentences(found *findings, paras []*paragraph, limit int) {
	for _, para := range paras {
		for _, sentence := range sentenceBreakRe.Split(para.text.String(), -1) {
			words := proseWords(sentence)
			if len(words) > limit {
				lead := words[:min(len(words), excerptWords)]
				found.add(para.start, RuleLongSentence, fmt.Sprintf("%d words: %s ...", len(words), strings.Join(lead, " ")))
			}
		}
	}
}
