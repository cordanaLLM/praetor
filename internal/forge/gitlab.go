package forge

import (
	"context"
	"errors"
	"fmt"

	"github.com/cordanaLLM/praetor/internal/config"
)

// GitLabDriver implements the credential handling for GitLab Project Access Tokens. The
// enforcement methods are deliberately unimplemented: returning success without performing
// the operation would report governance that does not exist, so every one of them fails
// with ErrNotImplemented until a real GitLab client exists.
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

func (gl *GitLabDriver) Name() string {
	return "gitlab"
}

func (gl *GitLabDriver) Authenticate(ctx context.Context) error {
	if ctx == nil {
		return errors.New("gitlab authentication: context cannot be nil")
	}
	if gl.Token == "" {
		return errors.New("gitlab authentication failed: token is empty")
	}
	return nil
}

func (gl *GitLabDriver) unsupported(method string) error {
	return fmt.Errorf("gitlab driver: %s: %w", method, ErrNotImplemented)
}

func (gl *GitLabDriver) ReconcileProtection(ctx context.Context, branch string, policy *config.BranchProtectionPolicy) error {
	if err := gl.Authenticate(ctx); err != nil {
		return err
	}
	return gl.unsupported("ReconcileProtection")
}

func (gl *GitLabDriver) ReconcileLabels(ctx context.Context, labels []Label) error {
	if err := gl.Authenticate(ctx); err != nil {
		return err
	}
	return gl.unsupported("ReconcileLabels")
}

func (gl *GitLabDriver) PostStatusCheck(ctx context.Context, commitSHA string, check CheckRun) error {
	if err := gl.Authenticate(ctx); err != nil {
		return err
	}
	return gl.unsupported("PostStatusCheck")
}

func (gl *GitLabDriver) CreatePullRequest(ctx context.Context, req PRRequest) (*PRResponse, error) {
	if err := gl.Authenticate(ctx); err != nil {
		return nil, err
	}
	return nil, gl.unsupported("CreatePullRequest")
}

func (gl *GitLabDriver) CreateIssue(ctx context.Context, spec IssueSpec) (*IssueResponse, error) {
	if err := gl.Authenticate(ctx); err != nil {
		return nil, err
	}
	return nil, gl.unsupported("CreateIssue")
}

func (gl *GitLabDriver) ListIssues(ctx context.Context, state string) ([]IssueSpec, error) {
	if err := gl.Authenticate(ctx); err != nil {
		return nil, err
	}
	return nil, gl.unsupported("ListIssues")
}

func (gl *GitLabDriver) UpdateIssue(ctx context.Context, number int, labels []string, state string) error {
	if err := gl.Authenticate(ctx); err != nil {
		return err
	}
	return gl.unsupported("UpdateIssue")
}
