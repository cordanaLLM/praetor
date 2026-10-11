// SPDX-License-Identifier: EUPL-1.2
// SPDX-FileCopyrightText: 2026 Lusoris <lusoris@proton.me>

package forge

import (
	"slices"
	"strconv"
	"strings"
	"testing"
)

// plantedClosingCases are texts the forge reads as closing a pull request or an issue on merge,
// with the keyword, the reference and the line the refusal must name. The first is the squash
// body that closed a bot's pull request on 2026-10-11.
var plantedClosingCases = []struct {
	text, keyword, ref string
	line               int
}{
	{"nobody here closes #28 by hand", "closes", "#28", 1},
	{"Fixes owner/repo#5", "Fixes", "owner/repo#5", 1},
	{"resolved https://github.com/o/r/pull/7", "resolved", "https://github.com/o/r/pull/7", 1},
	{"CLOSES: #10", "CLOSES", "#10", 1},
	{"see\n\nfix https://github.com/o/r/issues/3", "fix", "https://github.com/o/r/issues/3", 3},
	{"Resolves GH-12", "Resolves", "GH-12", 1},
}

// closingRefusal returns the error of errs that names keyword, ref and line, or "".
func closingRefusal(errs []string, keyword, ref string, line int) string {
	names := containsAll("closing keyword "+strconv.Quote(keyword), strconv.Quote(ref), "line "+strconv.Itoa(line)+":")
	for _, e := range errs {
		if names(e) {
			return e
		}
	}
	return ""
}

// Negative: each planted closing reference is refused in a commit message (the commit-msg hook
// and check-commits) and in a pull request body (validate-pr), naming the keyword, the reference
// and its line. The commit subject is line 1, so a body line moves down by two.
func TestClosingKeywords_Negative_PlantedReferencesAreRefused(t *testing.T) {
	for _, c := range plantedClosingCases {
		analysis, err := AnalyzeCommit("fix(forge): planted\n\n" + c.text)
		if err != nil || analysis.Valid || closingRefusal(analysis.Errors, c.keyword, c.ref, c.line+2) == "" {
			t.Errorf("commit message %q: %+v, %v; want a refusal naming %q, %q and line %d", c.text, analysis, err, c.keyword, c.ref, c.line+2)
		}
		res, err := ValidatePRChecklist(c.text)
		if err != nil || res.Valid || closingRefusal(res.Errors, c.keyword, c.ref, c.line) == "" {
			t.Errorf("pull request body %q: %+v, %v; want a refusal naming %q, %q and line %d", c.text, res, err, c.keyword, c.ref, c.line)
		}
	}
}

// Negative: a short reference cannot be told apart from a pull request offline, so the refusal
// says the forge was not asked; a /pull/ URL names a pull request outright; an allow marker for
// another reference allows nothing.
func TestClosingKeywords_Negative_MessageAndMismatchedMarker(t *testing.T) {
	errs := closingReferenceErrors("closes #28", "commit message")
	if len(errs) != 1 || !strings.Contains(errs[0], "cannot tell an issue from a pull request without the forge") ||
		!strings.Contains(errs[0], closingAllowMarker+" #28") {
		t.Errorf("short reference: %q; want the offline reason and the allow marker", errs)
	}
	errs = closingReferenceErrors("resolved https://github.com/o/r/pull/7", "commit message")
	if len(errs) != 1 || !strings.Contains(errs[0], "names a pull request") {
		t.Errorf("pull request URL: %q; want it named as a pull request", errs)
	}
	errs = closingReferenceErrors("closes #28\n\n"+closingAllowMarker+" #29\n"+closingAllowMarker+" o/r#28", "commit message")
	if closingRefusal(errs, "closes", "#28", 1) == "" {
		t.Errorf("a marker for #29 or o/r#28 allowed #28: %q", errs)
	}
}

// Positive: clean text passes; a closing reference the text marks as intended passes, the
// marker matched case-insensitively per reference; the existing closing-issue parser keeps its
// answers and reads the documented colon form.
func TestClosingKeywords_Positive_CleanAndMarkedText(t *testing.T) {
	for _, text := range []string{
		"feat(forge): add a check\n\nThe bot's pull request #28 stays open.\n\nSigned-off-by: A <a@example.invalid>",
		"fix(x): y\n\nCloses #12 and Fixes O/R#5\n\n" + closingAllowMarker + " #12\ncloses-own: o/r#5",
		"fix(x): y\n\nresolved https://github.com/o/r/pull/7\n" + closingAllowMarker + " https://github.com/o/r/pull/7",
	} {
		if analysis, err := AnalyzeCommit(text); err != nil || !analysis.Valid {
			t.Errorf("%q: %+v, %v; want it valid", text, analysis, err)
		}
	}
	if got := ParseClosingIssueNumbers("Closes: #10\nfixes other/repo#3\nresolved https://github.com/o/r/issues/4\nFixed #11"); !slices.Equal(got, []int{10, 11}) {
		t.Errorf("ParseClosingIssueNumbers = %v, want [10 11]", got)
	}
}

// Boundary: words that only start like a keyword, a number not right after the keyword and a
// reference glued to more text are not closing references. A keyword inside a code fence or a
// quoted line is refused: a commit message is plain text and a squash merge copies the pull
// request body into it, so the forge reads the fence as prose there. A marker line is never a
// finding itself, and more references than the bound are refused, not checked in part.
func TestClosingKeywords_Boundary_WordsFencesAndBound(t *testing.T) {
	for _, text := range []string{
		"a closed-loop controller #3", "fixture #3", "prefixes #3", "closes the gap in #4",
		"closes #28abc", "unresolved #2", closingAllowMarker + " #28",
	} {
		if errs := closingReferenceErrors(text, "commit message"); len(errs) != 0 {
			t.Errorf("%q: %q; want no finding", text, errs)
		}
	}
	for _, c := range []struct {
		text string
		line int
	}{{"```\ncloses #28\n```", 2}, {"> nobody closes #28", 1}, {"`closes #28`", 1}} {
		if errs := closingReferenceErrors(c.text, "pull request body"); closingRefusal(errs, "closes", "#28", c.line) == "" {
			t.Errorf("%q: %q; want a refusal at line %d", c.text, errs, c.line)
		}
	}
	over := strings.Repeat("closes #1\n", maxClosingReferences+1)
	if errs := closingReferenceErrors(over, "commit message"); len(errs) != 1 || !strings.Contains(errs[0], "more than") {
		t.Errorf("past the bound: %d errors (%.200q); want one bound refusal", len(errs), errs)
	}
	at := strings.Repeat("closes #1\n", maxClosingReferences)
	if errs := closingReferenceErrors(at, "commit message"); len(errs) != maxClosingReferences {
		t.Errorf("at the bound: %d errors, want %d findings", len(errs), maxClosingReferences)
	}
}
