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
)

const (
	defaultHTTPTimeout  = 15 * time.Second
	maxHTTPResponseBody = 16 * 1024 * 1024 // 16 MB limit
	// FixtureTokenPrefix marks a token that constructs a dry-run driver: every write is
	// validated and answered with canned data, and no request leaves the process. It is
	// the single place this convention lives; callers must consult DryRun, never the
	// token text.
	FixtureTokenPrefix = "test-"
	// maxRulesetsPerPage bounds the ruleset listing loop (HISS-02).
	maxRulesetsPerPage = 100
)

// ErrRepositoryUnset reports a repository-scoped request without a target repository.
var ErrRepositoryUnset = errors.New("github: target repository is not set; call SetRepository or export GITHUB_REPOSITORY")

// GitHubDriver implements Forge for GitHub using GitHub Apps / Personal Access Tokens.
type GitHubDriver struct {
	Token      string
	Endpoint   string
	Owner      string
	Repo       string
	HTTPClient *http.Client
	// DryRun makes every mutating call validate its input and return canned data
	// without touching the network. NewGitHubDriver sets it for FixtureTokenPrefix
	// tokens; callers may set it explicitly.
	DryRun bool
}

// NewGitHubDriver initializes a GitHub driver.
func NewGitHubDriver(token string, endpoint string) *GitHubDriver {
	if endpoint == "" {
		endpoint = "https://api.github.com"
	}
	return &GitHubDriver{
		Token:    token,
		Endpoint: strings.TrimRight(endpoint, "/"),
		DryRun:   strings.HasPrefix(token, FixtureTokenPrefix),
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

// resolveRepository returns the configured owner and repository, falling back to the
// GITHUB_REPOSITORY variable the Actions runner sets. It never invents a repository.
func (g *GitHubDriver) resolveRepository() (owner, repo string, err error) {
	owner, repo = g.Owner, g.Repo
	if owner != "" && repo != "" {
		return owner, repo, nil
	}
	if envRepo := os.Getenv("GITHUB_REPOSITORY"); envRepo != "" {
		parts := strings.SplitN(envRepo, "/", 2)
		if len(parts) == 2 && parts[0] != "" && parts[1] != "" {
			return parts[0], parts[1], nil
		}
	}
	return "", "", ErrRepositoryUnset
}

// repoPath constructs an API path for the targeted repository.
func (g *GitHubDriver) repoPath(subpath string) (string, error) {
	owner, repo, err := g.resolveRepository()
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("/repos/%s/%s/%s", owner, repo, strings.TrimPrefix(subpath, "/")), nil
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
		// HISS-02: never fall back to the timeout-less http.DefaultClient.
		client = &http.Client{Timeout: defaultHTTPTimeout}
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, 0, fmt.Errorf("github api request failed (%s %s): %w", method, path, err)
	}
	defer func() {
		if cerr := resp.Body.Close(); cerr != nil && err == nil {
			err = fmt.Errorf("failed closing response body: %w", cerr)
		}
	}()

	body, err = io.ReadAll(io.LimitReader(resp.Body, maxHTTPResponseBody))
	if err != nil {
		return nil, resp.StatusCode, fmt.Errorf("failed reading response body: %w", err)
	}

	return body, resp.StatusCode, nil
}

// rulesetName is the name of the ruleset praetor owns for a branch.
func rulesetName(branch string) string {
	return fmt.Sprintf("%s-branch-protection", branch)
}

// rulesetPayload renders the GitHub ruleset for the resolved policy. Linear history and
// signed commits are emitted only when the policy requires them, so the forge never
// enforces stricter rules than the manifest declares.
func rulesetPayload(branch string, policy *config.BranchProtectionPolicy) map[string]any {
	rules := []map[string]any{
		{"type": "deletion"},
		{"type": "non_fast_forward"},
	}
	if policy.EnforceLinearHistory {
		rules = append(rules, map[string]any{"type": "required_linear_history"})
	}
	if policy.RequireSignedCommits {
		rules = append(rules, map[string]any{"type": "required_signatures"})
	}
	rules = append(rules, map[string]any{
		"type": "pull_request",
		"parameters": map[string]any{
			"required_approving_review_count": policy.RequiredApprovingReviewers,
			"dismiss_stale_reviews_on_push":   policy.DismissStaleReviews,
			"require_code_owner_review":       true,
		},
	})
	return map[string]any{
		"name":        rulesetName(branch),
		"target":      "branch",
		"enforcement": "active",
		"conditions": map[string]any{
			"ref_name": map[string]any{
				"include": []string{fmt.Sprintf("refs/heads/%s", branch)},
				"exclude": []string{},
			},
		},
		"rules": rules,
	}
}

// findRulesetID looks up the id of an existing ruleset with the given name.
func (g *GitHubDriver) findRulesetID(ctx context.Context, name string) (id int64, found bool, err error) {
	path, err := g.repoPath(fmt.Sprintf("rulesets?per_page=%d", maxRulesetsPerPage))
	if err != nil {
		return 0, false, err
	}
	body, status, err := g.sendRequest(ctx, http.MethodGet, path, nil)
	if err != nil {
		return 0, false, err
	}
	if status != http.StatusOK {
		return 0, false, fmt.Errorf("unexpected status %d listing rulesets: %s", status, string(body))
	}
	var rulesets []struct {
		ID   int64  `json:"id"`
		Name string `json:"name"`
	}
	if err := json.Unmarshal(body, &rulesets); err != nil {
		return 0, false, fmt.Errorf("failed parsing rulesets list: %w", err)
	}
	for i := 0; i < len(rulesets) && i < maxRulesetsPerPage; i++ {
		if rulesets[i].Name == name {
			return rulesets[i].ID, true, nil
		}
	}
	return 0, false, nil
}

// ReconcileProtection creates the branch protection ruleset for branch, or updates the
// existing one of the same name so repeated runs stay idempotent.
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
	if g.DryRun {
		return nil
	}

	name := rulesetName(branch)
	id, found, err := g.findRulesetID(ctx, name)
	if err != nil {
		return fmt.Errorf("reconcile branch protection failed for %s: %w", branch, err)
	}
	method, subpath := http.MethodPost, "rulesets"
	if found {
		method, subpath = http.MethodPut, fmt.Sprintf("rulesets/%d", id)
	}
	path, err := g.repoPath(subpath)
	if err != nil {
		return fmt.Errorf("reconcile branch protection failed for %s: %w", branch, err)
	}

	body, status, err := g.sendRequest(ctx, method, path, rulesetPayload(branch, policy))
	if err != nil {
		return fmt.Errorf("reconcile branch protection failed for %s: %w", branch, err)
	}
	if status != http.StatusOK && status != http.StatusCreated {
		return fmt.Errorf("unexpected status %d while reconciling ruleset %s for %s: %s", status, name, branch, string(body))
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
		if g.DryRun {
			continue
		}
		if err := g.upsertLabel(ctx, l); err != nil {
			return err
		}
	}
	return nil
}

// upsertLabel updates a label and creates it when it does not exist yet.
func (g *GitHubDriver) upsertLabel(ctx context.Context, l Label) error {
	payload := map[string]string{
		"name":        l.Name,
		"color":       l.Color,
		"description": l.Description,
	}
	path, err := g.repoPath(fmt.Sprintf("labels/%s", l.Name))
	if err != nil {
		return err
	}
	_, status, err := g.sendRequest(ctx, http.MethodPatch, path, payload)
	if err != nil {
		return fmt.Errorf("failed updating label %s: %w", l.Name, err)
	}
	if status == http.StatusNotFound {
		createPath, err := g.repoPath("labels")
		if err != nil {
			return err
		}
		if _, _, err := g.sendRequest(ctx, http.MethodPost, createPath, payload); err != nil {
			return fmt.Errorf("failed creating label %s: %w", l.Name, err)
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
	if g.DryRun {
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

	path, err := g.repoPath(fmt.Sprintf("statuses/%s", commitSHA))
	if err != nil {
		return err
	}
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

	if g.DryRun {
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
	path, err := g.repoPath("pulls")
	if err != nil {
		return nil, err
	}
	respBody, status, err := g.sendRequest(ctx, http.MethodPost, path, payload)
	if err != nil {
		return nil, fmt.Errorf("failed creating pull request: %w", err)
	}
	if status != http.StatusCreated {
		return nil, fmt.Errorf("unexpected status %d creating pull request: %s", status, string(respBody))
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

	if g.DryRun {
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
	path, err := g.repoPath("issues")
	if err != nil {
		return nil, err
	}
	respBody, status, err := g.sendRequest(ctx, http.MethodPost, path, payload)
	if err != nil {
		return nil, fmt.Errorf("failed creating issue: %w", err)
	}
	if status != http.StatusCreated {
		return nil, fmt.Errorf("unexpected status %d creating issue: %s", status, string(respBody))
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
	if g.DryRun {
		return []IssueSpec{
			{ID: 1, Title: "Test Issue 1", State: "closed", Labels: []string{"governance"}},
			{ID: 2, Title: "Test Issue 2", State: "open", Labels: []string{"architecture", "status/blocked"}, DependsOn: []string{"#1"}},
		}, nil
	}

	path, err := g.repoPath(fmt.Sprintf("issues?state=%s&per_page=100", state))
	if err != nil {
		return nil, err
	}
	respBody, status, err := g.sendRequest(ctx, http.MethodGet, path, nil)
	if err != nil {
		return nil, fmt.Errorf("failed listing issues: %w", err)
	}
	if status != http.StatusOK {
		return nil, fmt.Errorf("unexpected status %d listing issues: %s", status, string(respBody))
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
	if g.DryRun {
		return nil
	}
	payload := make(map[string]any)
	if len(labels) > 0 {
		payload["labels"] = labels
	}
	if state != "" {
		payload["state"] = state
	}
	path, err := g.repoPath(fmt.Sprintf("issues/%d", number))
	if err != nil {
		return err
	}
	_, status, err := g.sendRequest(ctx, http.MethodPatch, path, payload)
	if err != nil {
		return fmt.Errorf("failed updating issue #%d: %w", number, err)
	}
	if status != http.StatusOK {
		return fmt.Errorf("unexpected status %d updating issue #%d", status, number)
	}
	return nil
}
