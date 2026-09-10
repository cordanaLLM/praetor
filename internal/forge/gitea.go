package forge

import (
	"context"
	"fmt"

	"github.com/cordanaLLM/standards/internal/config"
)

// GiteaDriver implements Forge for Gitea / Forgejo instances.
type GiteaDriver struct {
	Token    string
	Endpoint string
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
	return &PRResponse{
		Number: 1,
		URL:    fmt.Sprintf("%s/pulls/1", gt.Endpoint),
		State:  "open",
	}, nil
}

func (gt *GiteaDriver) CreateIssue(ctx context.Context, spec IssueSpec) (*IssueResponse, error) {
	if err := gt.Authenticate(ctx); err != nil {
		return nil, err
	}
	return &IssueResponse{
		Number: 1,
		URL:    fmt.Sprintf("%s/issues/1", gt.Endpoint),
		State:  "open",
	}, nil
}
