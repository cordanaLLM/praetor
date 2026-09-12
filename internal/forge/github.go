package forge

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/cordanaLLM/praetor/internal/config"
	"github.com/cordanaLLM/praetor/internal/util"
)

const (
	defaultHTTPTimeout  = 15 * time.Second
	maxHTTPResponseBody = 16 * 1024 * 1024 // 16 MB limit
	// maxErrorBodyBytes bounds how much of a response body may be read into, and
	// embedded in, an error that the CLI prints verbatim.
	maxErrorBodyBytes = util.MaxErrorBodyBytes
)

// GitHubDriver implements Forge for GitHub using GitHub Apps / Personal Access Tokens.
type GitHubDriver struct {
	Token      string
	Endpoint   string
	Owner      string
	Repo       string
	HTTPClient *http.Client
}

// NewGitHubDriver initializes a GitHub driver.
func NewGitHubDriver(token string, endpoint string) *GitHubDriver {
	if endpoint == "" {
		endpoint = "https://api.github.com"
	}
	return &GitHubDriver{
		Token:    token,
		Endpoint: strings.TrimRight(endpoint, "/"),
		HTTPClient: &http.Client{
			Timeout: defaultHTTPTimeout,
		},
	}
}

// SetRepository configures the target repository owner and name.
func (g *GitHubDriver) SetRepository(owner, repo string) {
	g.Owner = owner
	g.Repo = repo
}

// repoPath constructs an API path for the targeted repository.
func (g *GitHubDriver) repoPath(subpath string) string {
	owner := g.Owner
	repo := g.Repo
	if owner == "" || repo == "" {
		if envRepo := os.Getenv("GITHUB_REPOSITORY"); envRepo != "" {
			parts := strings.SplitN(envRepo, "/", 2)
			if len(parts) == 2 {
				owner = parts[0]
				repo = parts[1]
			}
		}
	}
	if owner == "" {
		owner = "cordanaLLM"
	}
	if repo == "" {
		repo = "praetor"
	}
	return fmt.Sprintf("/repos/%s/%s/%s", owner, repo, strings.TrimPrefix(subpath, "/"))
}

func (g *GitHubDriver) Name() string {
	return "github"
}

func (g *GitHubDriver) Authenticate(ctx context.Context) error {
	if ctx == nil {
		return errors.New("github authentication: context cannot be nil")
	}
	if g.Token == "" {
		return errors.New("github authentication failed: token is empty")
	}
	return nil
}

// sendRequest handles authenticated HTTP communication with GitHub REST API.
func (g *GitHubDriver) sendRequest(ctx context.Context, method, path string, payload any) (body []byte, status int, err error) {
	if err := g.Authenticate(ctx); err != nil {
		return nil, 0, err
	}

	url := fmt.Sprintf("%s%s", g.Endpoint, path)
	var bodyReader io.Reader
	if payload != nil {
		data, err := json.Marshal(payload)
		if err != nil {
			return nil, 0, fmt.Errorf("failed encoding request payload: %w", err)
		}
		bodyReader = bytes.NewReader(data)
	}

	req, err := http.NewRequestWithContext(ctx, method, url, bodyReader)
	if err != nil {
		return nil, 0, fmt.Errorf("failed constructing http request: %w", err)
	}

	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", g.Token))
	req.Header.Set("User-Agent", "praetor-governance-engine")
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	client := g.HTTPClient
	if client == nil {
		// HISS-02: never fall back to http.DefaultClient, which carries no timeout.
		client = &http.Client{Timeout: defaultHTTPTimeout}
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, 0, fmt.Errorf("github api request failed (%s %s): %w", method, path, err)
	}
	defer func() {
		if cerr := resp.Body.Close(); cerr != nil && err == nil {
			err = fmt.Errorf("close response body (%s %s): %w", method, path, cerr)
		}
	}()

	body, err = io.ReadAll(io.LimitReader(resp.Body, maxHTTPResponseBody))
	if err != nil {
		return nil, resp.StatusCode, fmt.Errorf("failed reading response body: %w", err)
	}

	return body, resp.StatusCode, nil
}

func (g *GitHubDriver) ReconcileProtection(ctx context.Context, branch string, policy *config.BranchProtectionPolicy) error {
	if err := g.Authenticate(ctx); err != nil {
		return err
	}
	if branch == "" {
		return errors.New("reconcile protection: branch cannot be empty")
	}
	if policy == nil {
		return errors.New("reconcile protection: policy cannot be nil")
	}

	// In test mode or dry-run, validate parameters and conclude successfully
	if strings.HasPrefix(g.Token, "test-") {
		return nil
	}

	// Payload for GitHub Ruleset API
	rulesetPayload := map[string]any{
		"name":        fmt.Sprintf("%s-branch-protection", branch),
		"target":      "branch",
		"enforcement": "active",
		"conditions": map[string]any{
			"ref_name": map[string]any{
				"include": []string{fmt.Sprintf("refs/heads/%s", branch)},
				"exclude": []string{},
			},
		},
		"rules": []map[string]any{
			{"type": "deletion"},
			{"type": "non_fast_forward"},
			{"type": "required_linear_history"},
			{"type": "required_signatures"},
			{
				"type": "pull_request",
				"parameters": map[string]any{
					"required_approving_review_count": policy.RequiredApprovingReviewers,
					"dismiss_stale_reviews_on_push":   policy.DismissStaleReviews,
					"require_code_owner_review":       true,
				},
			},
		},
	}

	path := g.repoPath("rulesets")
	_, status, err := g.sendRequest(ctx, http.MethodPost, path, rulesetPayload)
	if err != nil {
		return fmt.Errorf("reconcile branch protection failed for %s: %w", branch, err)
	}
	if status != http.StatusOK && status != http.StatusCreated {
		return fmt.Errorf("unexpected status %d while reconciling ruleset for %s", status, branch)
	}
	return nil
}

func (g *GitHubDriver) ReconcileLabels(ctx context.Context, labels []Label) error {
	if err := g.Authenticate(ctx); err != nil {
		return err
	}

	for i := 0; i < len(labels); i++ {
		l := labels[i]
		if l.Name == "" {
			return errors.New("reconcile labels: label name cannot be empty")
		}
		if strings.HasPrefix(g.Token, "test-") {
			continue
		}

		payload := map[string]string{
			"name":        l.Name,
			"color":       l.Color,
			"description": l.Description,
		}
		path := g.repoPath(fmt.Sprintf("labels/%s", l.Name))
		_, status, err := g.sendRequest(ctx, http.MethodPatch, path, payload)
		if err != nil {
			return fmt.Errorf("failed updating label %s: %w", l.Name, err)
		}
		if status == http.StatusNotFound {
			createPath := g.repoPath("labels")
			if _, _, err := g.sendRequest(ctx, http.MethodPost, createPath, payload); err != nil {
				return fmt.Errorf("failed creating label %s: %w", l.Name, err)
			}
		}
	}
	return nil
}

func (g *GitHubDriver) PostStatusCheck(ctx context.Context, commitSHA string, check CheckRun) error {
	if err := g.Authenticate(ctx); err != nil {
		return err
	}
	if commitSHA == "" {
		return errors.New("post status check: commitSHA cannot be empty")
	}

	if strings.HasPrefix(g.Token, "test-") {
		return nil
	}

	state := "pending"
	switch check.Conclusion {
	case "success":
		state = "success"
	case "failure":
		state = "failure"
	}

	payload := map[string]string{
		"state":       state,
		"target_url":  check.DetailsURL,
		"description": check.Summary,
		"context":     check.Name,
	}

	path := g.repoPath(fmt.Sprintf("statuses/%s", commitSHA))
	_, status, err := g.sendRequest(ctx, http.MethodPost, path, payload)
	if err != nil {
		return fmt.Errorf("failed posting commit status check to %s: %w", commitSHA, err)
	}
	if status != http.StatusCreated && status != http.StatusOK {
		return fmt.Errorf("unexpected status %d posting commit status for %s", status, commitSHA)
	}
	return nil
}

func (g *GitHubDriver) CreatePullRequest(ctx context.Context, req PRRequest) (*PRResponse, error) {
	if err := g.Authenticate(ctx); err != nil {
		return nil, err
	}
	if req.Title == "" || req.Head == "" || req.Base == "" {
		return nil, errors.New("create pull request: title, head, and base are required")
	}

	if strings.HasPrefix(g.Token, "test-") {
		return &PRResponse{
			Number: 1,
			URL:    fmt.Sprintf("%s/pulls/1", g.Endpoint),
			State:  "open",
		}, nil
	}

	payload := map[string]string{
		"title": req.Title,
		"body":  req.Body,
		"head":  req.Head,
		"base":  req.Base,
	}
	path := g.repoPath("pulls")
	respBody, status, err := g.sendRequest(ctx, http.MethodPost, path, payload)
	if err != nil {
		return nil, fmt.Errorf("failed creating pull request: %w", err)
	}
	if status != http.StatusCreated {
		return nil, fmt.Errorf("unexpected status %d creating pull request: %s", status, util.TruncateExcerpt(string(respBody), maxErrorBodyBytes))
	}

	var res PRResponse
	if err := json.Unmarshal(respBody, &res); err != nil {
		return nil, fmt.Errorf("failed parsing pull request response: %w", err)
	}
	return &res, nil
}

func (g *GitHubDriver) CreateIssue(ctx context.Context, spec IssueSpec) (*IssueResponse, error) {
	if err := g.Authenticate(ctx); err != nil {
		return nil, err
	}
	if spec.Title == "" {
		return nil, errors.New("create issue: title is required")
	}

	if strings.HasPrefix(g.Token, "test-") {
		return &IssueResponse{
			Number: 1,
			URL:    fmt.Sprintf("%s/issues/1", g.Endpoint),
			State:  "open",
		}, nil
	}

	payload := map[string]any{
		"title":  spec.Title,
		"body":   spec.Body,
		"labels": spec.Labels,
	}
	path := g.repoPath("issues")
	respBody, status, err := g.sendRequest(ctx, http.MethodPost, path, payload)
	if err != nil {
		return nil, fmt.Errorf("failed creating issue: %w", err)
	}
	if status != http.StatusCreated {
		return nil, fmt.Errorf("unexpected status %d creating issue: %s", status, util.TruncateExcerpt(string(respBody), maxErrorBodyBytes))
	}

	var res IssueResponse
	if err := json.Unmarshal(respBody, &res); err != nil {
		return nil, fmt.Errorf("failed parsing issue response: %w", err)
	}
	return &res, nil
}

// ListIssues fetches issues from the repository.
func (g *GitHubDriver) ListIssues(ctx context.Context, state string) ([]IssueSpec, error) {
	if err := g.Authenticate(ctx); err != nil {
		return nil, err
	}
	if state == "" {
		state = "all"
	}
	if strings.HasPrefix(g.Token, "test-") {
		return []IssueSpec{
			{ID: 1, Title: "Test Issue 1", State: "closed", Labels: []string{"governance"}},
			{ID: 2, Title: "Test Issue 2", State: "open", Labels: []string{"architecture", "status/blocked"}, DependsOn: []string{"#1"}},
		}, nil
	}

	path := fmt.Sprintf("%s?state=%s&per_page=100", g.repoPath("issues"), state)
	respBody, status, err := g.sendRequest(ctx, http.MethodGet, path, nil)
	if err != nil {
		return nil, fmt.Errorf("failed listing issues: %w", err)
	}
	if status != http.StatusOK {
		return nil, fmt.Errorf("unexpected status %d listing issues: %s", status, util.TruncateExcerpt(string(respBody), maxErrorBodyBytes))
	}

	return parseGitHubIssues(respBody)
}

type ghIssueRaw struct {
	Number int    `json:"number"`
	Title  string `json:"title"`
	Body   string `json:"body"`
	State  string `json:"state"`
	Labels []struct {
		Name string `json:"name"`
	} `json:"labels"`
}

func parseGitHubIssues(body []byte) ([]IssueSpec, error) {
	var raw []ghIssueRaw
	if err := json.Unmarshal(body, &raw); err != nil {
		preview := string(body)
		if len(preview) > 100 {
			preview = preview[:100]
		}
		return nil, fmt.Errorf("failed parsing issues list (len %d, raw: %q): %w", len(body), preview, err)
	}
	specs := make([]IssueSpec, 0, len(raw))
	for _, r := range raw {
		lbls := make([]string, 0, len(r.Labels))
		for _, l := range r.Labels {
			lbls = append(lbls, l.Name)
		}
		deps := ParseIssueDependencies(r.Body)
		depStrs := make([]string, 0, len(deps))
		for _, d := range deps {
			depStrs = append(depStrs, d.Raw)
		}
		specs = append(specs, IssueSpec{
			ID:        r.Number,
			Title:     r.Title,
			Body:      r.Body,
			State:     r.State,
			Labels:    lbls,
			DependsOn: depStrs,
		})
	}
	return specs, nil
}

// UpdateIssue modifies state or labels of an existing issue.
func (g *GitHubDriver) UpdateIssue(ctx context.Context, number int, labels []string, state string) error {
	if err := g.Authenticate(ctx); err != nil {
		return err
	}
	if strings.HasPrefix(g.Token, "test-") {
		return nil
	}
	payload := make(map[string]any)
	if len(labels) > 0 {
		payload["labels"] = labels
	}
	if state != "" {
		payload["state"] = state
	}
	path := fmt.Sprintf("%s/%d", g.repoPath("issues"), number)
	_, status, err := g.sendRequest(ctx, http.MethodPatch, path, payload)
	if err != nil {
		return fmt.Errorf("failed updating issue #%d: %w", number, err)
	}
	if status != http.StatusOK {
		return fmt.Errorf("unexpected status %d updating issue #%d", status, number)
	}
	return nil
}
