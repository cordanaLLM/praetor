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

var (
	// compiledPatterns holds deterministic, pre-compiled neutralization patterns.
	compiledPatterns = []InjectionPattern{
		// XML / Markdown Role Delimiters
		{
			Name:        "role_system_open",
			Regex:       regexp.MustCompile(`(?i)<\s*system\s*>`),
			Replacement: "[neutralized:system]",
		},
		{
			Name:        "role_system_close",
			Regex:       regexp.MustCompile(`(?i)<\s*/\s*system\s*>`),
			Replacement: "[neutralized:/system]",
		},
		{
			Name:        "role_user_open",
			Regex:       regexp.MustCompile(`(?i)<\s*user\s*>`),
			Replacement: "[neutralized:user]",
		},
		{
			Name:        "role_user_close",
			Regex:       regexp.MustCompile(`(?i)<\s*/\s*user\s*>`),
			Replacement: "[neutralized:/user]",
		},
		{
			Name:        "role_assistant_open",
			Regex:       regexp.MustCompile(`(?i)<\s*assistant\s*>`),
			Replacement: "[neutralized:assistant]",
		},
		{
			Name:        "role_assistant_close",
			Regex:       regexp.MustCompile(`(?i)<\s*/\s*assistant\s*>`),
			Replacement: "[neutralized:/assistant]",
		},

		// Special Model Delimiters
		{
			Name:        "im_start",
			Regex:       regexp.MustCompile(`(?i)<\s*\|\s*im_start\s*\|\s*>`),
			Replacement: "[neutralized:im_start]",
		},
		{
			Name:        "im_end",
			Regex:       regexp.MustCompile(`(?i)<\s*\|\s*im_end\s*\|\s*>`),
			Replacement: "[neutralized:im_end]",
		},
		{
			Name:        "pipe_system",
			Regex:       regexp.MustCompile(`(?i)<\s*\|\s*system\s*\|\s*>`),
			Replacement: "[neutralized:pipe_system]",
		},
		{
			Name:        "inst_open",
			Regex:       regexp.MustCompile(`(?i)\[\s*INST\s*\]`),
			Replacement: "[neutralized:INST]",
		},
		{
			Name:        "inst_close",
			Regex:       regexp.MustCompile(`(?i)\[\s*/\s*INST\s*\]`),
			Replacement: "[neutralized:/INST]",
		},
		{
			Name:        "sys_open",
			Regex:       regexp.MustCompile(`(?i)<<\s*SYS\s*>>`),
			Replacement: "[neutralized:SYS]",
		},
		{
			Name:        "sys_close",
			Regex:       regexp.MustCompile(`(?i)<<\s*/\s*SYS\s*>>`),
			Replacement: "[neutralized:/SYS]",
		},

		// Adversarial Override Phrases
		{
			Name:        "ignore_previous_instructions",
			Regex:       regexp.MustCompile(`(?i)\bignore\s+(?:all\s+)?previous\s+instructions\b`),
			Replacement: "[neutralized-phrase:ignore-previous-instructions]",
		},
		{
			Name:        "disregard_previous_instructions",
			Regex:       regexp.MustCompile(`(?i)\bdisregard\s+(?:all\s+)?previous\s+instructions\b`),
			Replacement: "[neutralized-phrase:disregard-previous-instructions]",
		},
		{
			Name:        "forget_previous_instructions",
			Regex:       regexp.MustCompile(`(?i)\bforget\s+(?:all\s+)?previous\s+instructions\b`),
			Replacement: "[neutralized-phrase:forget-previous-instructions]",
		},
		{
			Name:        "system_prompt_override",
			Regex:       regexp.MustCompile(`(?i)\bsystem\s+prompt\s+override\b`),
			Replacement: "[neutralized-phrase:system-prompt-override]",
		},
		{
			Name:        "override_system_prompt",
			Regex:       regexp.MustCompile(`(?i)\boverride\s+(?:the\s+)?system\s+prompt\b`),
			Replacement: "[neutralized-phrase:override-system-prompt]",
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

// NormalizeWhitespace normalizes repeated whitespace into single spaces for clean processing.
func NormalizeWhitespace(s string) string {
	return strings.Join(strings.Fields(s), " ")
}
