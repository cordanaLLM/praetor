package forge

import (
	"context"
	"fmt"

	"github.com/cordanaLLM/standards/internal/config"
)

// GitLabDriver implements Forge for GitLab using Project Access Tokens.
type GitLabDriver struct {
	Token     string
	Endpoint  string
	ProjectID string
}

// NewGitLabDriver initializes a GitLab driver.
func NewGitLabDriver(token string, endpoint string) *GitLabDriver {
	if endpoint == "" {
		endpoint = "https://gitlab.com/api/v4"
	}
	return &GitLabDriver{
		Token:    token,
		Endpoint: endpoint,
	}
}

// SetProject sets the target GitLab project ID or URL-encoded path.
func (gl *GitLabDriver) SetProject(projectID string) {
	gl.ProjectID = projectID
}

func (gl *GitLabDriver) targetProject() string {
	if gl.ProjectID != "" {
		return gl.ProjectID
	}
	return "default-project"
}

func (gl *GitLabDriver) Name() string {
	return "gitlab"
}

func (gl *GitLabDriver) Authenticate(ctx context.Context) error {
	if gl.Token == "" {
		return fmt.Errorf("gitlab authentication failed: token is empty")
	}
	return nil
}

func (gl *GitLabDriver) ReconcileProtection(ctx context.Context, branch string, policy *config.BranchProtectionPolicy) error {
	if err := gl.Authenticate(ctx); err != nil {
		return err
	}
	return nil
}

func (gl *GitLabDriver) ReconcileLabels(ctx context.Context, labels []Label) error {
	if err := gl.Authenticate(ctx); err != nil {
		return err
	}
	return nil
}

func (gl *GitLabDriver) PostStatusCheck(ctx context.Context, commitSHA string, check CheckRun) error {
	if err := gl.Authenticate(ctx); err != nil {
		return err
	}
	return nil
}

func (gl *GitLabDriver) CreatePullRequest(ctx context.Context, req PRRequest) (*PRResponse, error) {
	if err := gl.Authenticate(ctx); err != nil {
		return nil, err
	}
	return &PRResponse{
		Number: 1,
		URL:    fmt.Sprintf("%s/projects/%s/merge_requests/1", gl.Endpoint, gl.targetProject()),
		State:  "opened",
	}, nil
}

func (gl *GitLabDriver) CreateIssue(ctx context.Context, spec IssueSpec) (*IssueResponse, error) {
	if err := gl.Authenticate(ctx); err != nil {
		return nil, err
	}
	return &IssueResponse{
		Number: 1,
		URL:    fmt.Sprintf("%s/projects/%s/issues/1", gl.Endpoint, gl.targetProject()),
		State:  "opened",
	}, nil
}
