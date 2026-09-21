package caveman

import (
	"fmt"
	"regexp"
	"strings"
)

// MessageKind selects the contract Check applies. Message, brief and return are runtime
// internal traffic. Context is policy text (AGENTS.md, personas and skills) that describes
// the contract and therefore keeps the measured C1-C8 checks without banning every grammar
// token it must name. The zero value is Context for source compatibility; runtime producers
// and the CLI select Message explicitly.
type MessageKind string

const (
	KindMessage MessageKind = "message"
	KindBrief   MessageKind = "brief"
	KindReturn  MessageKind = "return"
	KindContext MessageKind = "context"
)

// Enforcement states whether Check can decide a Caveman skill rule from one text alone.
type Enforcement string

const (
	EnforcementMechanical Enforcement = "mechanical"
	EnforcementAdvisory   Enforcement = "advisory"
	SkillRuleCount                    = 8
)

// RuleCoverage maps one numbered rule in .agents/skills/caveman/SKILL.md to Check's
// enforcement. Detail names the exact mechanical boundary or why judgment remains needed.
type RuleCoverage struct {
	SkillRule   int
	Enforcement Enforcement
	Detail      string
}

var (
	schemaFieldRe  = regexp.MustCompile(`(?i)(?:^|[[:space:]])(goal|inputs|return|evidence|task|verdict|changed|ran|open):`)
	fieldAtStartRe = regexp.MustCompile(`(?i)^(?:[-*+]\s+)?(goal|inputs|return|evidence|task|verdict|changed|ran|open):`)
	asIsRe         = regexp.MustCompile(`(?i)\bas\s+is\b`)
	apostrophes    = strings.NewReplacer("’", "'", "‘", "'")
	grammarTerms   = map[string]string{
		"a": "article", "an": "article", "the": "article",
		"i": "pronoun", "me": "pronoun", "my": "pronoun", "mine": "pronoun", "myself": "pronoun",
		"we": "pronoun", "us": "pronoun", "our": "pronoun", "ours": "pronoun", "ourselves": "pronoun",
		"you": "pronoun", "your": "pronoun", "yours": "pronoun", "yourself": "pronoun", "yourselves": "pronoun",
		"he": "pronoun", "him": "pronoun", "his": "pronoun", "himself": "pronoun",
		"she": "pronoun", "her": "pronoun", "hers": "pronoun", "herself": "pronoun",
		"it": "pronoun", "its": "pronoun", "itself": "pronoun",
		"they": "pronoun", "them": "pronoun", "their": "pronoun", "theirs": "pronoun", "themselves": "pronoun",
		"am": "copula", "is": "copula", "are": "copula", "was": "copula", "were": "copula", "be": "copula",
		"being": "copula", "been": "copula",
		"have": "auxiliary", "has": "auxiliary", "had": "auxiliary", "having": "auxiliary",
		"do": "auxiliary", "does": "auxiliary", "did": "auxiliary", "doing": "auxiliary", "will": "auxiliary",
		"can": "modal", "cannot": "modal", "could": "modal", "may": "modal", "might": "modal",
		"must": "modal", "shall": "modal", "should": "modal", "would": "modal",
		"thanks": "politeness", "thank": "politeness",
	}
	grammarContractions = map[string]string{
		"i'm": "pronoun", "you're": "pronoun", "we're": "pronoun", "they're": "pronoun",
		"he's": "pronoun", "she's": "pronoun", "it's": "pronoun",
		"i've": "pronoun", "you've": "pronoun", "we've": "pronoun", "they've": "pronoun",
		"i'll": "pronoun", "you'll": "pronoun", "he'll": "pronoun", "she'll": "pronoun",
		"it'll": "pronoun", "we'll": "pronoun", "they'll": "pronoun",
		"i'd": "pronoun", "you'd": "pronoun", "he'd": "pronoun", "she'd": "pronoun",
		"it'd": "pronoun", "we'd": "pronoun", "they'd": "pronoun",
		"isn't": "copula", "aren't": "copula", "wasn't": "copula", "weren't": "copula", "ain't": "copula",
		"haven't": "auxiliary", "hasn't": "auxiliary", "hadn't": "auxiliary",
		"don't": "auxiliary", "doesn't": "auxiliary", "didn't": "auxiliary",
		"can't": "modal", "couldn't": "modal", "mayn't": "modal", "mightn't": "modal",
		"mustn't": "modal", "shan't": "modal", "shouldn't": "modal", "won't": "modal", "wouldn't": "modal",
	}
)

func (k MessageKind) normalized() MessageKind {
	if k == "" {
		return KindContext
	}
	return k
}

// Valid reports whether kind names a supported checker contract.
func (k MessageKind) Valid() bool {
	switch k.normalized() {
	case KindMessage, KindBrief, KindReturn, KindContext:
		return true
	default:
		return false
	}
}

func (k MessageKind) strictGrammar() bool {
	return k.normalized() != KindContext
}

func contractCoverage(kind MessageKind) []RuleCoverage {
	kind = kind.normalized()
	rows := []RuleCoverage{
		{1, EnforcementAdvisory, "C1-C3 measure selected grammar; strict message kinds add C9 lexical drops"},
		{2, EnforcementAdvisory, "one-fact semantics require reader judgment"},
		{3, EnforcementAdvisory, "symbol substitution depends on meaning"},
		{4, EnforcementAdvisory, "verbatim preservation requires before/after Floor input"},
		{5, EnforcementAdvisory, "generic answer-first intent requires caller context"},
		{6, EnforcementAdvisory, "table usefulness requires comparison intent"},
		{7, EnforcementAdvisory, "return schema applies only to return kind"},
		{8, EnforcementAdvisory, "artifact placement and line bound require producer metadata"},
	}
	if kind.strictGrammar() {
		rows[0] = RuleCoverage{1, EnforcementMechanical, "C9 rejects listed grammar classes; C2-C3 reject framing and hedges"}
	}
	if kind == KindBrief || kind == KindReturn {
		rows[4] = RuleCoverage{5, EnforcementMechanical, "C10 requires goal/verdict first"}
	}
	if kind == KindReturn {
		rows[6] = RuleCoverage{7, EnforcementMechanical, "C10 requires verdict, changed, ran, evidence and open fields"}
	}
	return rows
}

func checkGrammar(found *findings, num int, prose string) {
	prose = asIsRe.ReplaceAllString(prose, " ")
	for _, field := range strings.Fields(prose) {
		if protectedGrammarToken(field) {
			continue
		}
		word := strings.ToLower(strings.TrimFunc(field, notLetter))
		word = apostrophes.Replace(word)
		class, banned := grammarTerms[word]
		if !banned {
			class, banned = grammarContractions[word]
		}
		if banned {
			found.add(num, RuleGrammar, fmt.Sprintf("%s %q", class, word))
		}
	}
}

func protectedGrammarToken(field string) bool {
	trimmed := strings.Trim(field, "()[]{}<>,;\"")
	return strings.ContainsAny(trimmed, `/\\`) || strings.HasPrefix(trimmed, "-") ||
		strings.Contains(trimmed, "://") || strings.Contains(trimmed, "@")
}

type messageShape struct {
	first    string
	required []string
}

func shapeFor(kind MessageKind) (messageShape, bool) {
	switch kind.normalized() {
	case KindBrief:
		return messageShape{"goal", []string{"goal", "inputs", "return", "evidence", "task"}}, true
	case KindReturn:
		return messageShape{"verdict", []string{"verdict", "changed", "ran", "evidence", "open"}}, true
	default:
		return messageShape{}, false
	}
}

func checkMessageShape(found *findings, lines []line, kind MessageKind) {
	shape, enforced := shapeFor(kind)
	if !enforced {
		return
	}
	seen := map[string]int{}
	firstChecked := false
	for _, ln := range lines {
		if !shapeContent(ln) {
			continue
		}
		lintable := maskQuoted(proseOf(ln.text))
		fields := schemaFields(lintable)
		field := schemaFieldAtStart(lintable)
		if !firstChecked {
			checkFirstField(found, ln.num, field, shape.first)
			firstChecked = true
		}
		if len(fields) > 1 {
			found.add(ln.num, RuleMessageShape, "one schema field per line")
		}
		if field != "" {
			seen[field]++
			if seen[field] > 1 {
				found.add(ln.num, RuleMessageShape, "duplicate "+field+" field")
			}
		}
	}
	for _, field := range shape.required {
		if seen[field] == 0 {
			found.add(0, RuleMessageShape, "missing "+field+" field")
		}
	}
}

func shapeContent(ln line) bool {
	if ln.kind == kindBlank || ln.kind == kindCode || ln.kind == kindOff {
		return false
	}
	trimmed := strings.TrimSpace(ln.text)
	return !strings.HasPrefix(trimmed, "<!--") && !strings.HasPrefix(trimmed, "|")
}

func schemaFields(text string) []string {
	matches := schemaFieldRe.FindAllStringSubmatch(text, -1)
	fields := make([]string, 0, len(matches))
	for _, match := range matches {
		fields = append(fields, strings.ToLower(match[1]))
	}
	return fields
}

func schemaFieldAtStart(text string) string {
	match := fieldAtStartRe.FindStringSubmatch(strings.TrimSpace(text))
	if len(match) < 2 {
		return ""
	}
	return strings.ToLower(match[1])
}

func checkFirstField(found *findings, line int, got, want string) {
	if got != want {
		found.add(line, RuleMessageShape, want+" must be first")
	}
}
