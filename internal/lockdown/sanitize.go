package lockdown

import (
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

// InjectionPattern defines a pattern and its neutralized replacement token.
type InjectionPattern struct {
	Name        string
	Regex       *regexp.Regexp
	Replacement string
}

// Most MCP tool results are marshalled JSON, so text taken from a transcript or document
// arrives JSON-escaped: encoding/json writes angle brackets as u003c / u003e unicode
// escapes and a newline as backslash-n. Matching only the literal forms would let every
// JSON-shaped result carry delimiters and override phrases past the neutralizer, so each
// fragment below also accepts the escaped form.
const (
	// lt and gt match an angle bracket, literal or unicode-escaped.
	lt = `(?:<|\\u003c)`
	gt = `(?:>|\\u003e)`
	// ws matches one whitespace character, literal or escaped (\n, \r, \t).
	ws = `(?:\s|\\[nrt])`
	// wb starts a phrase at a word boundary or directly after an escaped whitespace, where
	// \b alone sees no boundary ("line\nIgnore"). It captures the escape so the replacement
	// ("${1}...") keeps it and the JSON text stays intact.
	wb = `(\\[nrt]|\b)`
)

// delimiter compiles a case-insensitive model delimiter whose tokens may be separated by
// optional whitespace.
func delimiter(tokens ...string) *regexp.Regexp {
	return regexp.MustCompile(`(?i)` + strings.Join(tokens, ws+`*`))
}

// phrase compiles a case-insensitive override phrase whose words are separated by
// required whitespace; group 1 holds the escape wb may have consumed.
func phrase(words ...string) *regexp.Regexp {
	return regexp.MustCompile(`(?i)` + wb + strings.Join(words, ws+`+`) + `\b`)
}

var (
	// compiledPatterns holds deterministic, pre-compiled neutralization patterns.
	compiledPatterns = []InjectionPattern{
		// XML / Markdown Role Delimiters
		{Name: "role_system_open", Regex: delimiter(lt, "system", gt), Replacement: "[neutralized:system]"},
		{Name: "role_system_close", Regex: delimiter(lt, "/", "system", gt), Replacement: "[neutralized:/system]"},
		{Name: "role_user_open", Regex: delimiter(lt, "user", gt), Replacement: "[neutralized:user]"},
		{Name: "role_user_close", Regex: delimiter(lt, "/", "user", gt), Replacement: "[neutralized:/user]"},
		{Name: "role_assistant_open", Regex: delimiter(lt, "assistant", gt), Replacement: "[neutralized:assistant]"},
		{Name: "role_assistant_close", Regex: delimiter(lt, "/", "assistant", gt), Replacement: "[neutralized:/assistant]"},

		// Special Model Delimiters
		{Name: "im_start", Regex: delimiter(lt, `\|`, "im_start", `\|`, gt), Replacement: "[neutralized:im_start]"},
		{Name: "im_end", Regex: delimiter(lt, `\|`, "im_end", `\|`, gt), Replacement: "[neutralized:im_end]"},
		{Name: "pipe_system", Regex: delimiter(lt, `\|`, "system", `\|`, gt), Replacement: "[neutralized:pipe_system]"},
		{Name: "inst_open", Regex: delimiter(`\[`, "INST", `\]`), Replacement: "[neutralized:INST]"},
		{Name: "inst_close", Regex: delimiter(`\[`, "/", "INST", `\]`), Replacement: "[neutralized:/INST]"},
		{Name: "sys_open", Regex: delimiter(lt+lt, "SYS", gt+gt), Replacement: "[neutralized:SYS]"},
		{Name: "sys_close", Regex: delimiter(lt+lt, "/", "SYS", gt+gt), Replacement: "[neutralized:/SYS]"},

		// Adversarial Override Phrases
		{
			Name:        "ignore_previous_instructions",
			Regex:       phrase("ignore", `(?:all`+ws+`+)?previous`, "instructions"),
			Replacement: "${1}[neutralized-phrase:ignore-previous-instructions]",
		},
		{
			Name:        "disregard_previous_instructions",
			Regex:       phrase("disregard", `(?:all`+ws+`+)?previous`, "instructions"),
			Replacement: "${1}[neutralized-phrase:disregard-previous-instructions]",
		},
		{
			Name:        "forget_previous_instructions",
			Regex:       phrase("forget", `(?:all`+ws+`+)?previous`, "instructions"),
			Replacement: "${1}[neutralized-phrase:forget-previous-instructions]",
		},
		{
			Name:        "system_prompt_override",
			Regex:       phrase("system", "prompt", "override"),
			Replacement: "${1}[neutralized-phrase:system-prompt-override]",
		},
		{
			Name:        "override_system_prompt",
			Regex:       phrase("override", `(?:the`+ws+`+)?system`, "prompt"),
			Replacement: "${1}[neutralized-phrase:override-system-prompt]",
		},
	}
)

// confusables folds the non-ASCII letters and delimiters that render like the ASCII the
// patterns spell. It is deliberately a bounded table rather than a Unicode dependency: the
// Cyrillic and Greek homoglyphs of the Latin letters, dotless i and long s, and the angle
// bracket, pipe and slash look-alikes. Fullwidth ASCII is folded by rule in foldRune.
var confusables = map[rune]rune{
	// Cyrillic lower case
	'\u0430': 'a', '\u0441': 'c', '\u0501': 'd', '\u0435': 'e', '\u04BB': 'h', '\u0456': 'i',
	'\u0458': 'j', '\u043E': 'o', '\u0440': 'p',
	'\u051B': 'q', '\u0455': 's', '\u0475': 'v', '\u051D': 'w', '\u0445': 'x', '\u0443': 'y',
	// Cyrillic upper case
	'\u0410': 'A', '\u0412': 'B', '\u0421': 'C', '\u0415': 'E', '\u041D': 'H', '\u0406': 'I',
	'\u0408': 'J', '\u041A': 'K', '\u041C': 'M',
	'\u041E': 'O', '\u0420': 'P', '\u0405': 'S', '\u0422': 'T', '\u0425': 'X', '\u0423': 'Y',
	// Greek
	'\u03B1': 'a', '\u03B9': 'i', '\u03BD': 'v', '\u03BF': 'o', '\u03C1': 'p',
	'\u0391': 'A', '\u0392': 'B', '\u0395': 'E', '\u0396': 'Z', '\u0397': 'H', '\u0399': 'I',
	'\u039A': 'K', '\u039C': 'M', '\u039D': 'N',
	'\u039F': 'O', '\u03A1': 'P', '\u03A4': 'T', '\u03A5': 'Y', '\u03A7': 'X',
	// Latin look-alikes
	'\u0131': 'i', '\u017F': 's', '\u0261': 'g',
	// Delimiters
	'\u2039': '<', '\u203A': '>', '\u2329': '<', '\u232A': '>', '\u3008': '<', '\u3009': '>',
	'\u27E8': '<', '\u27E9': '>', '\uFE64': '<', '\uFE65': '>', '\u02C2': '<', '\u02C3': '>',
	'\u2223': '|', '\u01C0': '|', '\u2502': '|', '\u2215': '/', '\u2044': '/',
}

// foldRune maps one rune to what the patterns compare against. Format characters (zero-width
// spaces and joiners, the byte-order mark, bidirectional controls, the soft hyphen) and
// combining marks are dropped, so they can no longer split a delimiter; every Unicode space
// becomes an ASCII space, which is all the patterns' \s matches; fullwidth ASCII and the
// confusables table fold to the ASCII they imitate.
func foldRune(r rune) (rune, bool) {
	switch {
	case r < utf8.RuneSelf:
		return r, true
	case unicode.Is(unicode.Cf, r) || unicode.Is(unicode.Mn, r):
		return 0, false
	case unicode.IsSpace(r):
		return ' ', true
	case r >= 0xFF01 && r <= 0xFF5E:
		return r - 0xFEE0, true
	}
	if ascii, ok := confusables[r]; ok {
		return ascii, true
	}
	return r, true
}

// matchView is the input folded for matching, with the way back to the input's byte offsets.
// Matching runs on the view and replacement on the input, so a finding is neutralized while
// every character outside it, non-Latin prose included, is returned exactly as it came in.
type matchView struct {
	text string
	// origin[i] is the input offset of the rune that produced byte i of text, and
	// origin[len(text)] is len(input). It is nil for ASCII input, where the view is the input.
	origin []int
}

func newMatchView(input string) matchView {
	if isASCII(input) {
		return matchView{text: input}
	}
	var b strings.Builder
	b.Grow(len(input))
	origin := make([]int, 0, len(input)+1)
	for offset, r := range input {
		folded, keep := foldRune(r)
		if !keep {
			continue
		}
		before := b.Len()
		b.WriteRune(folded)
		for i := before; i < b.Len(); i++ {
			origin = append(origin, offset)
		}
	}
	return matchView{text: b.String(), origin: append(origin, len(input))}
}

func isASCII(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] >= utf8.RuneSelf {
			return false
		}
	}
	return true
}

// inputOffset maps a byte offset in the view to the input.
func (v matchView) inputOffset(i int) int {
	if v.origin == nil {
		return i
	}
	return v.origin[i]
}

// replace substitutes the expanded replacement for the input span behind each match in the
// view. A match holds the view offsets of the whole match and of each group; they are mapped
// back to the input, so a ${1} in the replacement (the escape a phrase pattern consumed)
// carries the input's own bytes. A span ends where the next kept rune starts, so a format
// character trailing a match goes with it.
func (v matchView) replace(input string, re *regexp.Regexp, matches [][]int, replacement string) string {
	b := make([]byte, 0, len(input))
	last := 0
	for i := 0; i < len(matches); i++ {
		mapped := v.inputOffsets(matches[i])
		b = append(b, input[last:mapped[0]]...)
		b = re.ExpandString(b, replacement, input, mapped)
		last = mapped[1]
	}
	return string(append(b, input[last:]...))
}

// inputOffsets maps a match's view offsets to the input, keeping -1 for a group that took no
// part in the match.
func (v matchView) inputOffsets(match []int) []int {
	mapped := make([]int, len(match))
	for i, offset := range match {
		mapped[i] = offset
		if offset >= 0 {
			mapped[i] = v.inputOffset(offset)
		}
	}
	return mapped
}

// NeutralizeInjection scans the input for known prompt-injection delimiters and phrases,
// replaces them with inert neutralization markers, and returns the neutralized text
// alongside a list of detected pattern names.
//
// The patterns are ASCII literals, and they used to be matched against the raw input, so a
// Cyrillic s (U+0455) in <system>, a zero-width space inside <system>, fullwidth angle
// brackets (U+FF1C, U+FF1E) around system, or a no-break space in "ignore previous
// instructions" passed every one of them. Matching now runs on a folded view of the input
// (see foldRune); the view is rebuilt only after a pattern actually replaces something, so
// the work stays linear in the input per pattern.
func NeutralizeInjection(input string) (string, []string) {
	if input == "" {
		return "", nil
	}

	result := input
	detected := make([]string, 0, len(compiledPatterns))
	view := newMatchView(result)

	limit := len(compiledPatterns)
	for i := 0; i < limit; i++ {
		p := compiledPatterns[i]
		matches := p.Regex.FindAllStringSubmatchIndex(view.text, -1)
		if len(matches) == 0 {
			continue
		}
		detected = append(detected, p.Name)
		result = view.replace(result, p.Regex, matches, p.Replacement)
		view = newMatchView(result)
	}

	return result, detected
}

// SanitizePrompt applies prompt injection neutralization and returns the safe text.
func SanitizePrompt(input string) string {
	sanitized, _ := NeutralizeInjection(input)
	return sanitized
}

// HasInjection checks if the input contains any prohibited prompt-injection delimiters or phrases.
func HasInjection(input string) bool {
	if input == "" {
		return false
	}
	view := newMatchView(input)
	limit := len(compiledPatterns)
	for i := 0; i < limit; i++ {
		if compiledPatterns[i].Regex.MatchString(view.text) {
			return true
		}
	}
	return false
}
