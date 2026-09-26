package lockdown

import (
	"regexp"
	"strings"
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

// NeutralizeInjection scans the input for known prompt-injection delimiters and phrases,
// replaces them with inert neutralization markers, and returns the neutralized text
// alongside a list of detected pattern names.
func NeutralizeInjection(input string) (string, []string) {
	if input == "" {
		return "", nil
	}

	result := input
	detected := make([]string, 0, len(compiledPatterns))

	limit := len(compiledPatterns)
	for i := 0; i < limit; i++ {
		p := compiledPatterns[i]
		if p.Regex.MatchString(result) {
			detected = append(detected, p.Name)
			result = p.Regex.ReplaceAllString(result, p.Replacement)
		}
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
	limit := len(compiledPatterns)
	for i := 0; i < limit; i++ {
		if compiledPatterns[i].Regex.MatchString(input) {
			return true
		}
	}
	return false
}
