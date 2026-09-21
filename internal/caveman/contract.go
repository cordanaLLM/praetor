package caveman

import (
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
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
	literalFlagRe  = regexp.MustCompile(`^--?[A-Za-z0-9][A-Za-z0-9_.-]*(?:=[^\s=]+)?$`)
	msvcDefineRe   = regexp.MustCompile(`^/D[A-Za-z_][A-Za-z0-9_]*(?:=[^\s=]+)?$`)
	literalMailRe  = regexp.MustCompile(`^[A-Za-z0-9.!#$%&'*+/=?^_{|}~-]+@[A-Za-z0-9](?:[A-Za-z0-9-]*[A-Za-z0-9])?(?:\.[A-Za-z0-9](?:[A-Za-z0-9-]*[A-Za-z0-9])?)+$`)
	diagnosticRe   = regexp.MustCompile(`:\d+(?::\d+)?$`)
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

const (
	maxLiteralWrapperDepth = 4
	maxLiteralPathSegments = 256
)

type compatibilityLetterSpan struct {
	first rune
	last  rune
	base  rune
}

type compatibilityLetterPair struct {
	source rune
	target rune
}

// compatibilityLetterSpans bounds the compatibility-letter fold to fixed Latin ranges.
// The mathematical ranges include Unicode holes; folding an unassigned scalar is safe and
// keeps the loop independent of Unicode table changes in the Go toolchain.
var compatibilityLetterSpans = [...]compatibilityLetterSpan{
	{0x249C, 0x24B5, 'a'},
	{'Ⓐ', 'Ⓩ', 'A'}, {'ⓐ', 'ⓩ', 'a'},
	{0x1D400, 0x1D419, 'A'}, {0x1D41A, 0x1D433, 'a'},
	{0x1D434, 0x1D44D, 'A'}, {0x1D44E, 0x1D467, 'a'},
	{0x1D468, 0x1D481, 'A'}, {0x1D482, 0x1D49B, 'a'},
	{0x1D49C, 0x1D4B5, 'A'}, {0x1D4B6, 0x1D4CF, 'a'},
	{0x1D4D0, 0x1D4E9, 'A'}, {0x1D4EA, 0x1D503, 'a'},
	{0x1D504, 0x1D51D, 'A'}, {0x1D51E, 0x1D537, 'a'},
	{0x1D538, 0x1D551, 'A'}, {0x1D552, 0x1D56B, 'a'},
	{0x1D56C, 0x1D585, 'A'}, {0x1D586, 0x1D59F, 'a'},
	{0x1D5A0, 0x1D5B9, 'A'}, {0x1D5BA, 0x1D5D3, 'a'},
	{0x1D5D4, 0x1D5ED, 'A'}, {0x1D5EE, 0x1D607, 'a'},
	{0x1D608, 0x1D621, 'A'}, {0x1D622, 0x1D63B, 'a'},
	{0x1D63C, 0x1D655, 'A'}, {0x1D656, 0x1D66F, 'a'},
	{0x1D670, 0x1D689, 'A'}, {0x1D68A, 0x1D6A3, 'a'},
}

// compatibilityLatinLetters contains fixed non-contiguous Latin confusables and the
// legacy letterlike symbols omitted from the mathematical alphabet blocks. Each entry
// folds to one ASCII letter; no general Unicode normalization runs on literal spelling.
var compatibilityLatinLetters = [...]compatibilityLetterPair{
	{'ℂ', 'C'}, {'ℊ', 'g'}, {'ℋ', 'H'}, {'ℌ', 'H'}, {'ℍ', 'H'},
	{'ℎ', 'h'}, {'ℐ', 'I'}, {'ℑ', 'I'}, {'ℒ', 'L'}, {'ℓ', 'l'},
	{'ℕ', 'N'}, {'ℙ', 'P'}, {'ℚ', 'Q'}, {'ℛ', 'R'}, {'ℜ', 'R'},
	{'ℝ', 'R'}, {'ℤ', 'Z'}, {'ℨ', 'Z'}, {'ℬ', 'B'}, {'ℭ', 'C'},
	{'ℯ', 'e'}, {'ℰ', 'E'}, {'ℱ', 'F'}, {'ℳ', 'M'}, {'ℴ', 'o'},
	{'ᴀ', 'a'}, {'ʙ', 'b'}, {'ᴄ', 'c'}, {'ᴅ', 'd'}, {'ᴇ', 'e'},
	{'ꜰ', 'f'}, {'ɢ', 'g'}, {'ʜ', 'h'}, {'ɪ', 'i'}, {'ᴊ', 'j'},
	{'ᴋ', 'k'}, {'ʟ', 'l'}, {'ᴍ', 'm'}, {'ɴ', 'n'}, {'ᴏ', 'o'},
	{'ᴘ', 'p'}, {'ꞯ', 'q'}, {'ʀ', 'r'}, {'ꜱ', 's'}, {'ᴛ', 't'},
	{'ᴜ', 'u'}, {'ᴠ', 'v'}, {'ᴡ', 'w'}, {'ʏ', 'y'}, {'ᴢ', 'z'},
	{'ᵃ', 'a'}, {'ᵇ', 'b'}, {'ᶜ', 'c'}, {'ᵈ', 'd'}, {'ᵉ', 'e'},
	{'ᶠ', 'f'}, {'ᵍ', 'g'}, {'ʰ', 'h'}, {'ⁱ', 'i'}, {'ʲ', 'j'},
	{'ᵏ', 'k'}, {'ˡ', 'l'}, {'ᵐ', 'm'}, {'ⁿ', 'n'}, {'ᵒ', 'o'},
	{'ᵖ', 'p'}, {'ʳ', 'r'}, {'ˢ', 's'}, {'ᵗ', 't'}, {'ᵘ', 'u'},
	{'ᵛ', 'v'}, {'ʷ', 'w'}, {'ˣ', 'x'}, {'ʸ', 'y'}, {'ᶻ', 'z'},
	{'ₐ', 'a'}, {'ₑ', 'e'}, {'ₒ', 'o'}, {'ₓ', 'x'}, {'ₕ', 'h'},
	{'ₖ', 'k'}, {'ₗ', 'l'}, {'ₘ', 'm'}, {'ₙ', 'n'}, {'ₚ', 'p'},
	{'ₛ', 's'}, {'ₜ', 't'},
}

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
		for _, word := range grammarWords(field) {
			class, banned := grammarTerms[word]
			if !banned {
				class, banned = grammarContractions[word]
			}
			if banned {
				found.add(num, RuleGrammar, fmt.Sprintf("%s %q", class, word))
			}
		}
	}
}

func protectedGrammarToken(field string) bool {
	token := field
	for depth := 0; depth <= maxLiteralWrapperDepth; depth++ {
		if protectedLiteralToken(token) {
			return true
		}
		if depth == maxLiteralWrapperDepth {
			break
		}
		next := trimLiteralWrapper(token)
		if next == token {
			return false
		}
		token = next
	}
	return false
}

func protectedLiteralToken(token string) bool {
	if msvcDefineRe.MatchString(token) || shortTechnicalFlag(token) {
		return true
	}
	if completeHTTPURL(token) || protectedPath(token) || literalMailRe.MatchString(token) {
		return true
	}
	if !literalFlagRe.MatchString(token) {
		return false
	}
	name := strings.TrimLeft(token, "-")
	name, _, _ = strings.Cut(name, "=")
	word := collapsedGrammarWord(name)
	_, banned := grammarTerms[word]
	if !banned {
		_, banned = grammarContractions[word]
	}
	return !banned
}

func trimLiteralWrapper(token string) string {
	if token == "" {
		return token
	}
	first, firstSize := utf8.DecodeRuneInString(token)
	last, lastSize := utf8.DecodeLastRuneInString(token)
	if literalWrapperPair(first, last) {
		return token[firstSize : len(token)-lastSize]
	}
	if strings.ContainsRune(".!?,;)]}>\"'”’", last) {
		return token[:len(token)-lastSize]
	}
	if strings.ContainsRune(",;\"'“‘", first) {
		return token[firstSize:]
	}
	return token
}

func literalWrapperPair(first, last rune) bool {
	switch first {
	case '(':
		return last == ')'
	case '[':
		return last == ']'
	case '{':
		return last == '}'
	case '<':
		return last == '>'
	case '\'', '"':
		return last == first
	case '‘':
		return last == '’'
	case '“':
		return last == '”'
	default:
		return false
	}
}

func shortTechnicalFlag(token string) bool {
	if len(token) < 2 || token[0] != '-' && token[0] != '/' {
		return false
	}
	if token[1] < 'A' || token[1] > 'Z' && token[1] < 'a' || token[1] > 'z' {
		return false
	}
	if len(token) == 2 {
		return true
	}
	value := strings.TrimPrefix(token[2:], "=")
	return value != "" && protectedPath(value)
}

func completeHTTPURL(token string) bool {
	if strings.ContainsFunc(token, unicode.IsSpace) || strings.ContainsFunc(token, unicode.IsControl) {
		return false
	}
	parsed, err := url.Parse(token)
	if err != nil || parsed.Host == "" || parsed.Hostname() == "" {
		return false
	}
	return strings.EqualFold(parsed.Scheme, "http") || strings.EqualFold(parsed.Scheme, "https")
}

func protectedPath(token string) bool {
	path := diagnosticRe.ReplaceAllString(token, "")
	if path == "" || strings.ContainsFunc(path, unicode.IsSpace) || strings.ContainsFunc(path, unicode.IsControl) {
		return false
	}
	if windowsDrivePath(path) || strings.HasPrefix(path, `\\`) {
		return protectedAbsoluteWindowsPath(path)
	}
	if strings.Contains(path, `\`) {
		return protectedRelativePath(strings.ReplaceAll(path, `\`, "/"))
	}
	return protectedSlashPath(path)
}

func grammarWord(field string) string {
	field = compatibilityFold(field)
	word := strings.Trim(field, "()[]{}<>,;\"")
	if strings.HasPrefix(word, "-") {
		word = strings.TrimLeft(word, "-")
		word, _, _ = strings.Cut(word, "=")
	}
	word = strings.ToLower(strings.TrimFunc(word, notLetter))
	return word
}

func grammarWords(field string) []string {
	field = compatibilityFold(field)
	words := []string{grammarWord(field)}
	if _, banned := grammarTerms[words[0]]; banned {
		return words
	}
	if _, banned := grammarContractions[words[0]]; banned {
		return words
	}
	joined := strings.Map(func(char rune) rune {
		if grammarSeparator(char) || unicode.IsMark(char) {
			return -1
		}
		return char
	}, field)
	if word := grammarWord(joined); word != "" && word != words[0] {
		words = append(words, word)
	}
	parts := strings.FieldsFunc(field, grammarSeparator)
	for _, part := range parts {
		word := grammarWord(part)
		if word != "" && word != words[0] {
			words = append(words, word)
		}
	}
	return words
}

func grammarSeparator(char rune) bool {
	char = compatibilityRune(char)
	digit := char >= '0' && char <= '9'
	upper := char >= 'A' && char <= 'Z'
	lower := char >= 'a' && char <= 'z'
	if char >= '!' && char <= '~' && !digit && !upper && !lower {
		return true
	}
	return char > unicode.MaxASCII && (unicode.IsPunct(char) || unicode.IsSymbol(char))
}

// collapsedGrammarWord exposes punctuation-, apostrophe-, mark- and compatibility-
// segmented text to exact term lookup. Literal classification runs on source spelling
// first, so compatibility punctuation cannot manufacture a protected path or mail token.
func collapsedGrammarWord(text string) string {
	return strings.ToLower(strings.Map(func(char rune) rune {
		if unicode.IsLetter(char) || unicode.IsDigit(char) {
			return char
		}
		return -1
	}, compatibilityFold(text)))
}

func protectedSlashPath(token string) bool {
	if !strings.Contains(token, "/") {
		return false
	}
	if strings.HasPrefix(token, "/") || strings.HasPrefix(token, "./") || strings.HasPrefix(token, "../") || strings.HasPrefix(token, "~/") {
		segments, ok := validLiteralPathSegments(token)
		return ok && segments >= 2
	}
	return protectedRelativePath(token)
}

func protectedRelativePath(path string) bool {
	segments, ok := validLiteralPathSegments(path)
	if !ok || segments < 2 {
		return false
	}
	base := pathBase(path)
	return strings.HasSuffix(path, "/") || strings.Contains(base, ".")
}

func protectedAbsoluteWindowsPath(path string) bool {
	normalized := strings.ReplaceAll(path, `\`, "/")
	minimum := 2
	if windowsDrivePath(path) {
		normalized = normalized[3:]
		minimum = 1
	} else {
		normalized = strings.TrimPrefix(normalized, "//")
	}
	segments, ok := validLiteralPathSegments(normalized)
	return ok && segments >= minimum
}

func windowsDrivePath(path string) bool {
	return len(path) >= 3 && path[1] == ':' && (path[2] == '/' || path[2] == '\\') &&
		(path[0] >= 'A' && path[0] <= 'Z' || path[0] >= 'a' && path[0] <= 'z')
}

func validLiteralPathSegments(path string) (int, bool) {
	core := strings.Trim(path, "/")
	if core == "" || strings.Contains(core, "//") {
		return 0, false
	}
	parts := strings.Split(core, "/")
	if len(parts) > maxLiteralPathSegments {
		return 0, false
	}
	for _, part := range parts {
		if !validLiteralPathSegment(part) {
			return 0, false
		}
	}
	return len(parts), true
}

func validLiteralPathSegment(segment string) bool {
	if segment == "." || segment == ".." {
		return true
	}
	if segment == "" {
		return false
	}
	for _, char := range segment {
		if unicode.IsLetter(char) || unicode.IsDigit(char) || unicode.IsMark(char) || strings.ContainsRune("_~+().@-$", char) {
			continue
		}
		return false
	}
	return true
}

func pathBase(path string) string {
	trimmed := strings.TrimSuffix(path, "/")
	if index := strings.LastIndexByte(trimmed, '/'); index >= 0 {
		return trimmed[index+1:]
	}
	return trimmed
}

// compatibilityFold performs bounded adversarial mappings needed by grammar matching
// without importing a normalization library. Literal recognition always sees source
// spelling first, so this fold cannot turn pseudo-path or pseudo-mail punctuation into a
// protected token.
func compatibilityFold(text string) string {
	return strings.Map(compatibilityRune, text)
}

func compatibilityRune(char rune) rune {
	if folded, ok := compatibilityLatinLetter(char); ok {
		return folded
	}
	switch {
	case char >= 0xFF01 && char <= 0xFF5E:
		return char - 0xFEE0
	case char == 0x3000:
		return ' '
	case strings.ContainsRune("‘’ʼʻʹˊˋꞌ", char):
		return '\''
	default:
		return char
	}
}

func compatibilityLatinLetter(char rune) (rune, bool) {
	for index := 0; index < len(compatibilityLetterSpans); index++ {
		span := compatibilityLetterSpans[index]
		if char >= span.first && char <= span.last {
			return span.base + char - span.first, true
		}
	}
	return mappedCompatibilityLatinLetter(char)
}

func mappedCompatibilityLatinLetter(char rune) (rune, bool) {
	for index := 0; index < len(compatibilityLatinLetters); index++ {
		pair := compatibilityLatinLetters[index]
		if char == pair.source {
			return pair.target, true
		}
	}
	return char, false
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
