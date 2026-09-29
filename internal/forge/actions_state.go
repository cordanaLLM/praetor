// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package forge

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"

	"github.com/cordanaLLM/praetor/internal/util"
)

// workflowRunWindow is how many of a workflow's latest completed runs on a branch one question
// reads (HISS-02: one page). A failure streak longer than the window is reported as at least
// this long.
const workflowRunWindow = 20

// ErrActionsNotReadable marks an Actions read the forge refused: 401, 403 or 404. The token is
// missing a scope, the repository is not visible to it, or a rate limit answered; none of them
// says anything about the setting itself, so a caller reports the check as not made.
var ErrActionsNotReadable = errors.New("the forge did not show this Actions state to the token")

// LiveWorkflowPermissions is what the forge reports about a repository's Actions workflow
// permissions.
type LiveWorkflowPermissions struct {
	// Repository is the repository's effective value.
	Repository WorkflowPermissions
	// Organisation is the owning organisation's value, or nil when it was not read: the owner
	// is a user account, or the token cannot read organisation settings. OrganisationUnread
	// then says why.
	Organisation       *WorkflowPermissions
	OrganisationUnread string
}

// WorkflowRunRecord is one workflow run as the forge lists it.
type WorkflowRunRecord struct {
	Number     int    `json:"run_number"`
	Event      string `json:"event"`
	HeadBranch string `json:"head_branch"`
	Status     string `json:"status"`
	Conclusion string `json:"conclusion"`
	CreatedAt  string `json:"created_at"`
	URL        string `json:"html_url"`
}

// WorkflowRunHistory is what the forge reports about one workflow's runs.
type WorkflowRunHistory struct {
	// Known is false when the forge has no workflow of that file name: the file is not on
	// its default branch yet. Nothing else is set then.
	Known bool
	// Completed holds the latest completed runs on the branch asked about, newest first, at
	// most workflowRunWindow of them.
	Completed []WorkflowRunRecord
	// Total counts the workflow's runs on every branch and in every state, and Latest is the
	// newest of them. Both are read only when Completed is empty: Total is 0 then for a
	// workflow that has never run.
	Total  int
	Latest *WorkflowRunRecord
}

// workflowRunsPage is one page of GET .../actions/workflows/{workflow}/runs.
type workflowRunsPage struct {
	TotalCount int                 `json:"total_count"`
	Runs       []WorkflowRunRecord `json:"workflow_runs"`
}

// WorkflowPermissions reads GET /repos/{owner}/{repo}/actions/permissions/workflow and, for
// the owner, GET /orgs/{owner}/actions/permissions/workflow. The repository value is required:
// ErrActionsNotReadable when the forge refuses it. The organisation value is best effort: a
// user-owned repository has none, and a token that may read the repository need not read its
// organisation, so a refusal there leaves Organisation nil with the reason.
func (g *GitHubDriver) WorkflowPermissions(ctx context.Context) (LiveWorkflowPermissions, error) {
	owner, _, err := g.resolveRepository()
	if err != nil {
		return LiveWorkflowPermissions{}, err
	}
	path, err := g.repoPath("actions/permissions/workflow")
	if err != nil {
		return LiveWorkflowPermissions{}, err
	}
	repository, err := g.readWorkflowPermissions(ctx, path)
	if err != nil {
		return LiveWorkflowPermissions{}, fmt.Errorf("read repository workflow permissions: %w", err)
	}
	live := LiveWorkflowPermissions{Repository: repository}
	organisation, err := g.readWorkflowPermissions(ctx, "/orgs/"+url.PathEscape(owner)+"/actions/permissions/workflow")
	switch {
	case err == nil:
		live.Organisation = &organisation
	case errors.Is(err, ErrActionsNotReadable):
		live.OrganisationUnread = fmt.Sprintf("%s is not an organisation, or the token cannot read its settings (%v)", owner, err)
	default:
		return LiveWorkflowPermissions{}, fmt.Errorf("read organisation workflow permissions: %w", err)
	}
	return live, nil
}

// readWorkflowPermissions reads and checks one workflow permissions document.
func (g *GitHubDriver) readWorkflowPermissions(ctx context.Context, path string) (WorkflowPermissions, error) {
	body, status, err := g.sendRequest(ctx, http.MethodGet, path, nil)
	if err != nil {
		return WorkflowPermissions{}, err
	}
	if err := actionsReadStatus(status, body); err != nil {
		return WorkflowPermissions{}, err
	}
	var permissions WorkflowPermissions
	if err := json.Unmarshal(body, &permissions); err != nil {
		return WorkflowPermissions{}, fmt.Errorf("decode workflow permissions: %w", err)
	}
	if permissions.DefaultWorkflowPermissions != "read" && permissions.DefaultWorkflowPermissions != "write" {
		return WorkflowPermissions{}, fmt.Errorf("workflow permissions name no read or write default: %q",
			util.TruncateExcerpt(permissions.DefaultWorkflowPermissions, 80))
	}
	return permissions, nil
}

// actionsReadStatus maps a refused Actions read to ErrActionsNotReadable and any other status
// but 200 to an error that shows the body.
func actionsReadStatus(status int, body []byte) error {
	switch status {
	case http.StatusOK:
		return nil
	case http.StatusUnauthorized, http.StatusForbidden, http.StatusNotFound:
		return fmt.Errorf("status %d: %w", status, ErrActionsNotReadable)
	default:
		return fmt.Errorf("unexpected status %d: %s", status, util.BodyPreview(body))
	}
}

// WorkflowRunHistory reads the latest completed runs of workflow on branch through
// GET /repos/{owner}/{repo}/actions/workflows/{workflow}/runs, one page of workflowRunWindow.
// When there is none it asks once more, on every branch and in every state, for one run, so a
// workflow that never ran is told apart from one that runs only off the branch. A workflow the
// forge does not know answers 404 and is returned as not Known; 401 and 403 are
// ErrActionsNotReadable.
func (g *GitHubDriver) WorkflowRunHistory(ctx context.Context, workflow, branch string) (WorkflowRunHistory, error) {
	if workflow == "" || branch == "" {
		return WorkflowRunHistory{}, errors.New("read workflow runs: workflow and branch are required")
	}
	base, err := g.repoPath("actions/workflows/" + url.PathEscape(workflow) + "/runs")
	if err != nil {
		return WorkflowRunHistory{}, err
	}
	query := url.Values{"branch": {branch}, "status": {"completed"}, "per_page": {fmt.Sprint(workflowRunWindow)}}
	page, known, err := g.readWorkflowRuns(ctx, base+"?"+query.Encode())
	if err != nil || !known {
		return WorkflowRunHistory{}, wrapRunsError(workflow, err)
	}
	history := WorkflowRunHistory{Known: true, Completed: page.Runs}
	if len(history.Completed) > workflowRunWindow {
		history.Completed = history.Completed[:workflowRunWindow]
	}
	if len(history.Completed) > 0 {
		return history, nil
	}
	anywhere, known, err := g.readWorkflowRuns(ctx, base+"?per_page=1")
	if err != nil || !known {
		return WorkflowRunHistory{}, wrapRunsError(workflow, err)
	}
	history.Total = anywhere.TotalCount
	if len(anywhere.Runs) > 0 {
		history.Latest = &anywhere.Runs[0]
	}
	return history, nil
}

// readWorkflowRuns reads one runs page. A 404 is a workflow the forge does not know: known is
// false and err nil.
func (g *GitHubDriver) readWorkflowRuns(ctx context.Context, path string) (page workflowRunsPage, known bool, err error) {
	body, status, err := g.sendRequest(ctx, http.MethodGet, path, nil)
	if err != nil {
		return workflowRunsPage{}, false, err
	}
	if status == http.StatusNotFound {
		return workflowRunsPage{}, false, nil
	}
	if err := actionsReadStatus(status, body); err != nil {
		return workflowRunsPage{}, false, err
	}
	if err := json.Unmarshal(body, &page); err != nil {
		return workflowRunsPage{}, false, fmt.Errorf("decode workflow runs: %w", err)
	}
	return page, true, nil
}

// wrapRunsError names the workflow on a failed read; a nil error stays nil.
func wrapRunsError(workflow string, err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("read runs of %s: %w", workflow, err)
}
