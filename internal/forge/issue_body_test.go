package forge

import (
	"context"
	"net/http"
	"strings"
	"testing"
)

// Positive: GetIssue reads one issue with its body, labels and sub-issue progress, and
// EditIssueBody PATCHes the body alone.
func TestGitHubDriver_GetIssueEditIssueBody_Positive(t *testing.T) {
	gh, fake := newFakeForge(t, func(w http.ResponseWriter, r *http.Request, _ int) {
		if r.Method == http.MethodGet {
			writeJSON(t, w, http.StatusOK, map[string]any{
				"number": 12, "title": "Epic", "state": "open", "body": "- [ ] #13",
				"labels":             []map[string]string{{"name": "epic"}},
				"sub_issues_summary": map[string]int{"total": 3, "completed": 2, "percent_completed": 66},
			})
			return
		}
		writeJSON(t, w, http.StatusOK, map[string]any{"number": 12})
	})
	ctx := context.Background()

	issue, err := gh.GetIssue(ctx, 12)
	if err != nil {
		t.Fatalf("GetIssue: %v", err)
	}
	if issue.ID != 12 || issue.Body != "- [ ] #13" || len(issue.Labels) != 1 || issue.Labels[0] != "epic" {
		t.Fatalf("unexpected issue: %+v", issue)
	}
	if issue.SubIssues == nil || issue.SubIssues.Total != 3 || issue.SubIssues.Completed != 2 {
		t.Fatalf("sub-issue progress not read: %+v", issue.SubIssues)
	}
	if fake.requests[0].Path != "/repos/acme/widgets/issues/12" {
		t.Fatalf("unexpected read path %s", fake.requests[0].Path)
	}

	if err := gh.EditIssueBody(ctx, 12, "- [x] #13"); err != nil {
		t.Fatalf("EditIssueBody: %v", err)
	}
	patch := fake.requests[1]
	if patch.Method != http.MethodPatch || patch.Path != "/repos/acme/widgets/issues/12" {
		t.Fatalf("unexpected edit request: %s %s", patch.Method, patch.Path)
	}
	if len(patch.Body) != 1 || patch.Body["body"] != "- [x] #13" {
		t.Fatalf("the edit must carry the body alone: %s", patch.Raw)
	}
}

// Negative: a refused read, a pull request, a mismatched number and a refused edit are
// errors; an invalid number never reaches the forge.
func TestGitHubDriver_GetIssueEditIssueBody_Negative(t *testing.T) {
	gh, fake := newFakeForge(t, func(w http.ResponseWriter, r *http.Request, _ int) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/7"):
			writeJSON(t, w, http.StatusOK, map[string]any{"number": 7, "title": "PR", "pull_request": map[string]string{"url": "u"}})
		case strings.HasSuffix(r.URL.Path, "/8"):
			writeJSON(t, w, http.StatusOK, map[string]any{"number": 9, "title": "other"})
		default:
			writeJSON(t, w, http.StatusForbidden, map[string]any{"message": "no"})
		}
	})
	ctx := context.Background()

	for _, number := range []int{5, 7, 8} {
		if _, err := gh.GetIssue(ctx, number); err == nil {
			t.Errorf("GetIssue(%d) accepted a refused, pull-request or mismatched read", number)
		}
	}
	if err := gh.EditIssueBody(ctx, 5, "x"); err == nil {
		t.Fatal("expected an error for a 403 edit")
	}
	before := len(fake.requests)
	if _, err := gh.GetIssue(ctx, 0); err == nil {
		t.Fatal("expected an error for issue number 0")
	}
	if err := gh.EditIssueBody(ctx, -1, "x"); err == nil {
		t.Fatal("expected an error for a negative issue number")
	}
	if len(fake.requests) != before {
		t.Fatalf("an invalid number reached the forge: %d requests", len(fake.requests)-before)
	}
}

// Boundary: an empty body is a valid edit, and an issue without sub-issue progress reads
// as nil rather than as zero sub-issues.
func TestGitHubDriver_GetIssueEditIssueBody_Boundary(t *testing.T) {
	gh, fake := newFakeForge(t, func(w http.ResponseWriter, r *http.Request, _ int) {
		writeJSON(t, w, http.StatusOK, map[string]any{"number": 3, "title": "Plain", "state": "closed"})
	})
	ctx := context.Background()

	issue, err := gh.GetIssue(ctx, 3)
	if err != nil || issue.SubIssues != nil || issue.State != "closed" {
		t.Fatalf("plain issue: %+v, %v", issue, err)
	}
	if err := gh.EditIssueBody(ctx, 3, ""); err != nil {
		t.Fatalf("empty body edit: %v", err)
	}
	if raw := fake.requests[1].Raw; raw != `{"body":""}` {
		t.Fatalf("empty body must be sent explicitly, got %s", raw)
	}
}
