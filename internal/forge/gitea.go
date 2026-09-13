package forge

import (
	"context"
	"errors"
	"fmt"

	"github.com/cordanaLLM/praetor/internal/config"
)

// GiteaDriver implements the credential handling for Gitea / Forgejo instances. The
// enforcement methods are deliberately unimplemented: returning success without performing
// the operation would report governance that does not exist, so every one of them fails
// with ErrNotImplemented until a real Gitea client exists.
type GiteaDriver struct {
	Token    string
	Endpoint string
	Owner    string
	Repo     string
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
	if err := gt.Authenticate(ctx); err != nil {
		return nil, err
	}
	return nil, gt.unsupported("CreatePullRequest")
}

func (gt *GiteaDriver) CreateIssue(ctx context.Context, spec IssueSpec) (*IssueResponse, error) {
	if err := gt.Authenticate(ctx); err != nil {
		return nil, err
	}
	return nil, gt.unsupported("CreateIssue")
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
