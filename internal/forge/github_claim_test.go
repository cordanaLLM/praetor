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

// claimRoutes answers the calls the claim code makes, one route per method and path suffix.
func claimRoutes(t *testing.T) func(w http.ResponseWriter, r *http.Request, index int) {
	t.Helper()
	routes := []struct {
		method, suffix string
		status         int
		body           any
	}{
		{http.MethodGet, "/user", http.StatusOK, map[string]any{"login": "operator"}},
		{http.MethodGet, "/issues/7/comments", http.StatusOK, []map[string]any{{"id": 5, "user": map[string]any{"login": "a"}, "author_association": "OWNER", "body": "hi"}}},
		{http.MethodPost, "/issues/7/comments", http.StatusCreated, map[string]any{"id": 6, "user": map[string]any{"login": "operator"}, "author_association": "OWNER", "body": "x"}},
		{http.MethodPatch, "/issues/comments/6", http.StatusOK, map[string]any{"id": 6}},
		{http.MethodPost, "/issues/7/assignees", http.StatusCreated, map[string]any{}},
	}
	return func(w http.ResponseWriter, r *http.Request, _ int) {
		for _, route := range routes {
			if r.Method == route.method && strings.HasSuffix(r.URL.Path, route.suffix) {
				writeJSON(t, w, route.status, route.body)
				return
			}
		}
		t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		writeJSON(t, w, http.StatusTeapot, nil)
	}
}

func TestGitHubClaim_Positive_ViewerAndComments(t *testing.T) {
	gh, _ := newFakeForge(t, claimRoutes(t))
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
}

func TestGitHubClaim_Positive_AssigneesPayload(t *testing.T) {
	gh, fake := newFakeForge(t, claimRoutes(t))
	if err := gh.AddAssignees(context.Background(), 7, []string{"operator"}); err != nil {
		t.Fatalf("assign: %v", err)
	}
	last := fake.requests[len(fake.requests)-1]
	if got, ok := last.Body["assignees"].([]any); !ok || len(got) != 1 {
		t.Fatalf("assignees payload: %v", last.Body)
	}
}

func TestGitHubClaim_Negative_ForbiddenStatusesFail(t *testing.T) {
	gh, _ := newFakeForge(t, func(w http.ResponseWriter, _ *http.Request, _ int) {
		writeJSON(t, w, http.StatusForbidden, map[string]any{"message": "no"})
	})
	ctx := context.Background()
	calls := map[string]func() error{
		"viewer":       func() error { _, err := gh.Viewer(ctx); return err },
		"list":         func() error { _, err := gh.ListIssueComments(ctx, 7); return err },
		"create":       func() error { _, err := gh.CreateIssueComment(ctx, 7, "x"); return err },
		"edit":         func() error { return gh.EditIssueComment(ctx, 6, "x") },
		"assign":       func() error { return gh.AddAssignees(ctx, 7, []string{"a"}) },
		"ensure label": func() error { return gh.EnsureLabel(ctx, Label{Name: "x", Color: "ffffff"}) },
	}
	for name, call := range calls {
		if err := call(); err == nil {
			t.Errorf("%s must fail on 403", name)
		}
	}
}

func TestGitHubClaim_Negative_BadInputIsRefusedBeforeTheWire(t *testing.T) {
	gh, fake := newFakeForge(t, claimRoutes(t))
	ctx := context.Background()
	calls := map[string]func() error{
		"list issue 0":    func() error { _, err := gh.ListIssueComments(ctx, 0); return err },
		"create issue -1": func() error { _, err := gh.CreateIssueComment(ctx, -1, "x"); return err },
		"edit comment 0":  func() error { return gh.EditIssueComment(ctx, 0, "x") },
		"no logins":       func() error { return gh.AddAssignees(ctx, 7, nil) },
		"unnamed label":   func() error { return gh.EnsureLabel(ctx, Label{}) },
	}
	for name, call := range calls {
		if err := call(); err == nil {
			t.Errorf("%s must be refused", name)
		}
	}
	if len(fake.requests) != 0 {
		t.Fatalf("refused input must not reach the forge: %d requests", len(fake.requests))
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

func TestGitHubClaim_EnsureLabel_ConcurrentCreateSurvivesAlreadyExists(t *testing.T) {
	getCalls := 0
	gh, _ := newFakeForge(t, func(w http.ResponseWriter, r *http.Request, _ int) {
		if r.Method == http.MethodGet {
			getCalls++
			if getCalls == 1 {
				writeJSON(t, w, http.StatusNotFound, map[string]any{})
				return
			}
			writeJSON(t, w, http.StatusOK, map[string]any{"name": LabelInProgress})
			return
		}
		// POST returns 422 Unprocessable Entity with GitHub's already_exists error shape
		writeJSON(t, w, http.StatusUnprocessableEntity, map[string]any{
			"message": "Validation Failed",
			"errors": []map[string]any{
				{"resource": "Label", "code": "already_exists", "field": "name"},
			},
		})
	})
	label := Label{Name: LabelInProgress, Color: "1d76db", Description: "d"}
	if err := gh.EnsureLabel(context.Background(), label); err != nil {
		t.Fatalf("concurrent create returning 422 already_exists must succeed, got %v", err)
	}
	if getCalls < 2 {
		t.Fatalf("expected re-GET after failed create, got %d GET calls", getCalls)
	}
}
