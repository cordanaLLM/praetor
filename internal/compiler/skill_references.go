// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package compiler

import (
	"fmt"
	"path"
	"regexp"
	"strings"
	"unicode"

	"github.com/cordanaLLM/praetor/internal/agentcontext"
	"github.com/cordanaLLM/praetor/internal/config"
	"github.com/cordanaLLM/praetor/internal/util"
)

// maxSkillReferenceTokens bounds the tokens, code spans and link destinations one skill text
// may hold before CheckShippedSkillReferences refuses it (HISS-02). Text past the bound fails
// closed: a bound that skipped the rest would pass whatever sits behind it.
const maxSkillReferenceTokens = 16384

var (
	skillURLRe = regexp.MustCompile("(?i)https?://[^\\s<>\"'`*,;()|\\[\\]]+")
	// skillBackslashRe finds backslashes between two path characters, which reads as a slash:
	// docs\credits.md or docs\\credits.md is the path docs/credits.md.
	skillBackslashRe   = regexp.MustCompile(`([A-Za-z0-9_.-])\\+([A-Za-z0-9_.-])`)
	skillMarkdownRefRe = regexp.MustCompile(`(?m)^\s*\[[^\]]+\]:\s*(\S+)`)
	skillHTMLLinkRe    = regexp.MustCompile(`(?i)<a\s+[^>]*href=["']([^"']+)["']`)
	skillHTMLImageRe   = regexp.MustCompile(`(?i)<img\s+[^>]*src=["']([^"']+)["']`)
	markdownEscapeRe   = regexp.MustCompile("\\\\([_.*[\\]()#+\\-!~`])")
	htmlTagRe          = regexp.MustCompile(`(?i)</?[a-z][a-z0-9_-]*(\s+[^>]*)?/?>`)
)

// CheckShippedSkillReferences refuses the text of a skill Praetor ships when it names a
// repository path an adopter does not receive. It is a check on the source bundle, run where the
// defect starts (adoption reads the bundle, and Praetor's own tests read its tree): it is not a
// gate of compile-context or audit, which must never fail on the copy an adopter already holds.
//
// It protects by default. The whole text is tokenized, code spans and plain text alike, and
// every token that has a path shape (a slash and a letter or digit, after the wrapper
// punctuation, emphasis marks and a sentence-final dot are stripped) must be an absolute URL or
// one of the files adoption writes for the shipped skills (shippedSkillTargets). Link
// destinations are checked as well, bare file names included. Naming the cases that are
// unsafe would never converge with the ways a path can be written; naming the safe ones does.
func CheckShippedSkillReferences(skillName string, data []byte) error {
	text := strings.ReplaceAll(string(data), "\r\n", "\n")
	text = strings.ReplaceAll(text, "&#47;", "/")
	text = strings.ReplaceAll(text, "&sol;", "/")
	targets, err := shippedSkillTargets()
	if err != nil {
		return err
	}
	tokens, err := skillPathTokens(skillName, text)
	if err != nil {
		return err
	}
	for i := 0; i < len(tokens) && i < maxSkillReferenceTokens; i++ {
		if !targets[path.Clean(tokens[i])] {
			return unshippedReference(skillName, tokens[i])
		}
	}
	return checkLinkDestinations(skillName, text, targets)
}

// unshippedReference is the refusal for a path the adopter does not receive.
func unshippedReference(skillName, target string) error {
	return fmt.Errorf("skill %s references repository path %q, which an adopter does not receive", skillName, target)
}

// shippedSkillTargets are the clean slash paths a shipped skill may name: the skill
// directories and the SKILL.md and LICENSE files adoption writes for each skill of the bundle,
// in .agents/skills and in every client skill directory, and the same files as a sibling skill
// names them (../<skill>/SKILL.md), and the two files of the skill's own directory, which
// ./SKILL.md and ./LICENSE name once cleaned.
func shippedSkillTargets() (map[string]bool, error) {
	clientDirs, _, err := agentcontext.SkillDirs(nil)
	if err != nil {
		return nil, fmt.Errorf("read the client skill directories: %w", err)
	}
	dirs := append([]string{CanonicalSkillsRel}, clientDirs...)
	targets := map[string]bool{SkillEntryName: true, SkillLicenseName: true}
	for i := 0; i < len(dirs) && i < maxSkillProjections; i++ {
		targets[dirs[i]] = true
	}
	bundle := config.RegisterSkillBundle()
	for i := 0; i < len(bundle) && i < maxSkillProjections; i++ {
		for _, base := range append([]string{".."}, dirs...) {
			targets[base+"/"+bundle[i]] = true
			targets[base+"/"+bundle[i]+"/"+SkillEntryName] = true
			targets[base+"/"+bundle[i]+"/"+SkillLicenseName] = true
		}
	}
	return targets, nil
}

// skillPathTokens returns the path-shaped tokens of text, cleaned of the wrapper punctuation
// around them. Code span delimiters are dropped first (util.MarkdownCodeSpans, the reader every
// other Markdown check uses), absolute URLs are set aside, and a backslash reads as a slash. More
// than maxSkillReferenceTokens tokens is an error.
func skillPathTokens(skillName, text string) ([]string, error) {
	flat, err := withoutCodeSpanDelimiters(skillName, text)
	if err != nil {
		return nil, err
	}
	flat = htmlTagRe.ReplaceAllString(flat, " ")
	flat = skillURLRe.ReplaceAllString(flat, " ")
	flat = markdownEscapeRe.ReplaceAllString(flat, "$1")
	flat = skillBackslashRe.ReplaceAllString(flat, "$1/$2")
	fields := strings.FieldsFunc(flat, isSkillSeparator)
	if len(fields) > maxSkillReferenceTokens {
		return nil, fmt.Errorf("skill %s holds more than %d tokens to check", skillName, maxSkillReferenceTokens)
	}
	var tokens []string
	for i := 0; i < len(fields) && i < maxSkillReferenceTokens; i++ {
		token := strings.TrimRight(strings.Trim(fields[i], "_"), ".")
		if strings.Contains(token, "/") && strings.IndexFunc(token, isLetterOrDigit) >= 0 {
			if !isNonPathToken(token) {
				tokens = append(tokens, token)
			}
		}
	}
	return tokens, nil
}

// nonPathProse are common abbreviations, alternatives and acronyms that use a slash but are
// not repository paths.
var nonPathProse = map[string]bool{
	"and/or":     true,
	"i/o":        true,
	"ci/cd":      true,
	"pass/fail":  true,
	"true/false": true,
	"either/or":  true,
	"yes/no":     true,
	"on/off":     true,
	"read/write": true,
	"in/out":     true,
}

// isNonPathToken reports whether token is standard non-path prose: a common slash word,
// a fraction or date, or a unit.
func isNonPathToken(token string) bool {
	lower := strings.ToLower(token)
	if nonPathProse[lower] || isNumericSlash(token) {
		return true
	}
	if strings.HasSuffix(lower, "/s") || strings.HasSuffix(lower, "/sec") || strings.HasSuffix(lower, "/min") || strings.HasSuffix(lower, "/hr") {
		idx := strings.Index(token, "/")
		if isAllLetters(token[:idx]) {
			return true
		}
	}
	return false
}

// isNumericSlash reports whether s consists solely of digits and slashes with at least one digit.
func isNumericSlash(s string) bool {
	hasDigit := false
	for _, r := range s {
		if unicode.IsDigit(r) {
			hasDigit = true
		} else if r != '/' {
			return false
		}
	}
	return hasDigit
}

// isAllLetters reports whether s is non-empty and consists solely of unicode letters.
func isAllLetters(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if !unicode.IsLetter(r) {
			return false
		}
	}
	return true
}

// withoutCodeSpanDelimiters returns text with every code span replaced by its content, so a
// span of any backtick run length reads as plain words.
func withoutCodeSpanDelimiters(skillName, text string) (string, error) {
	spans := util.MarkdownCodeSpans(text, maxSkillReferenceTokens)
	if len(spans) >= maxSkillReferenceTokens {
		return "", fmt.Errorf("skill %s holds more than %d code spans to check", skillName, maxSkillReferenceTokens)
	}
	var flat strings.Builder
	last := 0
	for i := 0; i < len(spans) && i < maxSkillReferenceTokens; i++ {
		flat.WriteString(text[last:spans[i].Start])
		flat.WriteString(" " + spans[i].Content(text) + " ")
		last = spans[i].End
	}
	flat.WriteString(text[last:])
	return flat.String(), nil
}

// isSkillSeparator reports whether r ends a token: white space, and the brackets, quotes,
// emphasis marks and punctuation that wrap a path in Markdown or prose. A colon separates too,
// so Credit:docs/credits.md is two tokens (absolute URLs are set aside before this runs).
func isSkillSeparator(r rune) bool {
	return unicode.IsSpace(r) || strings.ContainsRune("()[]<>{}\"'`*,;|=!?:~#", r)
}

func isLetterOrDigit(r rune) bool {
	return unicode.IsLetter(r) || unicode.IsDigit(r)
}

// checkLinkDestinations checks the destination of every Markdown link, reference definition and
// HTML anchor or image in text, which unlike a prose token may be a bare file name.
func checkLinkDestinations(skillName, text string, targets map[string]bool) error {
	mdMatches := util.MarkdownLinkRe.FindAllStringSubmatch(text, maxSkillReferenceTokens+1)
	if len(mdMatches) > maxSkillReferenceTokens {
		return fmt.Errorf("skill %s holds more than %d links to check", skillName, maxSkillReferenceTokens)
	}
	for i := 0; i < len(mdMatches); i++ {
		if !linkDestinationShipped(mdMatches[i][2], targets) {
			return unshippedReference(skillName, strings.TrimSpace(mdMatches[i][2]))
		}
	}
	for _, re := range []*regexp.Regexp{skillMarkdownRefRe, skillHTMLLinkRe, skillHTMLImageRe} {
		matches := re.FindAllStringSubmatch(text, maxSkillReferenceTokens+1)
		if len(matches) > maxSkillReferenceTokens {
			return fmt.Errorf("skill %s holds more than %d links to check", skillName, maxSkillReferenceTokens)
		}
		for i := 0; i < len(matches); i++ {
			if !linkDestinationShipped(matches[i][1], targets) {
				return unshippedReference(skillName, strings.TrimSpace(matches[i][1]))
			}
		}
	}
	return nil
}

// linkDestinationShipped reports whether one link destination is an anchor, an absolute URL, a
// mail address or a shipped file. Its title, angle brackets and fragment are not part of the
// path.
func linkDestinationShipped(raw string, targets map[string]bool) bool {
	fields := strings.Fields(strings.Trim(strings.TrimSpace(raw), "<>"))
	if len(fields) == 0 {
		return true
	}
	target, _, _ := strings.Cut(strings.Trim(fields[0], "<>"), "#")
	lower := strings.ToLower(target)
	if target == "" || strings.HasPrefix(lower, "mailto:") || strings.HasPrefix(lower, "https://") || strings.HasPrefix(lower, "http://") {
		return true
	}
	clean := path.Clean(strings.ReplaceAll(target, `\`, "/"))
	return targets[clean] || clean == SkillEntryName || clean == SkillLicenseName
}
