// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package compiler

import (
	"fmt"
	"html"
	"regexp"
	"strings"
	"unicode"

	"github.com/cordanaLLM/praetor/internal/agentcontext"
	"github.com/cordanaLLM/praetor/internal/config"
	"github.com/cordanaLLM/praetor/internal/util"
)

// maxSkillReferenceTokens bounds the tokens and link destinations one skill text may hold before
// CheckShippedSkillReferences refuses it (HISS-02). Text past the bound fails closed: a bound
// that skipped the rest would pass whatever sits behind it.
const maxSkillReferenceTokens = 16384

var (
	// skillAbsoluteURLRe is an absolute URL: a scheme, "://" and something after it.
	skillAbsoluteURLRe = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9+.-]*://\S+$`)
	// skillMarkdownRefRe finds a Markdown reference definition and captures its destination.
	skillMarkdownRefRe = regexp.MustCompile(`(?m)^\s*\[[^\]]+\]:\s*(\S+)`)
	// skillAttributeRe is an HTML attribute name, which precedes "=" and is not part of a path.
	skillAttributeRe = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_-]*$`)
)

// skillWrapperChars are the characters that wrap a path in Markdown, HTML or prose: code span
// backticks, quotes, parentheses, brackets, braces, angle brackets, emphasis asterisks, commas,
// semicolons and table pipes. They end a token together with white space.
const skillWrapperChars = "`\"'()[]{}<>*,;|"

// skillAllowedTokens are the only path-shaped tokens, besides absolute URLs and the files
// adoption writes (shippedSkillTargets), the shipped skill texts hold. Each is an exact string,
// with no pattern and no suffix rule, and names where it occurs; a new one in a skill text is
// refused until it is listed here, with the place it occurs.
var skillAllowedTokens = map[string]bool{
	"lusoris@pm.me":                      true, // caveman, adhd-format, social-text: SPDX-FileCopyrightText line.
	"$\\le":                              true, // adhd-format: the LaTeX bound in "$\le 6$ nodes" and the table cells.
	".standards.yaml":                    true, // caveman, social-text: the manifest every adopter repository holds.
	"AGENTS.md":                          true, // caveman, social-text: the canonical agent context adoption writes.
	"CHANGELOG.md":                       true, // social-text: "Never edit CHANGELOG.md directly".
	".yaml":                              true, // social-text: the "<date>-<slug>.yaml" changelog fragment name.
	".standards-receipt.json":            true, // caveman: the gate receipt example at "wrote .standards-receipt.json".
	".agents/skills/caveman/SKILL.md:42": true, // social-text: the file-and-line example.
	"./":                                 true, // caveman: the Go package pattern "./..." once its trailing dots are trimmed.
	"/":                                  true, // social-text: "58 lines / 1500 tokens".
}

// CheckShippedSkillReferences refuses the text of a skill Praetor ships when it names a
// repository path an adopter does not receive. It is a check on the source bundle, run where the
// defect starts (adoption reads the bundle, and Praetor's own tests read its tree): it is not a
// gate of compile-context or audit, which must never fail on the copy an adopter already holds.
//
// It protects by default, with one rule. The text is read as it renders: HTML entities are
// decoded (html.UnescapeString), then split into tokens on white space and the wrapper
// characters (skillWrapperChars), so a path written in a code span, a link, a quote or an HTML
// attribute is a token like any other. A token loses a trailing dot or colon, a #fragment and a
// leading attribute name with "=" (href=docs/x.md is docs/x.md). Every token that holds a slash
// or a backslash, or ends in a file extension, must then be an absolute URL, exactly a path
// adoption writes for the shipped skills (shippedSkillTargets), or one of the listed tokens
// (skillAllowedTokens). The destinations of Markdown links and reference definitions are
// checked too, bare names included. Naming the cases that are unsafe would never converge with
// the ways a path can be written; naming the safe ones does. A bare word with no slash and no
// extension, in prose or as an HTML attribute value, is not a path shape and is not checked.
func CheckShippedSkillReferences(skillName string, data []byte) error {
	text := html.UnescapeString(strings.ReplaceAll(string(data), "\r\n", "\n"))
	targets, err := shippedSkillTargets()
	if err != nil {
		return err
	}
	fields := strings.FieldsFunc(text, isSkillSeparator)
	if len(fields) > maxSkillReferenceTokens {
		return fmt.Errorf("skill %s holds more than %d tokens to check", skillName, maxSkillReferenceTokens)
	}
	for i := 0; i < len(fields) && i < maxSkillReferenceTokens; i++ {
		token := skillToken(fields[i])
		if isPathShaped(token) && !referenceShipped(token, targets) {
			return unshippedReference(skillName, token)
		}
	}
	return checkLinkDestinations(skillName, text, targets)
}

// unshippedReference is the refusal for a path the adopter does not receive.
func unshippedReference(skillName, target string) error {
	return fmt.Errorf("skill %s references repository path %q, which an adopter does not receive", skillName, target)
}

// shippedSkillTargets are the exact paths a shipped skill may name: the skill directories (with
// and without a trailing slash) and the SKILL.md and LICENSE files adoption writes for each
// skill of the bundle, in .agents/skills and in every client skill directory, the same files as
// a sibling skill names them (../<skill>/SKILL.md), and the two files of the skill's own
// directory, bare or as ./SKILL.md and ./LICENSE.
func shippedSkillTargets() (map[string]bool, error) {
	clientDirs, _, err := agentcontext.SkillDirs(nil)
	if err != nil {
		return nil, fmt.Errorf("read the client skill directories: %w", err)
	}
	dirs := append([]string{CanonicalSkillsRel}, clientDirs...)
	targets := map[string]bool{SkillEntryName: true, SkillLicenseName: true,
		"./" + SkillEntryName: true, "./" + SkillLicenseName: true}
	for i := 0; i < len(dirs) && i < maxSkillProjections; i++ {
		targets[dirs[i]] = true
		targets[dirs[i]+"/"] = true
	}
	bundle := config.RegisterSkillBundle()
	for i := 0; i < len(bundle) && i < maxSkillProjections; i++ {
		for _, base := range append([]string{".."}, dirs...) {
			targets[base+"/"+bundle[i]] = true
			targets[base+"/"+bundle[i]+"/"] = true
			targets[base+"/"+bundle[i]+"/"+SkillEntryName] = true
			targets[base+"/"+bundle[i]+"/"+SkillLicenseName] = true
		}
	}
	return targets, nil
}

// isSkillSeparator reports whether r ends a token: white space or a wrapper character.
func isSkillSeparator(r rune) bool {
	return unicode.IsSpace(r) || strings.ContainsRune(skillWrapperChars, r)
}

// skillToken cleans one raw field: a trailing dot or colon goes, then a leading attribute name
// with "=", then trailing dots and colons again, so href=docs/x.md. reads as docs/x.md.
func skillToken(field string) string {
	token := strings.TrimRight(field, ".:")
	if name, value, found := strings.Cut(token, "="); found && skillAttributeRe.MatchString(name) {
		token = strings.TrimRight(value, ".:")
	}
	return token
}

// isPathShaped reports whether token holds a slash or backslash, or ends in a file extension.
func isPathShaped(token string) bool {
	return strings.ContainsAny(token, "/\\") || hasFileExtension(token)
}

// hasFileExtension reports whether token ends in a dot and two to five letters or digits, at
// least one of them a letter, which keeps 1.5, e.g and a bare version out.
func hasFileExtension(token string) bool {
	dot := strings.LastIndexByte(token, '.')
	if dot < 0 {
		return false
	}
	ext := token[dot+1:]
	return len(ext) >= 2 && len(ext) <= 5 && strings.IndexFunc(ext, unicode.IsLetter) >= 0 &&
		strings.IndexFunc(ext, func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) }) < 0
}

// referenceShipped reports whether a path-shaped token (or link destination) may stay: it is
// empty once its fragment goes, an absolute URL, a file adoption writes or a listed token.
func referenceShipped(token string, targets map[string]bool) bool {
	path, _, _ := strings.Cut(token, "#")
	return path == "" || skillAbsoluteURLRe.MatchString(path) || targets[path] || skillAllowedTokens[path]
}

// checkLinkDestinations checks the destination of every Markdown link and reference definition in
// text, which unlike a prose token may be a bare file name.
func checkLinkDestinations(skillName, text string, targets map[string]bool) error {
	mdMatches := util.MarkdownLinkRe.FindAllStringSubmatch(text, maxSkillReferenceTokens+1)
	refMatches := skillMarkdownRefRe.FindAllStringSubmatch(text, maxSkillReferenceTokens+1)
	if len(mdMatches) > maxSkillReferenceTokens || len(refMatches) > maxSkillReferenceTokens {
		return fmt.Errorf("skill %s holds more than %d links to check", skillName, maxSkillReferenceTokens)
	}
	for i := 0; i < len(mdMatches); i++ {
		if !linkDestinationShipped(mdMatches[i][2], targets) {
			return unshippedReference(skillName, strings.TrimSpace(mdMatches[i][2]))
		}
	}
	for i := 0; i < len(refMatches); i++ {
		if !linkDestinationShipped(refMatches[i][1], targets) {
			return unshippedReference(skillName, strings.TrimSpace(refMatches[i][1]))
		}
	}
	return nil
}

// linkDestinationShipped reports whether one link destination is an anchor, an absolute URL or
// a shipped file. Its title and angle brackets are not part of the destination.
func linkDestinationShipped(raw string, targets map[string]bool) bool {
	fields := strings.Fields(strings.Trim(strings.TrimSpace(raw), "<>"))
	return len(fields) == 0 || referenceShipped(strings.Trim(fields[0], "<>"), targets)
}
