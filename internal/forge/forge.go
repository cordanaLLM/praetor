package forge

import (
	"context"
	"fmt"

	"github.com/cordanaLLM/praetor/internal/config"
)

// Label represents a canonical repository label.
type Label struct {
	Name        string `json:"name" yaml:"name"`
	Color       string `json:"color" yaml:"color"`
	Description string `json:"description" yaml:"description"`
}

// CheckRun represents a status check or SARIF diagnostic report posted to the forge.
type CheckRun struct {
	Name       string `json:"name"`
	HeadSHA    string `json:"head_sha"`
	Status     string `json:"status"`     // "in_progress", "completed"
	Conclusion string `json:"conclusion"` // "success", "failure", "neutral"
	Summary    string `json:"summary"`
	DetailsURL string `json:"details_url,omitempty"`
}

// PRRequest represents a pull request creation request for campaigns or automated fixes.
type PRRequest struct {
	Title string `json:"title"`
	Body  string `json:"body"`
	Head  string `json:"head"`
	Base  string `json:"base"`
}

// PRResponse represents the result of creating a pull request.
type PRResponse struct {
	Number int    `json:"number"`
	URL    string `json:"url"`
	State  string `json:"state"`
}

// Forge provides a decoupled, vendor-neutral abstraction across Git hosting providers.
type Forge interface {
	Name() string
	Authenticate(ctx context.Context) error
	ReconcileProtection(ctx context.Context, branch string, policy *config.BranchProtectionPolicy) error
	ReconcileLabels(ctx context.Context, labels []Label) error
	PostStatusCheck(ctx context.Context, commitSHA string, check CheckRun) error
	CreatePullRequest(ctx context.Context, req PRRequest) (*PRResponse, error)
	CreateIssue(ctx context.Context, spec IssueSpec) (*IssueResponse, error)
}

// NewForge returns the appropriate forge implementation based on provider identifier.
func NewForge(provider string, token string, endpoint string) (Forge, error) {
	switch provider {
	case "github":
		return NewGitHubDriver(token, endpoint), nil
	case "gitlab":
		return NewGitLabDriver(token, endpoint), nil
	case "gitea", "forgejo":
		return NewGiteaDriver(token, endpoint), nil
	default:
		return nil, fmt.Errorf("unsupported forge provider: %s", provider)
	}
}
