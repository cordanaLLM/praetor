// SPDX-License-Identifier: EUPL-1.2
// SPDX-FileCopyrightText: 2026 Lusoris <lusoris@proton.me>

package forge

import (
	"context"
	"errors"
	"net/http"
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

// The six planted references remain covered: offline, explicit pull request URLs fail, while
// ambiguous references warn and stay valid until CI resolves them with forge access.
func TestClosingKeywords_PlantedReferencesOffline(t *testing.T) {
	for _, c := range plantedClosingCases {
		analysis, err := AnalyzeCommit("fix(forge): planted\n\n" + c.text)
		if err != nil {
			t.Errorf("commit message %q: %v", c.text, err)
			continue
		}
		if strings.Contains(c.ref, "/pull/") || strings.Contains(c.ref, "/pulls/") {
			if analysis.Valid || closingRefusal(analysis.Errors, c.keyword, c.ref, c.line+2) == "" {
				t.Errorf("pull URL %q: %+v; want refusal naming %q, %q and line %d", c.text, analysis, c.keyword, c.ref, c.line+2)
			}
			continue
		}
		if !analysis.Valid || len(analysis.Errors) != 0 || closingRefusal(analysis.Warnings, c.keyword, c.ref, c.line+2) == "" {
			t.Errorf("ambiguous ref %q: %+v; want valid with warning naming %q, %q and line %d", c.text, analysis, c.keyword, c.ref, c.line+2)
		}
	}
}

// Negative and boundary: offline warnings name the CI resolver; pull request URLs fail; an
// allow marker for another reference allows nothing.
func TestClosingKeywords_OfflineWarningAndPullURLRefusal(t *testing.T) {
	errs, warnings := closingReferenceFindings(context.Background(), "closes #28", "commit message", ClosingReferencePolicy{})
	if len(errs) != 0 || len(warnings) != 1 || !strings.Contains(warnings[0], "praetorctl forge check-commits") {
		t.Errorf("short reference: errors=%q warnings=%q; want one CI warning", errs, warnings)
	}
	errs, warnings = closingReferenceFindings(context.Background(), "resolved https://github.com/o/r/pull/7", "commit message", ClosingReferencePolicy{})
	if len(errs) != 1 || !strings.Contains(errs[0], "names a pull request") {
		t.Errorf("pull request URL: %q; want it named as a pull request", errs)
	}
	if len(warnings) != 0 {
		t.Errorf("pull request URL produced warnings too: %q", warnings)
	}
	errs, warnings = closingReferenceFindings(context.Background(), "closes #28\n\n"+closingAllowMarker+" #29\n"+closingAllowMarker+" o/r#28", "commit message", ClosingReferencePolicy{})
	if len(errs) != 0 || closingRefusal(warnings, "closes", "#28", 1) == "" {
		t.Errorf("a marker for #29 or o/r#28 allowed #28: errors=%q warnings=%q", errs, warnings)
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

type stubClosingResolver map[string]struct {
	res ClosingReferenceResolution
	err error
}

func (s stubClosingResolver) ResolveClosingReference(_ context.Context, target ClosingReferenceTarget) (ClosingReferenceResolution, error) {
	item, ok := s[target.Ref]
	if !ok {
		return ClosingReferenceResolution{}, errors.New("missing stub")
	}
	return item.res, item.err
}

// Positive and negative: online resolution allows issues and same-author pull requests, and
// refuses foreign humans, foreign bots, and API failures.
func TestClosingKeywords_OnlineResolution(t *testing.T) {
	policy := ClosingReferencePolicy{
		PullRequestAuthor: "alice",
		Resolver: stubClosingResolver{
			"#1": {res: ClosingReferenceResolution{Kind: ClosingTargetIssue, Author: "reporter"}},
			"#2": {res: ClosingReferenceResolution{Kind: ClosingTargetPullRequest, Author: "alice"}},
			"#3": {res: ClosingReferenceResolution{Kind: ClosingTargetPullRequest, Author: "bob"}},
			"#4": {res: ClosingReferenceResolution{Kind: ClosingTargetPullRequest, Author: "renovate[bot]"}},
			"#5": {err: errors.New("api unavailable")},
		},
	}
	for _, text := range []string{"fix(x): issue\n\nCloses #1", "fix(x): own pr\n\nCloses #2"} {
		if analysis, err := AnalyzeCommitWithPolicy(context.Background(), text, policy); err != nil || !analysis.Valid {
			t.Errorf("%q: %+v, %v; want valid", text, analysis, err)
		}
	}
	for _, c := range []struct {
		ref, author string
	}{
		{"#3", "bob"},
		{"#4", "renovate[bot]"},
		{"#5", "api unavailable"},
	} {
		analysis, err := AnalyzeCommitWithPolicy(context.Background(), "fix(x): foreign\n\nCloses "+c.ref, policy)
		joined := strings.Join(analysis.Errors, "\n")
		if err != nil || analysis.Valid || !strings.Contains(joined, c.ref) || !strings.Contains(joined, "line 3:") ||
			!strings.Contains(joined, c.author) {
			t.Errorf("%s: %+v, %v; want refusal naming author/reason %q", c.ref, analysis, err, c.author)
		}
	}
}

func TestGitHubDriverResolveClosingReference(t *testing.T) {
	gh, _ := newFakeForge(t, func(w http.ResponseWriter, r *http.Request, index int) {
		switch r.URL.Path {
		case "/repos/acme/widgets/issues/1":
			writeJSON(t, w, http.StatusOK, map[string]any{"number": 1, "user": map[string]string{"login": "reporter"}})
		case "/repos/acme/widgets/issues/2":
			writeJSON(t, w, http.StatusOK, map[string]any{"number": 2, "user": map[string]string{"login": "alice"},
				"pull_request": map[string]string{"url": "u"}})
		case "/repos/other/repo/issues/3":
			writeJSON(t, w, http.StatusNotFound, map[string]string{"message": "Not Found"})
		default:
			t.Fatalf("unexpected request %s", r.URL.Path)
		}
	})
	issue, err := gh.ResolveClosingReference(context.Background(), ClosingReferenceTarget{Ref: "#1", Number: 1})
	if err != nil || issue.Kind != ClosingTargetIssue {
		t.Fatalf("issue resolution = %+v, %v", issue, err)
	}
	pr, err := gh.ResolveClosingReference(context.Background(), ClosingReferenceTarget{Ref: "#2", Number: 2})
	if err != nil || pr.Kind != ClosingTargetPullRequest || pr.Author != "alice" {
		t.Fatalf("pull resolution = %+v, %v", pr, err)
	}
	_, err = gh.ResolveClosingReference(context.Background(), ClosingReferenceTarget{Ref: "other/repo#3", Owner: "other", Repo: "repo", Number: 3})
	if err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("not found resolution = %v", err)
	}
}

// Boundary: words that only start like a keyword, a number not right after the keyword and a
// reference glued to more text are not closing references. A keyword inside a code fence or a
// quoted line is still scanned: a commit message is plain text and a squash merge copies the pull
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
		_, warnings := closingReferenceFindings(context.Background(), c.text, "pull request body", ClosingReferencePolicy{})
		if closingRefusal(warnings, "closes", "#28", c.line) == "" {
			t.Errorf("%q: %q; want a warning at line %d", c.text, warnings, c.line)
		}
	}
	over := strings.Repeat("closes #1\n", maxClosingReferences+1)
	if errs := closingReferenceErrors(over, "commit message"); len(errs) != 1 || !strings.Contains(errs[0], "more than") {
		t.Errorf("past the bound: %d errors (%.200q); want one bound refusal", len(errs), errs)
	}
	at := strings.Repeat("closes #1\n", maxClosingReferences)
	errs, warnings := closingReferenceFindings(context.Background(), at, "commit message", ClosingReferencePolicy{})
	if len(errs) != 0 || len(warnings) != maxClosingReferences {
		t.Errorf("at the bound: %d errors and %d warnings, want %d warnings", len(errs), len(warnings), maxClosingReferences)
	}
}
