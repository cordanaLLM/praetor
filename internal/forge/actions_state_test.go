// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package forge

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"
)

const (
	repoPermissionsPath = "/repos/acme/widgets/actions/permissions/workflow"
	orgPermissionsPath  = "/orgs/acme/actions/permissions/workflow"
	ciRunsPath          = "/repos/acme/widgets/actions/workflows/ci.yml/runs"
)

// permissionsForge answers the two workflow permission paths with the given statuses and
// bodies, and fails the test on any other request or any method but GET.
func permissionsForge(t *testing.T, repoStatus int, repoBody any, orgStatus int, orgBody any) (*GitHubDriver, *fakeForgeServer) {
	t.Helper()
	return newFakeForge(t, func(w http.ResponseWriter, r *http.Request, _ int) {
		if r.Method != http.MethodGet {
			t.Errorf("an Actions read sent %s %s", r.Method, r.URL.Path)
		}
		switch r.URL.Path {
		case repoPermissionsPath:
			writeJSON(t, w, repoStatus, repoBody)
		case orgPermissionsPath:
			writeJSON(t, w, orgStatus, orgBody)
		default:
			t.Errorf("unexpected request %s", r.URL.Path)
			writeJSON(t, w, http.StatusTeapot, nil)
		}
	})
}

func permissionsBody(permissions string, approve bool) map[string]any {
	return map[string]any{"default_workflow_permissions": permissions, "can_approve_pull_request_reviews": approve}
}

// Positive (#611): the driver reads the repository and the organisation value, by GET, from the
// documented paths.
func TestWorkflowPermissions_Positive_ReadsRepositoryAndOrganisation(t *testing.T) {
	gh, fake := permissionsForge(t, http.StatusOK, permissionsBody("read", false), http.StatusOK, permissionsBody("read", true))
	live, err := gh.WorkflowPermissions(t.Context())
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if live.Repository != (WorkflowPermissions{DefaultWorkflowPermissions: "read"}) {
		t.Fatalf("repository value = %+v", live.Repository)
	}
	if live.Organisation == nil || !live.Organisation.CanApprovePullRequestReviews || live.OrganisationUnread != "" {
		t.Fatalf("organisation value = %+v (%q)", live.Organisation, live.OrganisationUnread)
	}
	if len(fake.requests) != 2 || fake.requests[0].Path != repoPermissionsPath || fake.requests[1].Path != orgPermissionsPath {
		t.Fatalf("requests = %+v", fake.requests)
	}
}

// Negative (#611): a refused repository read is ErrActionsNotReadable, so the audit reports the
// check as not made; a server error or a value that is neither read nor write is another error.
func TestWorkflowPermissions_Negative_RefusedAndMalformedReads(t *testing.T) {
	for _, status := range []int{http.StatusUnauthorized, http.StatusForbidden, http.StatusNotFound} {
		gh, fake := permissionsForge(t, status, map[string]any{"message": "Must have admin rights"}, http.StatusOK, nil)
		if _, err := gh.WorkflowPermissions(t.Context()); !errors.Is(err, ErrActionsNotReadable) {
			t.Fatalf("status %d returned %v, want ErrActionsNotReadable", status, err)
		}
		if len(fake.requests) != 1 {
			t.Fatalf("status %d still asked the organisation: %+v", status, fake.requests)
		}
	}
	for name, gh := range map[string]*GitHubDriver{
		"server error": first(permissionsForge(t, http.StatusBadGateway, nil, http.StatusOK, nil)),
		"admin value":  first(permissionsForge(t, http.StatusOK, permissionsBody("admin", false), http.StatusOK, nil)),
		"org error":    first(permissionsForge(t, http.StatusOK, permissionsBody("read", false), http.StatusInternalServerError, nil)),
	} {
		if _, err := gh.WorkflowPermissions(t.Context()); err == nil || errors.Is(err, ErrActionsNotReadable) {
			t.Fatalf("%s returned %v, want an error other than ErrActionsNotReadable", name, err)
		}
	}
	unresolved := NewGitHubDriver("token", "http://127.0.0.1:1")
	t.Setenv("GITHUB_REPOSITORY", "")
	if _, err := unresolved.WorkflowPermissions(t.Context()); !errors.Is(err, ErrRepositoryUnresolved) {
		t.Fatalf("a driver without a repository returned %v", err)
	}
}

// Boundary (#611): a user-owned repository has no organisation, and a token may read the
// repository and not the organisation; either leaves Organisation nil with the reason.
func TestWorkflowPermissions_Boundary_OrganisationNotReadable(t *testing.T) {
	for _, status := range []int{http.StatusNotFound, http.StatusForbidden} {
		gh, _ := permissionsForge(t, http.StatusOK, permissionsBody("write", true), status, nil)
		live, err := gh.WorkflowPermissions(t.Context())
		if err != nil {
			t.Fatalf("organisation status %d failed the read: %v", status, err)
		}
		if live.Organisation != nil || !strings.Contains(live.OrganisationUnread, "acme") {
			t.Fatalf("organisation status %d: %+v", status, live)
		}
	}
}

func first(gh *GitHubDriver, _ *fakeForgeServer) *GitHubDriver { return gh }

// runsBody renders one runs page: total, and one run per conclusion, numbered downwards.
func runsBody(total int, conclusions ...string) map[string]any {
	runs := make([]map[string]any, 0, len(conclusions))
	for i, conclusion := range conclusions {
		runs = append(runs, map[string]any{
			"run_number": len(conclusions) - i, "event": "push", "head_branch": "main", "status": "completed",
			"conclusion": conclusion, "created_at": fmt.Sprintf("2026-09-%02dT07:00:00Z", 28-i),
			"html_url": fmt.Sprintf("https://forge.test/runs/%d", len(conclusions)-i),
		})
	}
	return map[string]any{"total_count": total, "workflow_runs": runs}
}

// runsForge answers the runs path with one response per request, in order.
func runsForge(t *testing.T, responses ...func(w http.ResponseWriter)) (*GitHubDriver, *fakeForgeServer) {
	t.Helper()
	return newFakeForge(t, func(w http.ResponseWriter, r *http.Request, index int) {
		if r.Method != http.MethodGet || index >= len(responses) {
			t.Errorf("unexpected request %d: %s %s?%s", index, r.Method, r.URL.EscapedPath(), r.URL.RawQuery)
			writeJSON(t, w, http.StatusTeapot, nil)
			return
		}
		responses[index](w)
	})
}

func respond(t *testing.T, status int, body any) func(w http.ResponseWriter) {
	return func(w http.ResponseWriter) { writeJSON(t, w, status, body) }
}

// Positive (#612): one request reads the latest completed runs on the branch, newest first.
func TestWorkflowRunHistory_Positive_CompletedRunsOnBranch(t *testing.T) {
	gh, fake := runsForge(t, respond(t, http.StatusOK, runsBody(3, "failure", "failure", "success")))
	history, err := gh.WorkflowRunHistory(t.Context(), "ci.yml", "main")
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !history.Known || len(history.Completed) != 3 || history.Completed[0].Conclusion != "failure" ||
		history.Completed[0].URL != "https://forge.test/runs/3" {
		t.Fatalf("history = %+v", history)
	}
	request := fake.requests[0]
	if len(fake.requests) != 1 || request.Path != ciRunsPath ||
		request.Query != "branch=main&per_page=20&status=completed" {
		t.Fatalf("requests = %+v", fake.requests)
	}
}

// Negative (#612): 401 and 403 are ErrActionsNotReadable, a server error is another error, a
// 404 is a workflow the forge does not know, and an empty workflow or branch is refused before
// any request.
func TestWorkflowRunHistory_Negative_RefusedUnknownAndInvalid(t *testing.T) {
	for _, status := range []int{http.StatusUnauthorized, http.StatusForbidden} {
		gh, _ := runsForge(t, respond(t, status, nil))
		if _, err := gh.WorkflowRunHistory(t.Context(), "ci.yml", "main"); !errors.Is(err, ErrActionsNotReadable) {
			t.Fatalf("status %d returned %v", status, err)
		}
	}
	gh, _ := runsForge(t, respond(t, http.StatusServiceUnavailable, nil))
	if _, err := gh.WorkflowRunHistory(t.Context(), "ci.yml", "main"); err == nil || errors.Is(err, ErrActionsNotReadable) {
		t.Fatalf("a server error returned %v", err)
	}
	gh, _ = runsForge(t, respond(t, http.StatusNotFound, nil))
	history, err := gh.WorkflowRunHistory(t.Context(), "ci.yml", "main")
	if err != nil || history.Known {
		t.Fatalf("an unknown workflow returned %+v, %v", history, err)
	}
	gh, fake := runsForge(t)
	for _, pair := range [][2]string{{"", "main"}, {"ci.yml", ""}} {
		if _, err := gh.WorkflowRunHistory(t.Context(), pair[0], pair[1]); err == nil {
			t.Fatalf("workflow %q on branch %q was asked about", pair[0], pair[1])
		}
	}
	if len(fake.requests) != 0 {
		t.Fatalf("an invalid question reached the forge: %+v", fake.requests)
	}
}

// Boundary (#612): with no completed run on the branch, a second request on every branch tells
// a workflow that never ran (total 0) from one that runs elsewhere; a page longer than the
// window is cut to it; a workflow name is escaped into one path segment.
func TestWorkflowRunHistory_Boundary_SecondQuestionWindowAndEscaping(t *testing.T) {
	gh, fake := runsForge(t, respond(t, http.StatusOK, runsBody(0)), respond(t, http.StatusOK, runsBody(0)))
	history, err := gh.WorkflowRunHistory(t.Context(), "release.yml", "main")
	if err != nil || !history.Known || history.Total != 0 || history.Latest != nil {
		t.Fatalf("a never-run workflow returned %+v, %v", history, err)
	}
	if len(fake.requests) != 2 || fake.requests[1].Query != "per_page=1" {
		t.Fatalf("requests = %+v", fake.requests)
	}
	gh, _ = runsForge(t, respond(t, http.StatusOK, runsBody(0)), respond(t, http.StatusOK, runsBody(7, "success")))
	history, err = gh.WorkflowRunHistory(t.Context(), "release.yml", "main")
	if err != nil || history.Total != 7 || history.Latest == nil || history.Latest.Conclusion != "success" {
		t.Fatalf("a workflow that runs elsewhere returned %+v, %v", history, err)
	}
	long := make([]string, workflowRunWindow+5)
	for i := range long {
		long[i] = "failure"
	}
	gh, fake = runsForge(t, respond(t, http.StatusOK, runsBody(len(long), long...)))
	history, err = gh.WorkflowRunHistory(t.Context(), "nightly build.yml", "main")
	if err != nil || len(history.Completed) != workflowRunWindow {
		t.Fatalf("an over-long page returned %d runs, %v", len(history.Completed), err)
	}
	if want := "/repos/acme/widgets/actions/workflows/nightly%20build.yml/runs"; fake.requests[0].Escaped != want {
		t.Fatalf("escaped path = %s, want %s", fake.requests[0].Escaped, want)
	}
}
