// SPDX-License-Identifier: EUPL-1.2
// SPDX-FileCopyrightText: 2026 Lusoris <lusoris@proton.me>

package forge

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// A closing keyword next to a reference closes that issue or pull request when the text reaches
// the default branch, whatever the sentence around it says: on 2026-10-11 a squash body that read
// "nobody here closes #28 by hand" closed a bot's pull request on merge. GitHub documents the
// keywords and the reference forms in "Linking a pull request to an issue"; the keyword is
// case-insensitive and may carry a colon ("Closes: #10").
//
// closingKeywordRegex is the one reader of closing references (HISS-19): ParseClosingIssueNumbers
// reads closed issue numbers through it, and closingReferenceErrors refuses commit messages and
// pull request bodies through it. Submatches: 1 the keyword; 2 the whole reference; 3-5 a URL's
// owner, kind (issues, pull or pulls) and number; 6-8 a short reference's owner, repository and
// number; 9 the number of a GH-N reference.
var closingKeywordRegex = regexp.MustCompile(`(?i)\b(close[sd]?|fix(?:e[sd])?|resolve[sd]?)\b[ \t]*:?\s*(` +
	`https?://[^\s/]+/([a-z0-9][\w.-]*)/[\w.-]+/(issues|pulls?)/(\d+)` +
	`|(?:([a-z0-9][\w.-]*)/)?([\w.-]+)?#(\d+)` +
	`|gh-(\d+))\b`)

// closingAllowMarker starts the line that marks one closing reference as intended: the author
// opened that pull request, or the reference is an issue the change is meant to close. The line
// carries the reference as the text writes it, compared without case: "Closes-own: #12". The
// keyword in it is followed by "-own:", not by a reference, so it closes nothing itself.
const closingAllowMarker = "Closes-own:"

// maxClosingReferences bounds the closing references one text is checked for (HISS-02). A text
// with more is refused, not checked in part.
const maxClosingReferences = 1000

// closingReference is one closing keyword and the reference it closes.
type closingReference struct {
	keyword string
	ref     string
	// pull is true for a pull request URL; every other form may name an issue or a pull
	// request, which share one number space.
	pull bool
	// local is true for a bare #N, a number in the repository the text belongs to.
	local  bool
	number int
	line   int
}

// findClosingReferences returns up to limit closing references of text, each with its 1-based
// line, and whether text holds more than limit.
func findClosingReferences(text string, limit int) ([]closingReference, bool) {
	matches := closingKeywordRegex.FindAllStringSubmatchIndex(text, limit+1)
	refs := make([]closingReference, 0, min(len(matches), limit))
	for i := 0; i < len(matches) && i < limit; i++ {
		refs = append(refs, newClosingReference(text, matches[i]))
	}
	return refs, len(matches) > limit
}

// newClosingReference builds the reference of one closingKeywordRegex match m in text.
func newClosingReference(text string, m []int) closingReference {
	group := func(n int) string {
		if m[2*n] < 0 {
			return ""
		}
		return text[m[2*n]:m[2*n+1]]
	}
	ref := closingReference{
		keyword: group(1),
		ref:     group(2),
		pull:    strings.HasPrefix(strings.ToLower(group(4)), "pull"),
		local:   group(8) != "" && group(6) == "" && group(7) == "",
		line:    strings.Count(text[:m[0]], "\n") + 1,
	}
	for _, n := range []int{5, 8, 9} {
		if num, err := strconv.Atoi(group(n)); err == nil {
			ref.number = num
		}
	}
	return ref
}

// allowedClosingReferences returns the references the closingAllowMarker lines among lines mark
// as intended, lower-cased.
func allowedClosingReferences(lines []string) map[string]bool {
	allowed := make(map[string]bool)
	for i := 0; i < len(lines) && i < maxCommitMessageLines; i++ {
		rest, ok := cutPrefixFold(strings.TrimSpace(lines[i]), closingAllowMarker)
		if ok && rest != "" && !strings.ContainsAny(rest, " \t") {
			allowed[strings.ToLower(rest)] = true
		}
	}
	return allowed
}

// cutPrefixFold is strings.CutPrefix with prefix compared without case; the rest is trimmed.
func cutPrefixFold(s, prefix string) (string, bool) {
	if len(s) < len(prefix) || !strings.EqualFold(s[:len(prefix)], prefix) {
		return "", false
	}
	return strings.TrimSpace(s[len(prefix):]), true
}

// closingReferenceErrors returns one refusal for each closing reference in text, where names
// the text ("commit message", "pull request body"), that no closingAllowMarker line marks as
// intended. Every line counts, inside a code fence and a quoted line too: a commit message is
// plain text, and a squash merge copies the pull request body into one.
//
// It runs offline, with no forge to ask whether a number is an issue or a pull request the
// author opened, so it fails closed: every reference is refused unless marked, and the refusal
// says the forge was not asked. Only a /pull/ URL is known to name a pull request.
func closingReferenceErrors(text, where string) []string {
	refs, over := findClosingReferences(text, maxClosingReferences)
	if over {
		return []string{fmt.Sprintf("the %s holds more than %d closing references (close, fix or resolve next to an issue or "+
			"pull request reference); it is refused rather than checked in part", where, maxClosingReferences)}
	}
	allowed := allowedClosingReferences(strings.Split(text, "\n"))
	var errs []string
	for i := 0; i < len(refs); i++ {
		if !allowed[strings.ToLower(refs[i].ref)] {
			errs = append(errs, closingReferenceError(refs[i], where))
		}
	}
	return errs
}

// closingReferenceError is the refusal of one unmarked closing reference.
func closingReferenceError(ref closingReference, where string) string {
	what := "may name a pull request the author did not open: issues and pull requests share one number space, " +
		"and this offline check cannot tell an issue from a pull request without the forge"
	if ref.pull {
		what = "names a pull request, which the author may not have opened"
	}
	return fmt.Sprintf("%s line %d: closing keyword %q next to %q closes it when the text reaches the default branch, "+
		"and it %s. Reword the line so no closing keyword stands before the reference, or, when closing it is "+
		"intended, add the line \"%s %s\"", where, ref.line, ref.keyword, ref.ref, what, closingAllowMarker, ref.ref)
}
