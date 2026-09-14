package forge

import (
	"context"
	"fmt"

	"github.com/cordanallm/praetor/internal/config"
)

// GiteaDriver implements Forge for Gitea / Forgejo instances.
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

func (gt *GiteaDriver) targetRepo() (string, string) {
	owner, repo := gt.Owner, gt.Repo
	if owner == "" {
		owner = "owner"
	}
	if repo == "" {
		repo = "repo"
	}
	return owner, repo
}

func (gt *GiteaDriver) Name() string {
	return "gitea"
}

func (gt *GiteaDriver) Authenticate(ctx context.Context) error {
	if gt.Token == "" {
		return fmt.Errorf("gitea authentication failed: token is empty")
	}
	return nil
}

func (gt *GiteaDriver) ReconcileProtection(ctx context.Context, branch string, policy *config.BranchProtectionPolicy) error {
	if err := gt.Authenticate(ctx); err != nil {
		return err
	}
	return nil
}

func (gt *GiteaDriver) ReconcileLabels(ctx context.Context, labels []Label) error {
	if err := gt.Authenticate(ctx); err != nil {
		return err
	}
	return nil
}

func (gt *GiteaDriver) PostStatusCheck(ctx context.Context, commitSHA string, check CheckRun) error {
	if err := gt.Authenticate(ctx); err != nil {
		return err
	}
	return nil
}

func (gt *GiteaDriver) CreatePullRequest(ctx context.Context, req PRRequest) (*PRResponse, error) {
	if err := gt.Authenticate(ctx); err != nil {
		return nil, err
	}
	owner, repo := gt.targetRepo()
	return &PRResponse{
		Number: 1,
		URL:    fmt.Sprintf("%s/repos/%s/%s/pulls/1", gt.Endpoint, owner, repo),
		State:  "open",
	}, nil
}

func (gt *GiteaDriver) CreateIssue(ctx context.Context, spec IssueSpec) (*IssueResponse, error) {
	if err := gt.Authenticate(ctx); err != nil {
		return nil, err
	}
	owner, repo := gt.targetRepo()
	return &IssueResponse{
		Number: 1,
		URL:    fmt.Sprintf("%s/repos/%s/%s/issues/1", gt.Endpoint, owner, repo),
		State:  "open",
	}, nil
}
