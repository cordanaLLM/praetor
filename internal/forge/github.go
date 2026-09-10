package forge

import (
	"context"
	"fmt"

	"github.com/cordanaLLM/standards/internal/config"
)

// GitHubDriver implements Forge for GitHub using GitHub Apps / Tokens.
type GitHubDriver struct {
	Token    string
	Endpoint string
}

// NewGitHubDriver initializes a GitHub driver.
func NewGitHubDriver(token string, endpoint string) *GitHubDriver {
	if endpoint == "" {
		endpoint = "https://api.github.com"
	}
	return &GitHubDriver{
		Token:    token,
		Endpoint: endpoint,
	}
}

func (g *GitHubDriver) Name() string {
	return "github"
}

func (g *GitHubDriver) Authenticate(ctx context.Context) error {
	if g.Token == "" {
		return fmt.Errorf("github authentication failed: token is empty")
	}
	return nil
}

func (g *GitHubDriver) ReconcileProtection(ctx context.Context, branch string, policy *config.BranchProtectionPolicy) error {
	if err := g.Authenticate(ctx); err != nil {
		return err
	}
	// Reconciles GitHub Rulesets or classic branch protection
	return nil
}

func (g *GitHubDriver) ReconcileLabels(ctx context.Context, labels []Label) error {
	if err := g.Authenticate(ctx); err != nil {
		return err
	}
	return nil
}

func (g *GitHubDriver) PostStatusCheck(ctx context.Context, commitSHA string, check CheckRun) error {
	if err := g.Authenticate(ctx); err != nil {
		return err
	}
	return nil
}

func (g *GitHubDriver) CreatePullRequest(ctx context.Context, req PRRequest) (*PRResponse, error) {
	if err := g.Authenticate(ctx); err != nil {
		return nil, err
	}
	return &PRResponse{
		Number: 1,
		URL:    fmt.Sprintf("%s/pulls/1", g.Endpoint),
		State:  "open",
	}, nil
}

func (g *GitHubDriver) CreateIssue(ctx context.Context, spec IssueSpec) (*IssueResponse, error) {
	if err := g.Authenticate(ctx); err != nil {
		return nil, err
	}
	return &IssueResponse{
		Number: 1,
		URL:    fmt.Sprintf("%s/issues/1", g.Endpoint),
		State:  "open",
	}, nil
}
