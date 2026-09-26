package caveman

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
	"unicode"
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
	// it), unlike C1-C6 and C13, because the ceiling is per surface (600 words for a persona
	// or a skill; text-register.md), not a property of caveman prose in general.
	RuleWordCeiling = "C7 word-ceiling"
	// RuleTokenCeiling fires when Options.MaxTokens is set and EstimateTokens of the whole
	// input (prose, code and structured lines together, since a dispatch pays for all of
	// it) exceeds the ceiling. Opt-in like RuleWordCeiling: it makes the evidence-pointer
	// bound (register.evidence, default 1500 tokens) and any other per-surface token budget
	// enforceable wherever the text is a file 'praetorctl caveman check' can read, per
	// text-register.md's "What is not enforced" list.
	RuleTokenCeiling = "C8 token-ceiling"
	// RuleGrammar rejects the grammar classes explicitly listed by the Caveman skill for
	// runtime message, brief and return kinds. Context policy text uses the measured C1-C8
	// profile because it must be able to name those tokens.
	RuleGrammar = "C9 grammar"
	// RuleMessageShape enforces the documented brief and return fields and answer-first
	// ordering when Options.Kind selects either structured message kind.
	RuleMessageShape = "C10 message-shape"
	// RuleRuntimeEscape rejects source-document constructs that can suppress prose. Runtime
	// emissions are adversarial text, not Markdown sources with trusted escape regions.
	RuleRuntimeEscape = "C11 runtime-source-escape"
	// RuleRuntimeEvidence rejects pointer-shaped evidence fields unless the complete field
	// is the canonical path, digest and line-count form.
	RuleRuntimeEvidence = "C12 runtime-evidence-pointer"
	// RuleUnclosedFence fires when a fenced code block is still open at the end of the
	// text. Everything after the opening fence then counts as code, so the other rules never
	// see it: an unclosed fence in a skill template once hid a whole section from C1.
	RuleUnclosedFence = "C13 unclosed-fence"
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
	doubleQuotedRe = regexp.MustCompile(`"[^"\n]*"|“[^”\n]*”`)
	// A single quote opens a protected diagnostic only at a text boundary. Apostrophes
	// inside contractions remain visible to C9, including straight and curly forms.
	singleQuotedRe = regexp.MustCompile(`(^|[[:space:][:punct:]])(?:'[^'\n]*'|‘[^’\n]*’)`)
	// sentenceBreakRe ends a sentence, or a clause the reader can parse on its own.
	sentenceBreakRe = regexp.MustCompile(`[.!?]+(?:\s|$)|;|->|→|:(?:\s|$)`)
	listItemRe      = regexp.MustCompile(`^(?:[-*+]|\d+[.)])\s`)
	phraseTerms     = map[string]phraseRule{
		"basedon":       {RuleFiller, "based on"},
		"ithink":        {RuleFiller, "i think"},
		"notethat":      {RuleFiller, "note that"},
		"itisimportant": {RuleFiller, "it is important"},
		"itlookslike":   {RuleFiller, "it looks like"},
		"inorderto":     {RuleFiller, "in order to"},
		"asrequested":   {RuleFiller, "as requested"},
		"letme":         {RuleFiller, "let me"},
		"please":        {RuleFiller, "please"},
		"probably":      {RuleHedge, "probably"},
		"seems":         {RuleHedge, "seems"},
		"might":         {RuleHedge, "might"},
		"basically":     {RuleHedge, "basically"},
		"simply":        {RuleHedge, "simply"},
		"just":          {RuleHedge, "just"},
		"really":        {RuleHedge, "really"},
		"actually":      {RuleHedge, "actually"},
	}
)

const maxPhraseFields = 3

type phraseRule struct {
	rule    string
	excerpt string
}

// Options tunes Check. A zero or negative field takes its default, except MaxProseWords:
// zero or negative there means no ceiling, since most callers (AGENTS.md, MCP text, hook
// messages) have no per-file word budget and only a surface that defines one should turn
// it on.
type Options struct {
	// Kind selects message, brief, return or context checking. Empty means context for
	// source compatibility; runtime callers must select their kind explicitly.
	Kind              MessageKind
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
	Kind       MessageKind
	// Coverage classifies every numbered Caveman skill rule as mechanical or advisory for
	// this message kind. A passing report means all mechanical rows passed, not that
	// advisory rows were judged.
	Coverage []RuleCoverage
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

// Check lints agent-facing text under Options.Kind. Fenced code, inline code, link targets,
// URLs, headings, HTML comments, ledger field rows, hook protocol lines, evidence pointers
// and caveman:off regions never trip a prose rule. Markdown table delimiters stay
// structured while their cells are prose. Findings are sorted by line, rule and excerpt,
// so equal input yields an equal report.
func Check(text string, opts Options) Report {
	return checkProfile(text, opts, false)
}

// CheckRuntime applies the adversarial runtime profile. Unlike Check, it exposes quoted
// and inline-code text to grammar checks and rejects source-only suppression constructs.
func CheckRuntime(text string, opts Options) Report {
	return checkProfile(text, opts, true)
}

func checkProfile(text string, opts Options, runtime bool) Report {
	opts = opts.withDefaults()
	lines, s := scan(text)
	kind := opts.Kind.normalized()
	report := Report{OffRegions: s.offRegions, EstimatedTokens: EstimateTokens(text), Kind: kind, Coverage: contractCoverage(kind)}
	var found findings
	checkProfileLines(&report, &found, lines, kind, runtime)
	paras := profileParagraphs(lines, runtime)
	checkPhrases(&found, paras, runtime)
	checkSentences(&found, paras, opts.MaxSentenceWords)
	checkMessageShape(&found, lines, kind)
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
	checkOpenRegions(&found, s)
	report.Findings = found.sorted()
	return report
}

// checkOpenRegions reports a caveman:off region or a fenced code block still open when the
// text ends, at the line that opened it.
func checkOpenRegions(found *findings, s scanner) {
	if s.off {
		found.add(s.offOpen, RuleUnclosedOff, OffMarker+" without "+OnMarker)
	}
	if s.fence != "" {
		found.add(s.fenceOpen, RuleUnclosedFence, s.fence+" fence without a closing "+s.fence)
	}
}

func checkProfileLines(report *Report, found *findings, lines []line, kind MessageKind, runtime bool) {
	for _, ln := range lines {
		checkNoise(found, ln, runtime)
		if runtime {
			checkRuntimeStructure(found, ln)
			checkRuntimeEvidence(found, ln)
		}
		segments := proseSegments(ln)
		if runtime {
			segments = runtimeProseSegments(ln)
		}
		for _, prose := range segments {
			report.count(prose)
			lintable := prose
			if !runtime {
				lintable = maskQuoted(prose)
			}
			if kind.strictGrammar() {
				checkGrammar(found, ln.num, lintable)
			}
		}
	}
}

// maskQuoted protects verbatim diagnostics without hiding contraction apostrophes.
func maskQuoted(prose string) string {
	masked := doubleQuotedRe.ReplaceAllString(prose, " ")
	return singleQuotedRe.ReplaceAllString(masked, "$1 ")
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

// checkPhrases reports filler and hedge phrases across punctuation and wrapped lines.
// Each target has at most maxPhraseFields lexical fields; protected technical literals
// terminate a candidate so paths, URLs, email addresses and flags remain verbatim.
func checkPhrases(found *findings, paras []*paragraph, runtime bool) {
	for _, para := range paras {
		checkPhraseFields(found, phraseFields(para.segments, runtime))
	}
}

func checkPhraseFields(found *findings, fields []phraseField) {
	for start := 0; start < len(fields); start++ {
		var joined strings.Builder
		for width := 0; width < maxPhraseFields && start+width < len(fields); width++ {
			field := fields[start+width]
			if field.value == "" {
				break
			}
			joined.WriteString(field.value)
			if match, ok := phraseTerms[joined.String()]; ok {
				found.add(fields[start].line, match.rule, match.excerpt)
			}
		}
	}
}

func phraseFields(segments []paragraphSegment, runtime bool) []phraseField {
	var out []phraseField
	for _, segment := range segments {
		prose := segment.text
		if !runtime {
			prose = maskQuoted(prose)
		}
		for _, field := range strings.Fields(prose) {
			if protectedGrammarToken(field) {
				out = append(out, phraseField{line: segment.line})
				continue
			}
			if value := collapsedGrammarWord(field); value != "" {
				out = append(out, phraseField{line: segment.line, value: value})
			}
		}
	}
	return out
}

// checkNoise reports terminal decoration. ANSI escapes, unsafe control characters and
// Unicode default-ignorables are noise anywhere, fenced logs included; box drawing and
// emoji are noise outside code. Tabs remain valid spacing; scan normalizes CRLF and splits
// LF before this boundary, so every control rune still present on a line is unsafe.
func checkNoise(found *findings, ln line, runtime bool) {
	checkLineControls(found, ln)
	if ln.kind == kindOff && !runtime {
		return
	}
	if ln.kind == kindCode && !runtime {
		return
	}
	text := ln.text
	if !runtime {
		text = inlineCodeRe.ReplaceAllString(text, " ")
	}
	for _, r := range text {
		if isNoiseRune(r) {
			found.add(ln.num, RuleTerminalNoise, fmt.Sprintf("U+%04X", r))
			return
		}
	}
}

// checkLineControls rejects invisible content before source-profile masking.
func checkLineControls(found *findings, ln line) {
	if strings.Contains(ln.text, "\x1b") {
		found.add(ln.num, RuleTerminalNoise, "ANSI escape")
	}
	for _, r := range ln.text {
		if r == '\u2028' || r == '\u2029' {
			found.add(ln.num, RuleTerminalNoise, fmt.Sprintf("U+%04X line separator", r))
			break
		}
		if isDefaultIgnorableRune(r) {
			found.add(ln.num, RuleTerminalNoise, fmt.Sprintf("U+%04X default-ignorable", r))
			break
		}
		if isUnsafeControlRune(r) {
			if r != '\x1b' {
				found.add(ln.num, RuleTerminalNoise, fmt.Sprintf("U+%04X control", r))
			}
			break
		}
	}
}

// isNoiseRune matches box drawing (U+2500-257F), the pictograph and dingbat blocks and the
// emoji variation selector.
func isNoiseRune(r rune) bool {
	return r >= 0x2500 && r <= 0x257F || r >= 0x2600 && r <= 0x27BF ||
		r >= 0x1F000 && r <= 0x1FAFF || r == 0xFE0F
}

// isDefaultIgnorableRune matches Unicode format controls, the otherwise-combining
// Other_Default_Ignorable_Code_Point set and variation selectors. Visible combining
// marks such as U+0301 stay valid prose.
func isDefaultIgnorableRune(r rune) bool {
	return unicode.Is(unicode.Cf, r) || unicode.Is(unicode.Other_Default_Ignorable_Code_Point, r) ||
		unicode.Is(unicode.Variation_Selector, r)
}

func isUnsafeControlRune(r rune) bool {
	return r != '\t' && unicode.IsControl(r)
}

// paragraph is a run of prose lines read as one text; start is its first line.
type paragraph struct {
	start    int
	text     strings.Builder
	segments []paragraphSegment
}

type paragraphSegment struct {
	line int
	text string
}

type phraseField struct {
	line  int
	value string
}

func (p *paragraph) append(line int, text string) {
	p.text.WriteString(text)
	p.text.WriteByte(' ')
	p.segments = append(p.segments, paragraphSegment{line: line, text: text})
}

// paragraphs joins wrapped prose lines. A blank or non-prose line ends a paragraph and a
// list marker starts a new one. Each table cell becomes its own paragraph.
func paragraphs(lines []line) []*paragraph {
	var out []*paragraph
	var current *paragraph
	for _, ln := range lines {
		if ln.kind != kindProse {
			current = nil
			for _, cell := range proseSegments(ln) {
				para := &paragraph{start: ln.num}
				para.append(ln.num, cell)
				out = append(out, para)
			}
			continue
		}
		if current == nil || listItemRe.MatchString(strings.TrimSpace(ln.text)) {
			current = &paragraph{start: ln.num}
			out = append(out, current)
		}
		current.append(ln.num, proseOf(ln.text))
	}
	return out
}

func profileParagraphs(lines []line, runtime bool) []*paragraph {
	if !runtime {
		return paragraphs(lines)
	}
	var out []*paragraph
	var current *paragraph
	for _, ln := range lines {
		if ln.kind != kindProse {
			current = nil
			for _, segment := range runtimeProseSegments(ln) {
				para := &paragraph{start: ln.num}
				para.append(ln.num, segment)
				out = append(out, para)
			}
			continue
		}
		if current == nil || listItemRe.MatchString(strings.TrimSpace(ln.text)) {
			para := &paragraph{start: ln.num}
			out = append(out, para)
			current = para
		}
		current.append(ln.num, runtimeProseOf(ln.text))
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
