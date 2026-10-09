package forge

import (
	"context"
	"errors"
	"fmt"

	"github.com/cordanaLLM/praetor/internal/config"
)

// ErrNotImplemented marks a Forge method that has no implementation for the selected
// provider. Drivers return it instead of a fabricated success so that a caller can never
// mistake a stub for an enforced governance action. It wraps errors.ErrUnsupported.
var ErrNotImplemented = fmt.Errorf("%w: forge driver method is not implemented", errors.ErrUnsupported)

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
	Draft bool   `json:"draft"`
}

// PRResponse represents the result of creating a pull request.
type PRResponse struct {
	Number int    `json:"number"`
	URL    string `json:"url"`
	State  string `json:"state"`
}

// ActionsReader reads a repository's Actions state and changes nothing: its live workflow
// permissions (#611) and the recent runs of one of its workflows (#612). The audit and plan
// depend on this interface alone, so a stand-in replaces the forge in their tests.
type ActionsReader interface {
	// WorkflowPermissions returns the repository's live Actions workflow permissions and,
	// when the token can read them, its organisation's. ErrActionsNotReadable marks a forge
	// that refused to show the repository's value.
	WorkflowPermissions(ctx context.Context) (LiveWorkflowPermissions, error)
	// WorkflowRunHistory returns the latest completed runs of workflow, a file name under
	// .github/workflows, on branch, and whether the workflow has run anywhere at all.
	WorkflowRunHistory(ctx context.Context, workflow, branch string) (WorkflowRunHistory, error)
}

// Forge provides a decoupled, vendor-neutral abstraction across Git hosting providers.
type Forge interface {
	ActionsReader
	Name() string
	Authenticate(ctx context.Context) error
	ReconcileProtection(ctx context.Context, branch string, policy *config.BranchProtectionPolicy) error
	ReconcileLabels(ctx context.Context, labels []Label) error
	PostStatusCheck(ctx context.Context, commitSHA string, check CheckRun) error
	CreatePullRequest(ctx context.Context, req PRRequest) (*PRResponse, error)
	CreateIssue(ctx context.Context, spec IssueSpec) (*IssueResponse, error)
	ListIssues(ctx context.Context, state string) ([]IssueSpec, error)
	UpdateIssue(ctx context.Context, number int, labels []string, state string) error
	// EditIssueBody replaces the body of an existing issue and changes nothing else. The
	// pre-migration epic writes its child task-list lines through it (#837).
	EditIssueBody(ctx context.Context, number int, body string) error
	// ListMergedPullRequests fetches landed pull requests, newest merge first, filtered by
	// milestone before the limit, along with the creation time of their closing issues.
	ListMergedPullRequests(ctx context.Context, query MergedPullRequestQuery) (MergedPullRequestList, error)
}

// NewForge returns the appropriate forge implementation based on provider identifier.
// Only the GitHub driver performs real enforcement; the GitLab and Gitea drivers
// authenticate but fail every enforcement method with ErrNotImplemented.
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
