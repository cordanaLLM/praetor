package forge

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/cordanaLLM/praetor/internal/config"
	"github.com/cordanaLLM/praetor/internal/util"
)

// GiteaDriver implements the credential handling for Gitea / Forgejo instances. The
// enforcement methods are deliberately unimplemented: returning success without performing
// the operation would report governance that does not exist, so every one of them fails
// with ErrNotImplemented until a real Gitea client exists.
type GiteaDriver struct {
	Token      string
	Endpoint   string
	Owner      string
	Repo       string
	HTTPClient *http.Client
}

// NewGiteaDriver initializes a Gitea / Forgejo driver.
func NewGiteaDriver(token string, endpoint string) *GiteaDriver {
	if endpoint == "" {
		endpoint = "https://gitea.com/api/v1"
	}
	return &GiteaDriver{
		Token:    token,
		Endpoint: endpoint,
	}
}

// SetRepository sets target repository owner and name.
func (gt *GiteaDriver) SetRepository(owner, repo string) {
	gt.Owner = owner
	gt.Repo = repo
}

func (gt *GiteaDriver) Name() string {
	return "gitea"
}

func (gt *GiteaDriver) Authenticate(ctx context.Context) error {
	if ctx == nil {
		return errors.New("gitea authentication: context cannot be nil")
	}
	if gt.Token == "" {
		return errors.New("gitea authentication failed: token is empty")
	}
	return nil
}

func (gt *GiteaDriver) unsupported(method string) error {
	return fmt.Errorf("gitea driver: %s: %w", method, ErrNotImplemented)
}

func (gt *GiteaDriver) ReconcileProtection(ctx context.Context, branch string, policy *config.BranchProtectionPolicy) error {
	if err := gt.Authenticate(ctx); err != nil {
		return err
	}
	return gt.unsupported("ReconcileProtection")
}

func (gt *GiteaDriver) ReconcileLabels(ctx context.Context, labels []Label) error {
	if err := gt.Authenticate(ctx); err != nil {
		return err
	}
	return gt.unsupported("ReconcileLabels")
}

func (gt *GiteaDriver) PostStatusCheck(ctx context.Context, commitSHA string, check CheckRun) error {
	if err := gt.Authenticate(ctx); err != nil {
		return err
	}
	return gt.unsupported("PostStatusCheck")
}

func (gt *GiteaDriver) CreatePullRequest(ctx context.Context, req PRRequest) (*PRResponse, error) {
	return nil, stubWrite(ctx, gt.Authenticate, gt.unsupported, "CreatePullRequest", req.Title, req.Body)
}

func (gt *GiteaDriver) CreateIssue(ctx context.Context, spec IssueSpec) (*IssueResponse, error) {
	return nil, stubWrite(ctx, gt.Authenticate, gt.unsupported, "CreateIssue", spec.Title, spec.Body)
}

func (gt *GiteaDriver) ListIssues(ctx context.Context, state string) ([]IssueSpec, error) {
	if err := gt.Authenticate(ctx); err != nil {
		return nil, err
	}
	return nil, gt.unsupported("ListIssues")
}

func (gt *GiteaDriver) UpdateIssue(ctx context.Context, number int, labels []string, state string) error {
	if err := gt.Authenticate(ctx); err != nil {
		return err
	}
	return gt.unsupported("UpdateIssue")
}

func (gt *GiteaDriver) EditIssueBody(ctx context.Context, number int, body string) error {
	return stubWrite(ctx, gt.Authenticate, gt.unsupported, "EditIssueBody", body)
}

func (gt *GiteaDriver) WorkflowPermissions(ctx context.Context) (LiveWorkflowPermissions, error) {
	if err := gt.Authenticate(ctx); err != nil {
		return LiveWorkflowPermissions{}, err
	}
	return LiveWorkflowPermissions{}, gt.unsupported("WorkflowPermissions")
}

func (gt *GiteaDriver) WorkflowRunHistory(ctx context.Context, workflow, branch string) (WorkflowRunHistory, error) {
	if err := gt.Authenticate(ctx); err != nil {
		return WorkflowRunHistory{}, err
	}
	return WorkflowRunHistory{}, gt.unsupported("WorkflowRunHistory")
}

func (gt *GiteaDriver) WorkflowRunJobs(ctx context.Context, runID int64) ([]WorkflowJobRecord, error) {
	if err := gt.Authenticate(ctx); err != nil {
		return nil, err
	}
	return nil, gt.unsupported("WorkflowRunJobs")
}

func (gt *GiteaDriver) ListMergedPullRequests(ctx context.Context, _ MergedPullRequestQuery) (MergedPullRequestList, error) {
	if err := gt.Authenticate(ctx); err != nil {
		return MergedPullRequestList{}, err
	}
	return MergedPullRequestList{}, gt.unsupported("ListMergedPullRequests")
}

// ForgejoAPIBase normalizes a Forgejo / Gitea host or endpoint URL into its /api/v1 root.
func ForgejoAPIBase(endpoint string) string {
	base := strings.TrimRight(strings.TrimSpace(endpoint), "/")
	if base == "" {
		return "https://codeberg.org/api/v1"
	}
	if !strings.HasSuffix(base, "/api/v1") {
		return base + "/api/v1"
	}
	return base
}

// ListPullRequestsByHead fetches up to one bounded page of pull requests (all states) whose
// head branch is branch from the Forgejo REST API (/api/v1/repos/{owner}/{repo}/pulls).
func (gt *GiteaDriver) ListPullRequestsByHead(ctx context.Context, branch string) ([]PullRequestHead, error) {
	if ctx == nil {
		return nil, errors.New("forgejo pull requests by head: context cannot be nil")
	}
	branch = strings.TrimSpace(branch)
	if branch == "" {
		return nil, errors.New("forgejo pull requests by head: branch is required")
	}
	if err := util.ValidateGitHubRepositoryIdentity(gt.Owner, gt.Repo); err != nil {
		return nil, fmt.Errorf("forgejo pull requests by head %q: %w", branch, err)
	}
	ctx, cancel := context.WithTimeout(ctx, defaultHTTPTimeout)
	defer cancel()
	body, status, err := gt.fetchHeadPulls(ctx, branch)
	if err != nil {
		return nil, fmt.Errorf("failed listing forgejo pull requests for head %q: %w", branch, err)
	}
	if status != http.StatusOK {
		return nil, fmt.Errorf("unexpected status %d listing forgejo pull requests for head %q: %s",
			status, branch, util.BodyPreview(body))
	}
	return ParseHeadPullRequests(body, branch)
}

// fetchHeadPulls fetches the raw JSON body and status code for head branch pulls from Forgejo.
func (gt *GiteaDriver) fetchHeadPulls(ctx context.Context, branch string) (respBody []byte, statusCode int, err error) {
	endpoint := fmt.Sprintf("%s/repos/%s/%s/pulls?state=all&head=%s&page=1&limit=%d",
		ForgejoAPIBase(gt.Endpoint), url.PathEscape(gt.Owner), url.PathEscape(gt.Repo),
		url.QueryEscape(branch), HeadPullRequestsPerPage)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, 0, fmt.Errorf("construct forgejo pull request lookup: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "praetor-governance-engine")
	if gt.Token != "" {
		req.Header.Set("Authorization", "token "+gt.Token)
	}
	client := gt.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: defaultHTTPTimeout}
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer func() {
		if closeErr := resp.Body.Close(); closeErr != nil {
			err = errors.Join(err, fmt.Errorf("close response body (%s %s): %w", http.MethodGet, req.URL.Path, closeErr))
		}
	}()
	return readBoundedResponseBody(resp.Body, resp.StatusCode)
}
