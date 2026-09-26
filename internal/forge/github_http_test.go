package forge

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/config"
)

// recordedRequest captures one request the driver sent to the fake forge.
type recordedRequest struct {
	Method  string
	Path    string
	Escaped string
	Query   string
	Body    map[string]any
	Raw     string
}

// fakeForgeServer is a hermetic stand-in for the GitHub REST API.
type fakeForgeServer struct {
	t        *testing.T
	requests []recordedRequest
	handler  func(w http.ResponseWriter, r *http.Request, index int)
}

func newFakeForge(t *testing.T, handler func(w http.ResponseWriter, r *http.Request, index int)) (*GitHubDriver, *fakeForgeServer) {
	t.Helper()
	fake := &fakeForgeServer{t: t, handler: handler}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(io.LimitReader(r.Body, maxHTTPResponseBody))
		if err != nil {
			t.Errorf("failed reading request body: %v", err)
		}
		rec := recordedRequest{
			Method:  r.Method,
			Path:    r.URL.Path,
			Escaped: r.URL.EscapedPath(),
			Query:   r.URL.RawQuery,
			Raw:     string(body),
		}
		if len(body) > 0 {
			decoded := make(map[string]any)
			if err := json.Unmarshal(body, &decoded); err == nil {
				rec.Body = decoded
			}
		}
		index := len(fake.requests)
		fake.requests = append(fake.requests, rec)
		fake.handler(w, r, index)
	}))
	t.Cleanup(srv.Close)

	// The driver must never fall back to an ambient repository.
	t.Setenv("GITHUB_REPOSITORY", "")
	gh := NewGitHubDriver("gh-token", srv.URL)
	gh.SetRepository("acme", "widgets")
	return gh, fake
}

func writeJSON(t *testing.T, w http.ResponseWriter, status int, payload any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if payload == nil {
		return
	}
	if err := json.NewEncoder(w).Encode(payload); err != nil {
		t.Errorf("failed encoding fake response: %v", err)
	}
}

// ============================================================================
// repoPath / repository resolution (positive, negative, boundary)
// ============================================================================

func TestGitHubDriver_RepoPath_Positive_ExplicitRepository(t *testing.T) {
	t.Setenv("GITHUB_REPOSITORY", "")
	gh := NewGitHubDriver("token", "")
	gh.SetRepository("acme", "widgets")

	path, err := gh.repoPath("issues")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if path != "/repos/acme/widgets/issues" {
		t.Fatalf("unexpected path: %s", path)
	}
	target, err := gh.TargetRepository()
	if err != nil || target != "acme/widgets" {
		t.Fatalf("unexpected target %q (err %v)", target, err)
	}
}

func TestGitHubDriver_RepoPath_Negative_Unresolved(t *testing.T) {
	t.Setenv("GITHUB_REPOSITORY", "")
	gh := NewGitHubDriver("token", "")

	if _, err := gh.repoPath("rulesets"); !errors.Is(err, ErrRepositoryUnresolved) {
		t.Fatalf("expected ErrRepositoryUnresolved, got %v", err)
	}
	if _, err := gh.TargetRepository(); !errors.Is(err, ErrRepositoryUnresolved) {
		t.Fatalf("expected ErrRepositoryUnresolved from TargetRepository, got %v", err)
	}

	ctx := context.Background()
	if err := gh.ReconcileProtection(ctx, "main", &config.BranchProtectionPolicy{}); !errors.Is(err, ErrRepositoryUnresolved) {
		t.Fatalf("expected ReconcileProtection to refuse an unresolved repository, got %v", err)
	}
	if _, err := gh.CreateIssue(ctx, IssueSpec{Title: "x"}); !errors.Is(err, ErrRepositoryUnresolved) {
		t.Fatalf("expected CreateIssue to refuse an unresolved repository, got %v", err)
	}
	if _, err := gh.ListIssues(ctx, "all"); !errors.Is(err, ErrRepositoryUnresolved) {
		t.Fatalf("expected ListIssues to refuse an unresolved repository, got %v", err)
	}
}

func TestGitHubDriver_RepoPath_Boundary_EnvironmentFallback(t *testing.T) {
	gh := NewGitHubDriver("token", "")

	t.Setenv("GITHUB_REPOSITORY", "envowner/envrepo")
	path, err := gh.repoPath("/issues")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if path != "/repos/envowner/envrepo/issues" {
		t.Fatalf("unexpected path: %s", path)
	}

	// A malformed coordinate must not become a partially-resolved target.
	t.Setenv("GITHUB_REPOSITORY", "no-slash")
	if _, err := gh.repoPath("issues"); !errors.Is(err, ErrRepositoryUnresolved) {
		t.Fatalf("expected ErrRepositoryUnresolved for malformed GITHUB_REPOSITORY, got %v", err)
	}
	t.Setenv("GITHUB_REPOSITORY", "owner/")
	if _, err := gh.repoPath("issues"); !errors.Is(err, ErrRepositoryUnresolved) {
		t.Fatalf("expected ErrRepositoryUnresolved for empty repo segment, got %v", err)
	}
}

// ============================================================================
// ListIssues: pagination and pull-request filtering
// ============================================================================

func issuePage(start, count int, everyThirdIsPR bool) []map[string]any {
	page := make([]map[string]any, 0, count)
	for i := 0; i < count; i++ {
		item := map[string]any{
			"number": start + i,
			"title":  fmt.Sprintf("Issue %d", start+i),
			"body":   "",
			"state":  "open",
			"labels": []map[string]string{{"name": "governance"}},
		}
		if everyThirdIsPR && (start+i)%3 == 0 {
			item["pull_request"] = map[string]string{"url": "https://example.invalid/pulls/1"}
		}
		page = append(page, item)
	}
	return page
}

func TestGitHubDriver_ListIssues_Positive_PaginatesAndDropsPullRequests(t *testing.T) {
	gh, fake := newFakeForge(t, func(w http.ResponseWriter, r *http.Request, index int) {
		switch r.URL.Query().Get("page") {
		case "1":
			writeJSON(t, w, http.StatusOK, issuePage(1, issuesPerPage, true))
		case "2":
			writeJSON(t, w, http.StatusOK, issuePage(101, 5, false))
		default:
			t.Errorf("unexpected page request: %s", r.URL.RawQuery)
			writeJSON(t, w, http.StatusOK, []map[string]any{})
		}
	})

	issues, err := gh.ListIssues(context.Background(), "all")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(fake.requests) != 2 {
		t.Fatalf("expected 2 paginated requests, got %d", len(fake.requests))
	}

	prCount := 0
	for i := 1; i <= issuesPerPage; i++ {
		if i%3 == 0 {
			prCount++
		}
	}
	want := issuesPerPage - prCount + 5
	if len(issues) != want {
		t.Fatalf("expected %d issues after dropping %d pull requests, got %d", want, prCount, len(issues))
	}
	found := false
	for _, is := range issues {
		if is.ID == 105 {
			found = true
		}
		if is.ID <= issuesPerPage && is.ID%3 == 0 {
			t.Fatalf("pull request #%d leaked into the issue listing", is.ID)
		}
	}
	if !found {
		t.Fatalf("issue #105 from the second page is missing; pagination stopped early")
	}
}

func TestGitHubDriver_ListIssues_Negative_StatusAndBodyTruncation(t *testing.T) {
	huge := strings.Repeat("A", 4096) + "\x1b[31mred"
	gh, _ := newFakeForge(t, func(w http.ResponseWriter, r *http.Request, index int) {
		w.WriteHeader(http.StatusForbidden)
		if _, err := w.Write([]byte(huge)); err != nil {
			t.Errorf("failed writing fake body: %v", err)
		}
	})

	_, err := gh.ListIssues(context.Background(), "all")
	if err == nil {
		t.Fatal("expected an error for status 403")
	}
	msg := err.Error()
	if len(msg) > 1024 {
		t.Fatalf("error message embeds an unbounded response body (%d bytes)", len(msg))
	}
	if strings.Contains(msg, "\x1b") {
		t.Fatal("error message must not carry terminal escape sequences")
	}
	if !strings.Contains(msg, "truncated") {
		t.Fatalf("expected a truncation marker, got: %s", msg)
	}
}

func TestGitHubDriver_ListIssues_Boundary_SinglePartialPage(t *testing.T) {
	gh, fake := newFakeForge(t, func(w http.ResponseWriter, r *http.Request, index int) {
		writeJSON(t, w, http.StatusOK, issuePage(1, 2, false))
	})

	issues, err := gh.ListIssues(context.Background(), "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(issues) != 2 {
		t.Fatalf("expected 2 issues, got %d", len(issues))
	}
	if len(fake.requests) != 1 {
		t.Fatalf("a short page must end pagination, got %d requests", len(fake.requests))
	}
	if !strings.Contains(fake.requests[0].Query, "state=all") {
		t.Fatalf("expected the default state to be 'all', got %q", fake.requests[0].Query)
	}
}

// ============================================================================
// ReconcileProtection
// ============================================================================

func TestGitHubDriver_ReconcileProtection_Positive_CreatesWithStatusChecks(t *testing.T) {
	gh, fake := newFakeForge(t, func(w http.ResponseWriter, r *http.Request, index int) {
		if r.Method == http.MethodGet {
			writeJSON(t, w, http.StatusOK, []map[string]any{{"id": 3, "name": "other"}})
			return
		}
		writeJSON(t, w, http.StatusCreated, map[string]any{"id": 9})
	})

	policy := &config.BranchProtectionPolicy{
		EnforceLinearHistory:       true,
		RequireSignedCommits:       true,
		RequiredApprovingReviewers: 2,
		DismissStaleReviews:        true,
	}
	if err := gh.ReconcileProtection(context.Background(), "main", policy); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(fake.requests) != 2 {
		t.Fatalf("expected a list then a create, got %d requests", len(fake.requests))
	}
	create := fake.requests[1]
	if create.Method != http.MethodPost || create.Path != "/repos/acme/widgets/rulesets" {
		t.Fatalf("unexpected create request: %s %s", create.Method, create.Path)
	}
	types := ruleTypes(t, create)
	for _, want := range []string{"required_status_checks", "required_linear_history", "required_signatures", "pull_request"} {
		if !containsString(types, want) {
			t.Fatalf("ruleset payload is missing rule %q: %v", want, types)
		}
	}
	if !strings.Contains(create.Raw, "Invariant Verification Gate") {
		t.Fatalf("required status check contexts are not pushed: %s", create.Raw)
	}
}

func TestGitHubDriver_ReconcileProtection_Positive_UpdatesExistingRuleset(t *testing.T) {
	gh, fake := newFakeForge(t, func(w http.ResponseWriter, r *http.Request, index int) {
		if r.Method == http.MethodGet {
			writeJSON(t, w, http.StatusOK, []map[string]any{{"id": 42, "name": "main-branch-protection"}})
			return
		}
		writeJSON(t, w, http.StatusOK, map[string]any{"id": 42})
	})

	policy := &config.BranchProtectionPolicy{RequiredApprovingReviewers: 1}
	if err := gh.ReconcileProtection(context.Background(), "main", policy); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	update := fake.requests[1]
	if update.Method != http.MethodPut || update.Path != "/repos/acme/widgets/rulesets/42" {
		t.Fatalf("expected an in-place update, got %s %s", update.Method, update.Path)
	}
	types := ruleTypes(t, update)
	if containsString(types, "required_linear_history") || containsString(types, "required_signatures") {
		t.Fatalf("policy booleans are ignored; payload rules: %v", types)
	}
}

func TestGitHubDriver_ReconcileProtection_Negative_StatusAndArguments(t *testing.T) {
	gh, _ := newFakeForge(t, func(w http.ResponseWriter, r *http.Request, index int) {
		if r.Method == http.MethodGet {
			writeJSON(t, w, http.StatusOK, []map[string]any{})
			return
		}
		writeJSON(t, w, http.StatusUnprocessableEntity, map[string]any{"message": "validation failed"})
	})

	ctx := context.Background()
	if err := gh.ReconcileProtection(ctx, "main", &config.BranchProtectionPolicy{}); err == nil {
		t.Fatal("expected an error for a 422 response")
	}
	if err := gh.ReconcileProtection(ctx, "", &config.BranchProtectionPolicy{}); err == nil {
		t.Fatal("expected an error for an empty branch")
	}
	if err := gh.ReconcileProtection(ctx, "main", nil); err == nil {
		t.Fatal("expected an error for a nil policy")
	}
}

func TestGitHubDriver_ReconcileProtection_Boundary_NoStatusChecksConfigured(t *testing.T) {
	gh, fake := newFakeForge(t, func(w http.ResponseWriter, r *http.Request, index int) {
		if r.Method == http.MethodGet {
			writeJSON(t, w, http.StatusOK, []map[string]any{})
			return
		}
		writeJSON(t, w, http.StatusCreated, map[string]any{"id": 1})
	})
	gh.RequiredStatusChecks = nil
	gh.RulesetName = "custom-name"

	if err := gh.ReconcileProtection(context.Background(), "main", &config.BranchProtectionPolicy{}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	create := fake.requests[1]
	if containsString(ruleTypes(t, create), "required_status_checks") {
		t.Fatal("no contexts configured must yield no required_status_checks rule")
	}
	name, isString := create.Body["name"].(string)
	if !isString || name != "custom-name" {
		t.Fatalf("expected the overridden ruleset name, got %q", name)
	}
}

func ruleTypes(t *testing.T, rec recordedRequest) []string {
	t.Helper()
	rules, ok := rec.Body["rules"].([]any)
	if !ok {
		t.Fatalf("request body carries no rules array: %s", rec.Raw)
	}
	types := make([]string, 0, len(rules))
	for _, r := range rules {
		rule, isMap := r.(map[string]any)
		if !isMap {
			continue
		}
		if typ, isStr := rule["type"].(string); isStr {
			types = append(types, typ)
		}
	}
	return types
}

func containsString(haystack []string, needle string) bool {
	for _, h := range haystack {
		if h == needle {
			return true
		}
	}
	return false
}

// ============================================================================
// ReconcileLabels
// ============================================================================

func TestGitHubDriver_ReconcileLabels_Positive_PatchAndCreate(t *testing.T) {
	gh, fake := newFakeForge(t, func(w http.ResponseWriter, r *http.Request, index int) {
		if r.Method == http.MethodPatch && strings.HasSuffix(r.URL.Path, "/labels/missing") {
			writeJSON(t, w, http.StatusNotFound, map[string]any{"message": "Not Found"})
			return
		}
		if r.Method == http.MethodPost {
			writeJSON(t, w, http.StatusCreated, map[string]any{"name": "missing"})
			return
		}
		writeJSON(t, w, http.StatusOK, map[string]any{"name": "existing"})
	})

	labels := []Label{{Name: "existing", Color: "ff0000"}, {Name: "missing", Color: "00ff00"}}
	if err := gh.ReconcileLabels(context.Background(), labels); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(fake.requests) != 3 {
		t.Fatalf("expected patch, patch and create, got %d requests", len(fake.requests))
	}
	if fake.requests[2].Method != http.MethodPost || fake.requests[2].Path != "/repos/acme/widgets/labels" {
		t.Fatalf("unexpected create request: %s %s", fake.requests[2].Method, fake.requests[2].Path)
	}
}

func TestGitHubDriver_ReconcileLabels_Negative_ForbiddenAndUnprocessable(t *testing.T) {
	gh, _ := newFakeForge(t, func(w http.ResponseWriter, r *http.Request, index int) {
		writeJSON(t, w, http.StatusForbidden, map[string]any{"message": "Resource not accessible"})
	})
	if err := gh.ReconcileLabels(context.Background(), []Label{{Name: "governance"}}); err == nil {
		t.Fatal("a 403 must not be reported as a reconciled label")
	}

	ghCreate, _ := newFakeForge(t, func(w http.ResponseWriter, r *http.Request, index int) {
		if r.Method == http.MethodPatch {
			writeJSON(t, w, http.StatusNotFound, map[string]any{"message": "Not Found"})
			return
		}
		writeJSON(t, w, http.StatusUnprocessableEntity, map[string]any{"message": "already exists"})
	})
	if err := ghCreate.ReconcileLabels(context.Background(), []Label{{Name: "governance"}}); err == nil {
		t.Fatal("a 422 on create must not be reported as a reconciled label")
	}

	if err := ghCreate.ReconcileLabels(context.Background(), []Label{{Name: ""}}); err == nil {
		t.Fatal("expected an error for an empty label name")
	}
}

func TestGitHubDriver_ReconcileLabels_Boundary_BatchLimit(t *testing.T) {
	gh, _ := newFakeForge(t, func(w http.ResponseWriter, r *http.Request, index int) {
		writeJSON(t, w, http.StatusOK, map[string]any{})
	})
	over := make([]Label, MaxLabelsLimit+1)
	for i := range over {
		over[i] = Label{Name: fmt.Sprintf("label-%d", i)}
	}
	if err := gh.ReconcileLabels(context.Background(), over); err == nil {
		t.Fatalf("expected an error above the %d label batch limit", MaxLabelsLimit)
	}
	if err := gh.ReconcileLabels(context.Background(), nil); err != nil {
		t.Fatalf("an empty batch must be a no-op, got %v", err)
	}
}

// ============================================================================
// Issue mutations
// ============================================================================

func TestGitHubDriver_CreateIssue_Positive_FullSpec(t *testing.T) {
	gh, fake := newFakeForge(t, func(w http.ResponseWriter, r *http.Request, index int) {
		if strings.HasSuffix(r.URL.Path, "/milestones") {
			writeJSON(t, w, http.StatusOK, []map[string]any{{"number": 7, "title": "v1"}})
			return
		}
		writeJSON(t, w, http.StatusCreated, map[string]any{"number": 11, "url": "u", "state": "open"})
	})

	spec := IssueSpec{
		Title:     "Adopt praetor",
		Body:      "Depends-On: #4",
		Labels:    []string{"governance"},
		Assignees: []string{"alice"},
		Milestone: "v1",
	}
	res, err := gh.CreateIssue(context.Background(), spec)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Number != 11 {
		t.Fatalf("unexpected issue number %d", res.Number)
	}
	create := fake.requests[len(fake.requests)-1]
	if !strings.Contains(create.Raw, `"assignees":["alice"]`) {
		t.Fatalf("assignees were dropped from the payload: %s", create.Raw)
	}
	milestone, ok := create.Body["milestone"].(float64)
	if !ok || int(milestone) != 7 {
		t.Fatalf("milestone was not resolved to its number: %s", create.Raw)
	}
}

func TestGitHubDriver_CreateIssue_Negative_TitleStatusAndMilestone(t *testing.T) {
	gh, _ := newFakeForge(t, func(w http.ResponseWriter, r *http.Request, index int) {
		if strings.HasSuffix(r.URL.Path, "/milestones") {
			writeJSON(t, w, http.StatusOK, []map[string]any{})
			return
		}
		writeJSON(t, w, http.StatusOK, map[string]any{"number": 1})
	})
	ctx := context.Background()

	if _, err := gh.CreateIssue(ctx, IssueSpec{}); err == nil {
		t.Fatal("expected an error for a missing title")
	}
	if _, err := gh.CreateIssue(ctx, IssueSpec{Title: "x", Milestone: "nope"}); err == nil {
		t.Fatal("expected an error for an unknown milestone")
	}
	if _, err := gh.CreateIssue(ctx, IssueSpec{Title: "x"}); err == nil {
		t.Fatal("expected an error when the API answers 200 instead of 201")
	}
}

func TestGitHubDriver_CreateIssue_Boundary_MalformedResponse(t *testing.T) {
	gh, _ := newFakeForge(t, func(w http.ResponseWriter, r *http.Request, index int) {
		w.WriteHeader(http.StatusCreated)
		if _, err := w.Write([]byte("{not json")); err != nil {
			t.Errorf("write failed: %v", err)
		}
	})
	if _, err := gh.CreateIssue(context.Background(), IssueSpec{Title: "x"}); err == nil {
		t.Fatal("expected a parse error for a malformed response")
	}
}

func TestGitHubDriver_UpdateIssue_3D(t *testing.T) {
	gh, fake := newFakeForge(t, func(w http.ResponseWriter, r *http.Request, index int) {
		if index == 0 {
			writeJSON(t, w, http.StatusOK, map[string]any{"number": 5})
			return
		}
		writeJSON(t, w, http.StatusNotFound, map[string]any{"message": "Not Found"})
	})
	ctx := context.Background()

	if err := gh.UpdateIssue(ctx, 5, []string{"a", "b"}, "closed"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	patch := fake.requests[0]
	if patch.Method != http.MethodPatch || patch.Path != "/repos/acme/widgets/issues/5" {
		t.Fatalf("unexpected request: %s %s", patch.Method, patch.Path)
	}
	if !strings.Contains(patch.Raw, `"labels":["a","b"]`) || !strings.Contains(patch.Raw, `"state":"closed"`) {
		t.Fatalf("unexpected payload: %s", patch.Raw)
	}

	if err := gh.UpdateIssue(ctx, 5, []string{"a"}, ""); err == nil {
		t.Fatal("expected an error for a 404 response")
	}
	if err := gh.UpdateIssue(ctx, 0, []string{"a"}, ""); err == nil {
		t.Fatal("expected an error for issue number 0")
	}
	if err := gh.UpdateIssue(ctx, 5, nil, ""); err == nil {
		t.Fatal("expected an error when there is nothing to update")
	}
}

func TestGitHubDriver_AddLabelsRemoveLabel_3D(t *testing.T) {
	gh, fake := newFakeForge(t, func(w http.ResponseWriter, r *http.Request, index int) {
		switch {
		case r.Method == http.MethodPost:
			writeJSON(t, w, http.StatusOK, []map[string]any{{"name": "status/ready-for-work"}})
		case r.Method == http.MethodDelete && strings.HasSuffix(r.URL.Path, "gone"):
			writeJSON(t, w, http.StatusNotFound, map[string]any{"message": "Label does not exist"})
		case r.Method == http.MethodDelete && strings.HasSuffix(r.URL.Path, "boom"):
			writeJSON(t, w, http.StatusForbidden, map[string]any{"message": "no"})
		default:
			writeJSON(t, w, http.StatusOK, []map[string]any{})
		}
	})
	ctx := context.Background()

	if err := gh.AddLabels(ctx, 5, []string{"status/ready-for-work"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	add := fake.requests[0]
	if add.Method != http.MethodPost || add.Path != "/repos/acme/widgets/issues/5/labels" {
		t.Fatalf("unexpected add request: %s %s", add.Method, add.Path)
	}
	if err := gh.RemoveLabel(ctx, 5, "status/blocked"); err != nil {
		t.Fatalf("unexpected error removing a label: %v", err)
	}
	if fake.requests[1].Escaped != "/repos/acme/widgets/issues/5/labels/status%2Fblocked" {
		t.Fatalf("label name is not path-escaped: %s", fake.requests[1].Escaped)
	}
	if err := gh.RemoveLabel(ctx, 5, "gone"); err != nil {
		t.Fatalf("a missing label must not be an error, got %v", err)
	}
	if err := gh.RemoveLabel(ctx, 5, "boom"); err == nil {
		t.Fatal("expected an error for a 403 response")
	}
	if err := gh.AddLabels(ctx, 5, nil); err == nil {
		t.Fatal("expected an error for an empty label set")
	}
	if err := gh.RemoveLabel(ctx, 0, "x"); err == nil {
		t.Fatal("expected an error for issue number 0")
	}
}

// ============================================================================
// Pull requests and status checks
// ============================================================================

func TestGitHubDriver_CreatePullRequestAndStatusCheck_3D(t *testing.T) {
	gh, fake := newFakeForge(t, func(w http.ResponseWriter, r *http.Request, index int) {
		if strings.HasSuffix(r.URL.Path, "/pulls") {
			writeJSON(t, w, http.StatusCreated, map[string]any{"number": 4, "url": "u", "state": "open"})
			return
		}
		if strings.Contains(r.URL.Path, "/statuses/") {
			writeJSON(t, w, http.StatusCreated, map[string]any{"state": "success"})
			return
		}
		writeJSON(t, w, http.StatusInternalServerError, map[string]any{})
	})
	ctx := context.Background()

	pr, err := gh.CreatePullRequest(ctx, PRRequest{Title: "t", Head: "h", Base: "main"})
	if err != nil || pr.Number != 4 {
		t.Fatalf("unexpected pull request result %+v (err %v)", pr, err)
	}
	if draft, ok := fake.requests[0].Body["draft"].(bool); !ok || draft {
		t.Fatalf("default pull request draft flag = %v (present=%v), want false", draft, ok)
	}
	if _, err := gh.CreatePullRequest(ctx, PRRequest{Title: "draft", Head: "h", Base: "main", Draft: true}); err != nil {
		t.Fatalf("unexpected draft pull request error: %v", err)
	}
	if draft, ok := fake.requests[1].Body["draft"].(bool); !ok || !draft {
		t.Fatalf("draft pull request draft flag = %v (present=%v), want true", draft, ok)
	}
	if _, err := gh.CreatePullRequest(ctx, PRRequest{Title: "t"}); err == nil {
		t.Fatal("expected an error for a missing head and base")
	}

	if err := gh.PostStatusCheck(ctx, "abc123", CheckRun{Name: "verify", Conclusion: "success"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	status := fake.requests[len(fake.requests)-1]
	if !strings.Contains(status.Raw, `"state":"success"`) {
		t.Fatalf("unexpected status payload: %s", status.Raw)
	}
	if err := gh.PostStatusCheck(ctx, "", CheckRun{Name: "verify"}); err == nil {
		t.Fatal("expected an error for an empty commit SHA")
	}
}

// ============================================================================
// Helpers
// ============================================================================

func TestParseGitHubIssues_3D(t *testing.T) {
	raw := []byte(`[
	  {"number":1,"title":"a","body":"Depends-On: #9","state":"open","labels":[{"name":"governance"}]},
	  {"number":2,"title":"pr","body":"","state":"open","labels":[],"pull_request":{"url":"x"}}
	]`)
	specs, count, err := parseGitHubIssues(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if count != 2 {
		t.Fatalf("expected a raw count of 2, got %d", count)
	}
	if len(specs) != 1 || specs[0].ID != 1 {
		t.Fatalf("expected only the issue to survive, got %+v", specs)
	}
	if len(specs[0].Labels) != 1 || specs[0].Labels[0] != "governance" {
		t.Fatalf("labels were not flattened: %+v", specs[0].Labels)
	}
	if len(specs[0].DependsOn) != 1 {
		t.Fatalf("dependencies were not extracted: %+v", specs[0].DependsOn)
	}

	if _, _, err := parseGitHubIssues([]byte("{not json")); err == nil {
		t.Fatal("expected a parse error")
	}
	specs, count, err = parseGitHubIssues([]byte("[]"))
	if err != nil || count != 0 || len(specs) != 0 {
		t.Fatalf("expected an empty result, got %+v / %d / %v", specs, count, err)
	}
}
