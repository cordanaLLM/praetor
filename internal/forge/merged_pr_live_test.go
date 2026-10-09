// SPDX-License-Identifier: EUPL-1.2
// SPDX-FileCopyrightText: 2026 Lusoris <lusoris@proton.me>

package forge

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"
)

func atoiOrFail(t *testing.T, text string) int {
	t.Helper()
	n, err := strconv.Atoi(text)
	if err != nil {
		t.Errorf("request carries %q, not a number: %v", text, err)
	}
	return n
}

func pullJSON(number int, branch, milestone, merged, body string) map[string]any {
	pull := map[string]any{
		"number": number, "title": "t", "body": body, "created_at": "2026-10-01T00:00:00Z",
		"head": map[string]any{"ref": branch},
	}
	if merged != "" {
		pull["merged_at"] = merged
	}
	if milestone != "" {
		pull["milestone"] = map[string]any{"title": milestone}
	}
	return pull
}

// serveLedgerForge answers pull listings from pages and issue lookups from issues
// (number -> created_at; absent means 404; "pr" means the item is a pull request).
func serveLedgerForge(t *testing.T, pages [][]map[string]any, issues map[int]string) (*GitHubDriver, *fakeForgeServer) {
	t.Helper()
	return newFakeForge(t, func(w http.ResponseWriter, r *http.Request, index int) {
		if num, ok := strings.CutPrefix(r.URL.Path, "/repos/acme/widgets/issues/"); ok {
			n := atoiOrFail(t, num)
			created, found := issues[n]
			switch {
			case !found:
				writeJSON(t, w, http.StatusNotFound, map[string]any{"message": "Not Found"})
			case created == "pr":
				writeJSON(t, w, http.StatusOK, map[string]any{"number": n, "created_at": "2026-09-01T00:00:00Z", "pull_request": map[string]any{"url": "u"}})
			default:
				writeJSON(t, w, http.StatusOK, map[string]any{"number": n, "created_at": created, "closed_at": "2026-10-02T00:00:00Z"})
			}
			return
		}
		page := atoiOrFail(t, r.URL.Query().Get("page"))
		if page < 1 || page > len(pages) {
			writeJSON(t, w, http.StatusOK, []map[string]any{})
			return
		}
		writeJSON(t, w, http.StatusOK, pages[page-1])
	})
}

func TestListMergedPullRequests_Positive_MilestoneBeforeLimitSortedByMerge(t *testing.T) {
	pages := [][]map[string]any{{
		pullJSON(10, "feat/a", "M1", "2026-10-03T00:00:00Z", "Closes #1"),
		pullJSON(11, "feat/b", "M2", "2026-10-09T00:00:00Z", ""),
		pullJSON(12, "feat/c", "M1", "2026-10-05T00:00:00Z", "Fixes #2 and closes #1"),
		pullJSON(13, "feat/d", "M1", "", ""),
	}}
	issues := map[int]string{1: "2026-09-20T00:00:00Z", 2: "2026-09-25T00:00:00Z"}
	gh, fake := serveLedgerForge(t, pages, issues)
	list, err := gh.ListMergedPullRequests(context.Background(), MergedPullRequestQuery{Limit: 1, Milestone: "M1"})
	if err != nil {
		t.Fatal(err)
	}
	if len(list.PullRequests) != 1 || list.PullRequests[0].Number != 12 {
		t.Fatalf("want newest M1 merge #12 only (M2 #11 is newer but filtered first): %+v", list.PullRequests)
	}
	if !strings.Contains(list.Truncated, "1 matching merged pull requests beyond the limit of 1") {
		t.Fatalf("truncation not stated: %q", list.Truncated)
	}
	want := time.Date(2026, 9, 20, 0, 0, 0, 0, time.UTC)
	got := list.PullRequests[0].ClosingIssues
	if len(got) != 2 || !got[1].CreatedAt.Equal(want) || got[1].ClosedAt == nil {
		t.Fatalf("closing issue created_at not fetched: %+v", got)
	}
	if issueGets := countIssueLookups(fake); issueGets != 2 {
		t.Fatalf("only the kept pull request's issues are fetched, once each: %d", issueGets)
	}
}

func countIssueLookups(fake *fakeForgeServer) int {
	lookups := 0
	for _, req := range fake.requests {
		if strings.Contains(req.Path, "/issues/") {
			lookups++
		}
	}
	return lookups
}

func TestListMergedPullRequests_Negative_FetchFailuresAreWarnings(t *testing.T) {
	pages := [][]map[string]any{{
		pullJSON(20, "feat/a", "", "2026-10-03T00:00:00Z", "Closes #404"),
		pullJSON(21, "feat/b", "", "2026-10-02T00:00:00Z", "Closes #7"),
		{"number": 22, "title": "bad", "created_at": "garbage", "merged_at": "2026-10-01T00:00:00Z", "head": map[string]any{"ref": "x"}},
	}}
	gh, _ := serveLedgerForge(t, pages, map[int]string{7: "pr"})
	list, err := gh.ListMergedPullRequests(context.Background(), MergedPullRequestQuery{})
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(list.Warnings, "\n")
	for _, want := range []string{"closing issue #404 of pull request #20 not fetched", "closing issue #7 of pull request #21 not fetched", "pull request #22 skipped"} {
		if !strings.Contains(joined, want) {
			t.Errorf("warning %q missing in %q", want, joined)
		}
	}
	if !list.PullRequests[0].ClosingIssues[0].CreatedAt.IsZero() {
		t.Error("a failed lookup must leave created_at zero, not invent a time")
	}
}

func TestListMergedPullRequests_Negative_ListingErrorFails(t *testing.T) {
	gh, _ := newFakeForge(t, func(w http.ResponseWriter, _ *http.Request, _ int) {
		writeJSON(t, w, http.StatusInternalServerError, map[string]any{"message": "boom"})
	})
	if _, err := gh.ListMergedPullRequests(context.Background(), MergedPullRequestQuery{}); err == nil {
		t.Fatal("a failing listing must fail the call")
	}
	unconfigured := NewGitHubDriver("token", "http://127.0.0.1:1")
	if _, err := unconfigured.ListMergedPullRequests(context.Background(), MergedPullRequestQuery{}); err == nil {
		t.Fatal("a driver without repository must fail")
	}
}

func TestListMergedPullRequests_Boundary_PageCeilingStatesTruncation(t *testing.T) {
	full := make([]map[string]any, issuesPerPage)
	for i := range full {
		full[i] = pullJSON(i+1, fmt.Sprintf("b%d", i), "", "2026-10-03T00:00:00Z", "")
	}
	pages := make([][]map[string]any, maxIssuePages)
	for i := range pages {
		pages[i] = full
	}
	gh, fake := serveLedgerForge(t, pages, nil)
	list, err := gh.ListMergedPullRequests(context.Background(), MergedPullRequestQuery{Limit: 5})
	if err != nil {
		t.Fatal(err)
	}
	if len(list.PullRequests) != 5 || !strings.Contains(list.Truncated, "older merged pull requests were not examined") {
		t.Fatalf("ceiling truncation not stated: %d %q", len(list.PullRequests), list.Truncated)
	}
	if len(fake.requests) != maxIssuePages {
		t.Fatalf("pages walked: %d", len(fake.requests))
	}
}

func TestListMergedPullRequests_Boundary_IssueLookupCap(t *testing.T) {
	var pages [][]map[string]any
	issues := map[int]string{}
	for p := 0; p < 3; p++ {
		var page []map[string]any
		for i := 0; i < issuesPerPage; i++ {
			n := p*issuesPerPage + i + 1
			page = append(page, pullJSON(n, fmt.Sprintf("b%d", n), "", "2026-10-03T00:00:00Z", fmt.Sprintf("Closes #%d", 1000+n)))
			issues[1000+n] = "2026-09-01T00:00:00Z"
		}
		pages = append(pages, page)
	}
	gh, fake := serveLedgerForge(t, pages, issues)
	list, err := gh.ListMergedPullRequests(context.Background(), MergedPullRequestQuery{})
	if err != nil {
		t.Fatal(err)
	}
	if lookups := countIssueLookups(fake); lookups != maxClosingIssueFetches {
		t.Fatalf("lookups = %d, want the cap %d", lookups, maxClosingIssueFetches)
	}
	if len(list.Warnings) != 300-maxClosingIssueFetches || !strings.Contains(list.Warnings[0], "more than 200 lookups needed") {
		t.Fatalf("cap overflow not reported per issue: %d %v", len(list.Warnings), list.Warnings[:1])
	}
}

func TestParseIssueTimes_Positive_Negative_Boundary(t *testing.T) {
	closed := "2026-10-02T00:00:00Z"
	created, got, err := parseIssueTimes(&ghIssueDetailRaw{CreatedAt: "2026-10-01T00:00:00Z", ClosedAt: &closed}, 5)
	if err != nil || created.IsZero() || got == nil {
		t.Fatalf("positive: %v %v %v", created, got, err)
	}
	if _, _, err := parseIssueTimes(&ghIssueDetailRaw{CreatedAt: "nope"}, 5); err == nil {
		t.Error("bad created_at must fail")
	}
	if _, _, err := parseIssueTimes(&ghIssueDetailRaw{CreatedAt: "2026-10-01T00:00:00Z", PullRequest: &struct {
		URL string `json:"url"`
	}{URL: "u"}}, 5); err == nil {
		t.Error("a pull request is not a closing issue")
	}
	_, open, err := parseIssueTimes(&ghIssueDetailRaw{CreatedAt: "2026-10-01T00:00:00Z"}, 5)
	if err != nil || open != nil {
		t.Errorf("open issue has no closed_at: %v %v", open, err)
	}
	gh := NewGitHubDriver("t", "http://127.0.0.1:1")
	gh.SetRepository("a", "b")
	if _, _, err := gh.fetchIssueTimes(context.Background(), 0); err == nil {
		t.Error("issue number 0 must fail")
	}
}

func TestParseClosingIssueNumbers_Negative_CrossRepositoryReference(t *testing.T) {
	got := ParseClosingIssueNumbers("Closes other/repo#12\nCloses #3\nFixes owner/name#4")
	if len(got) != 1 || got[0] != 3 {
		t.Fatalf("only the local reference counts: %v", got)
	}
}
