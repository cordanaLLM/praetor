// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package forge

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
)

func TestGitHubClaim_Positive_CommentCalls(t *testing.T) {
	gh, fake := newFakeForge(t, func(w http.ResponseWriter, r *http.Request, _ int) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/user":
			writeJSON(t, w, http.StatusOK, map[string]any{"login": "operator"})
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/issues/7/comments"):
			writeJSON(t, w, http.StatusOK, []map[string]any{{"id": 5, "user": map[string]any{"login": "a"}, "author_association": "OWNER", "body": "hi"}})
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/issues/7/comments"):
			writeJSON(t, w, http.StatusCreated, map[string]any{"id": 6, "user": map[string]any{"login": "operator"}, "author_association": "OWNER", "body": "x"})
		case r.Method == http.MethodPatch && strings.HasSuffix(r.URL.Path, "/issues/comments/6"):
			writeJSON(t, w, http.StatusOK, map[string]any{"id": 6})
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/issues/7/assignees"):
			writeJSON(t, w, http.StatusCreated, map[string]any{})
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			writeJSON(t, w, http.StatusTeapot, nil)
		}
	})
	ctx := context.Background()
	if login, err := gh.Viewer(ctx); err != nil || login != "operator" {
		t.Fatalf("viewer: %q %v", login, err)
	}
	comments, err := gh.ListIssueComments(ctx, 7)
	if err != nil || len(comments) != 1 || comments[0].Association != "OWNER" || comments[0].Author != "a" {
		t.Fatalf("list: %+v %v", comments, err)
	}
	created, err := gh.CreateIssueComment(ctx, 7, "x")
	if err != nil || created.ID != 6 {
		t.Fatalf("create: %+v %v", created, err)
	}
	if err := gh.EditIssueComment(ctx, 6, "y"); err != nil {
		t.Fatalf("edit: %v", err)
	}
	if err := gh.AddAssignees(ctx, 7, []string{"operator"}); err != nil {
		t.Fatalf("assign: %v", err)
	}
	last := fake.requests[len(fake.requests)-1]
	if got, _ := last.Body["assignees"].([]any); len(got) != 1 {
		t.Fatalf("assignees payload: %v", last.Body)
	}
}

func TestGitHubClaim_Negative_StatusesAndBadInput(t *testing.T) {
	gh, _ := newFakeForge(t, func(w http.ResponseWriter, _ *http.Request, _ int) {
		writeJSON(t, w, http.StatusForbidden, map[string]any{"message": "no"})
	})
	ctx := context.Background()
	if _, err := gh.Viewer(ctx); err == nil {
		t.Fatal("viewer must fail on 403")
	}
	if _, err := gh.ListIssueComments(ctx, 7); err == nil {
		t.Fatal("list must fail on 403")
	}
	if _, err := gh.CreateIssueComment(ctx, 7, "x"); err == nil {
		t.Fatal("create must fail on 403")
	}
	if err := gh.EditIssueComment(ctx, 6, "x"); err == nil {
		t.Fatal("edit must fail on 403")
	}
	if err := gh.AddAssignees(ctx, 7, []string{"a"}); err == nil {
		t.Fatal("assign must fail on 403")
	}
	if err := gh.EnsureLabel(ctx, Label{Name: "x", Color: "ffffff"}); err == nil {
		t.Fatal("ensure label must fail on 403")
	}
	if _, err := gh.ListIssueComments(ctx, 0); err == nil {
		t.Fatal("issue 0 must be refused")
	}
	if _, err := gh.CreateIssueComment(ctx, -1, "x"); err == nil {
		t.Fatal("issue -1 must be refused")
	}
	if err := gh.EditIssueComment(ctx, 0, "x"); err == nil {
		t.Fatal("comment 0 must be refused")
	}
	if err := gh.AddAssignees(ctx, 7, nil); err == nil {
		t.Fatal("empty login set must be refused")
	}
}

func TestGitHubClaim_Negative_CommentCreateWithoutIdFails(t *testing.T) {
	gh, _ := newFakeForge(t, func(w http.ResponseWriter, _ *http.Request, _ int) {
		writeJSON(t, w, http.StatusCreated, map[string]any{"body": "x"})
	})
	if _, err := gh.CreateIssueComment(context.Background(), 7, "x"); err == nil {
		t.Fatal("a created comment without an id must fail")
	}
}

func TestGitHubClaim_Boundary_CommentPagesFailClosedAtTheBound(t *testing.T) {
	gh, _ := newFakeForge(t, func(w http.ResponseWriter, _ *http.Request, _ int) {
		writeJSON(t, w, http.StatusOK, namedPage("id", "body", 1, issuesPerPage, "c"))
	})
	_, err := gh.ListIssueComments(context.Background(), 7)
	if !errors.Is(err, errPageCeiling) {
		t.Fatalf("a thread beyond the bound must fail closed, got %v", err)
	}
	// One short page ends the listing.
	short, _ := newFakeForge(t, func(w http.ResponseWriter, _ *http.Request, _ int) {
		writeJSON(t, w, http.StatusOK, namedPage("id", "body", 1, issuesPerPage-1, "c"))
	})
	got, err := short.ListIssueComments(context.Background(), 7)
	if err != nil || len(got) != issuesPerPage-1 {
		t.Fatalf("short page: %d %v", len(got), err)
	}
}

func TestGitHubClaim_EnsureLabel_CreatesOnlyWhenMissing(t *testing.T) {
	status := http.StatusNotFound
	gh, fake := newFakeForge(t, func(w http.ResponseWriter, r *http.Request, _ int) {
		if r.Method == http.MethodGet {
			writeJSON(t, w, status, map[string]any{})
			return
		}
		writeJSON(t, w, http.StatusCreated, map[string]any{})
	})
	label := Label{Name: LabelInProgress, Color: "1d76db", Description: "d"}
	if err := gh.EnsureLabel(context.Background(), label); err != nil {
		t.Fatal(err)
	}
	if methodsOf(fake)[len(fake.requests)-1] != http.MethodPost {
		t.Fatalf("missing label must be created: %v", methodsOf(fake))
	}
	status = http.StatusOK
	before := len(fake.requests)
	if err := gh.EnsureLabel(context.Background(), label); err != nil {
		t.Fatal(err)
	}
	if len(fake.requests) != before+1 {
		t.Fatalf("an existing label must be left alone, requests: %v", methodsOf(fake))
	}
}
