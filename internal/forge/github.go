package forge

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/cordanaLLM/praetor/internal/config"
	"github.com/cordanaLLM/praetor/internal/util"
)

const (
	defaultHTTPTimeout  = 15 * time.Second
	maxHTTPResponseBody = 16 * 1024 * 1024 // 16 MB read bound (HISS-02)
	// issuesPerPage is the maximum page size the GitHub REST API accepts; every paginated
	// listing (issues, milestones, rulesets) requests it.
	issuesPerPage = 100
	// maxIssuePages bounds issue pagination (HISS-02): at most 2000 issues per listing.
	maxIssuePages = MaxListedIssuesLimit / issuesPerPage
	// maxMilestonePages bounds milestone pagination (HISS-02): at most 5000 milestones,
	// the same bound the milestone package applies to its own listing.
	maxMilestonePages = 50
	// maxRulesetPages bounds ruleset pagination (HISS-02): at most 1000 rulesets.
	maxRulesetPages = 10
	// MaxLabelsLimit bounds a label taxonomy (ParseLabelTaxonomy) and so a single label
	// reconciliation batch (HISS-02): one bound, so a taxonomy that validates can be written.
	MaxLabelsLimit = 1000
)

// errPageCeiling marks a listing that still returned full pages at its page bound: the
// listing is incomplete, so an entry missing from it proves nothing.
var errPageCeiling = errors.New("listing exceeds its page bound")

// ErrRepositoryUnresolved is returned when no repository coordinates are available. The
// driver never falls back to a hard-coded repository: a mutating call against the wrong
// repository is worse than a failed call.
var ErrRepositoryUnresolved = errors.New(
	"github repository is unresolved: set repository.owner and repository.name in .standards.yaml, " +
		"call SetRepository, or export GITHUB_REPOSITORY=<owner>/<repo>")

// DefaultRequiredStatusChecks returns the status check contexts a praetor-governed branch
// must require remotely. They mirror the declarative ruleset synthesized into
// .github/rulesets/main.json.
func DefaultRequiredStatusChecks() []string {
	return []string{
		"Standards & Invariant Verification Gate",
		"DCO 1.1 & REUSE Compliance Gate",
		"Go Vulnerability & AST Security Scan",
	}
}

// GitHubDriver implements Forge for GitHub using GitHub Apps / Personal Access Tokens.
type GitHubDriver struct {
	Token      string
	Endpoint   string
	Owner      string
	Repo       string
	HTTPClient *http.Client
	// RulesetName overrides the remote ruleset name. Empty means "<branch>-branch-protection".
	RulesetName string
	// RequiredStatusChecks are the check contexts pushed into the remote ruleset.
	RequiredStatusChecks []string
	// StrictStatusChecks requires branches to be up to date before merging.
	StrictStatusChecks bool
}

// NewGitHubDriver initializes a GitHub driver. util.GitHubAPIBase normalizes the endpoint:
// an empty endpoint or the github.com web origin selects https://api.github.com.
func NewGitHubDriver(token string, endpoint string) *GitHubDriver {
	return &GitHubDriver{
		Token:    token,
		Endpoint: util.GitHubAPIBase(endpoint),
		HTTPClient: &http.Client{
			Timeout: defaultHTTPTimeout,
		},
		RequiredStatusChecks: DefaultRequiredStatusChecks(),
		StrictStatusChecks:   true,
	}
}

// SetRepository configures the target repository owner and name.
func (g *GitHubDriver) SetRepository(owner, repo string) {
	g.Owner = owner
	g.Repo = repo
}

// resolveRepository returns the target owner and repository, falling back to the
// GITHUB_REPOSITORY environment variable used by GitHub Actions. It fails closed: an
// incomplete coordinate is ErrRepositoryUnresolved, and an owner or name that
// util.ValidateGitHubRepositoryIdentity rejects ("..", "/", URL delimiters) is an error
// before it can reach an API path.
func (g *GitHubDriver) resolveRepository() (string, string, error) {
	owner, repo := g.Owner, g.Repo
	if owner == "" || repo == "" {
		envOwner, envRepo, ok := strings.Cut(os.Getenv("GITHUB_REPOSITORY"), "/")
		if ok && envOwner != "" && envRepo != "" {
			owner, repo = envOwner, envRepo
		}
	}
	if owner == "" || repo == "" {
		return "", "", ErrRepositoryUnresolved
	}
	if err := util.ValidateGitHubRepositoryIdentity(owner, repo); err != nil {
		return "", "", fmt.Errorf("github repository: %w", err)
	}
	return owner, repo, nil
}

// TargetRepository reports the resolved "owner/repo" coordinate the driver will mutate.
func (g *GitHubDriver) TargetRepository() (string, error) {
	owner, repo, err := g.resolveRepository()
	if err != nil {
		return "", err
	}
	return owner + "/" + repo, nil
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
func (g *GitHubDriver) sendRequest(ctx context.Context, method, path string, payload any) (respBody []byte, statusCode int, err error) {
	if err := g.Authenticate(ctx); err != nil {
		return nil, 0, err
	}

	target := g.Endpoint + path
	var bodyReader io.Reader
	if payload != nil {
		data, err := json.Marshal(payload)
		if err != nil {
			return nil, 0, fmt.Errorf("failed encoding request payload: %w", err)
		}
		bodyReader = bytes.NewReader(data)
	}

	req, err := http.NewRequestWithContext(ctx, method, target, bodyReader)
	if err != nil {
		return nil, 0, fmt.Errorf("failed constructing http request: %w", err)
	}

	req.Header.Set("Authorization", "Bearer "+g.Token)
	req.Header.Set("User-Agent", "praetor-governance-engine")
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	client := g.HTTPClient
	if client == nil {
		// HISS-02: avoid the unbounded ambient http.DefaultClient.
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

	data, readErr := io.ReadAll(io.LimitReader(resp.Body, maxHTTPResponseBody))
	if readErr != nil {
		return nil, resp.StatusCode, fmt.Errorf("failed reading response body: %w", readErr)
	}

	return data, resp.StatusCode, nil
}

// rulesetName returns the remote ruleset name for a branch.
func (g *GitHubDriver) rulesetName(branch string) string {
	if g.RulesetName != "" {
		return g.RulesetName
	}
	return branch + "-branch-protection"
}

type ghRulesetRaw struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
}

// pageVisitor consumes one page of a GitHub REST listing. It returns the number of raw
// entries on the page and whether the walk may stop because the caller found its entry.
type pageVisitor func(body []byte) (rawCount int, done bool, err error)

// walkPages follows a GitHub REST listing page by page, requesting issuesPerPage entries
// per page, up to maxPages pages (HISS-02). A page shorter than issuesPerPage ends the
// listing. A listing that still returns full pages at the bound yields errPageCeiling, so
// no caller mistakes a truncated listing for a complete one. query holds extra
// "key=value&" parameters and may be empty; what names the listing in errors.
func (g *GitHubDriver) walkPages(ctx context.Context, base, query, what string, maxPages int, visit pageVisitor) error {
	for page := 1; page <= maxPages; page++ {
		path := fmt.Sprintf("%s?%sper_page=%d&page=%d", base, query, issuesPerPage, page)
		body, status, err := g.sendRequest(ctx, http.MethodGet, path, nil)
		if err != nil {
			return fmt.Errorf("failed listing %s: %w", what, err)
		}
		if status != http.StatusOK {
			return fmt.Errorf("unexpected status %d listing %s: %s", status, what, util.BodyPreview(body))
		}
		rawCount, done, err := visit(body)
		if err != nil {
			return err
		}
		if done || rawCount < issuesPerPage {
			return nil
		}
	}
	return fmt.Errorf("%w: %s beyond %d pages of %d entries", errPageCeiling, what, maxPages, issuesPerPage)
}

// findRulesetID returns the id of the repository ruleset named name, or 0 when absent. It
// reads every page of the listing: a ruleset past the first page must be updated in place,
// never duplicated by a second POST.
func (g *GitHubDriver) findRulesetID(ctx context.Context, listPath, name string) (int, error) {
	id := 0
	err := g.walkPages(ctx, listPath, "", "repository rulesets", maxRulesetPages, func(body []byte) (int, bool, error) {
		var rulesets []ghRulesetRaw
		if err := json.Unmarshal(body, &rulesets); err != nil {
			return 0, false, fmt.Errorf("failed parsing repository rulesets (raw: %q): %w", util.BodyPreview(body), err)
		}
		if len(rulesets) > issuesPerPage {
			return 0, false, fmt.Errorf("ruleset response exceeds %d entries", issuesPerPage)
		}
		for i := 0; i < len(rulesets); i++ {
			if rulesets[i].Name == name {
				id = rulesets[i].ID
				return len(rulesets), true, nil
			}
		}
		return len(rulesets), false, nil
	})
	if err != nil {
		return 0, err
	}
	return id, nil
}

// ReconcileProtection converges the remote branch ruleset onto the resolved policy. It is a
// true reconciler: an existing ruleset of the same name is updated in place, never
// duplicated, and a non-converging remote is an error, not a warning.
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

	listPath, err := g.repoPath("rulesets")
	if err != nil {
		return fmt.Errorf("reconcile branch protection for %s: %w", branch, err)
	}
	name := g.rulesetName(branch)
	payload, err := protectionRuleset(name, []string{"refs/heads/" + branch}, *policy, g.RequiredStatusChecks, g.StrictStatusChecks)
	if err != nil {
		return err
	}
	id, err := g.findRulesetID(ctx, listPath, name)
	if err != nil {
		return fmt.Errorf("reconcile branch protection for %s: %w", branch, err)
	}

	method, path := http.MethodPost, listPath
	if id > 0 {
		method, path = http.MethodPut, fmt.Sprintf("%s/%d", listPath, id)
	}
	body, status, err := g.sendRequest(ctx, method, path, payload)
	if err != nil {
		return fmt.Errorf("reconcile branch protection failed for %s: %w", branch, err)
	}
	if status != http.StatusOK && status != http.StatusCreated {
		return fmt.Errorf("unexpected status %d while reconciling ruleset %q: %s", status, name, util.BodyPreview(body))
	}
	return nil
}

// ReconcileLabels converges the repository label taxonomy. Every non-success status is an
// error: a label that was not written must never be reported as reconciled.
func (g *GitHubDriver) ReconcileLabels(ctx context.Context, labels []Label) error {
	if err := g.Authenticate(ctx); err != nil {
		return err
	}
	if len(labels) > MaxLabelsLimit {
		return fmt.Errorf("label count %d exceeds maximum allowed batch limit of %d", len(labels), MaxLabelsLimit)
	}

	for i := 0; i < len(labels) && i < MaxLabelsLimit; i++ {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("context cancelled during label reconciliation at index %d: %w", i, err)
		}
		if err := g.reconcileLabel(ctx, labels[i]); err != nil {
			return err
		}
	}
	return nil
}

func (g *GitHubDriver) reconcileLabel(ctx context.Context, l Label) error {
	if l.Name == "" {
		return errors.New("reconcile labels: label name cannot be empty")
	}
	payload := map[string]string{
		"name":        l.Name,
		"color":       l.Color,
		"description": l.Description,
	}
	path, err := g.repoPath("labels/" + url.PathEscape(l.Name))
	if err != nil {
		return fmt.Errorf("reconcile label %s: %w", l.Name, err)
	}
	body, status, err := g.sendRequest(ctx, http.MethodPatch, path, payload)
	if err != nil {
		return fmt.Errorf("failed updating label %s: %w", l.Name, err)
	}
	if status == http.StatusOK {
		return nil
	}
	if status != http.StatusNotFound {
		return fmt.Errorf("unexpected status %d updating label %s: %s", status, l.Name, util.BodyPreview(body))
	}
	return g.createLabel(ctx, l.Name, payload)
}

func (g *GitHubDriver) createLabel(ctx context.Context, name string, payload map[string]string) error {
	createPath, err := g.repoPath("labels")
	if err != nil {
		return fmt.Errorf("create label %s: %w", name, err)
	}
	body, status, err := g.sendRequest(ctx, http.MethodPost, createPath, payload)
	if err != nil {
		return fmt.Errorf("failed creating label %s: %w", name, err)
	}
	if status != http.StatusCreated {
		return fmt.Errorf("unexpected status %d creating label %s: %s", status, name, util.BodyPreview(body))
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

	path, err := g.repoPath("statuses/" + url.PathEscape(commitSHA))
	if err != nil {
		return fmt.Errorf("post status check for %s: %w", commitSHA, err)
	}
	body, status, err := g.sendRequest(ctx, http.MethodPost, path, payload)
	if err != nil {
		return fmt.Errorf("failed posting commit status check to %s: %w", commitSHA, err)
	}
	if status != http.StatusCreated && status != http.StatusOK {
		return fmt.Errorf("unexpected status %d posting commit status for %s: %s", status, commitSHA, util.BodyPreview(body))
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

	payload := map[string]any{
		"title": req.Title,
		"body":  req.Body,
		"head":  req.Head,
		"base":  req.Base,
		"draft": req.Draft,
	}
	path, err := g.repoPath("pulls")
	if err != nil {
		return nil, fmt.Errorf("create pull request: %w", err)
	}
	respBody, status, err := g.sendRequest(ctx, http.MethodPost, path, payload)
	if err != nil {
		return nil, fmt.Errorf("failed creating pull request: %w", err)
	}
	if status != http.StatusCreated {
		return nil, fmt.Errorf("unexpected status %d creating pull request: %s", status, util.BodyPreview(respBody))
	}

	var res PRResponse
	if err := json.Unmarshal(respBody, &res); err != nil {
		return nil, fmt.Errorf("failed parsing pull request response (raw: %q): %w", util.BodyPreview(respBody), err)
	}
	return &res, nil
}

// buildIssuePayload renders the full IssueSpec, including assignees and the milestone,
// which is resolved from its title to the numeric id the REST API requires.
func (g *GitHubDriver) buildIssuePayload(ctx context.Context, spec IssueSpec) (map[string]any, error) {
	payload := map[string]any{
		"title": spec.Title,
		"body":  spec.Body,
	}
	if len(spec.Labels) > 0 {
		payload["labels"] = spec.Labels
	}
	if len(spec.Assignees) > 0 {
		payload["assignees"] = spec.Assignees
	}
	if spec.Milestone != "" {
		number, err := g.resolveMilestone(ctx, spec.Milestone)
		if err != nil {
			return nil, err
		}
		payload["milestone"] = number
	}
	return payload, nil
}

type ghMilestoneRaw struct {
	Number int    `json:"number"`
	Title  string `json:"title"`
}

// resolveMilestone maps a milestone title to its repository-scoped number. It reads every
// page of the listing. An exact title wins; otherwise the first title equal under Unicode
// case folding is used, the same key the milestone store merges titles by. A listing cut
// off at maxMilestonePages is an error, never "does not exist".
func (g *GitHubDriver) resolveMilestone(ctx context.Context, title string) (int, error) {
	base, err := g.repoPath("milestones")
	if err != nil {
		return 0, fmt.Errorf("resolve milestone %q: %w", title, err)
	}
	exact, folded := 0, 0
	err = g.walkPages(ctx, base, "state=all&", "milestones", maxMilestonePages, func(body []byte) (int, bool, error) {
		var raw []ghMilestoneRaw
		if err := json.Unmarshal(body, &raw); err != nil {
			return 0, false, fmt.Errorf("failed parsing milestones (raw: %q): %w", util.BodyPreview(body), err)
		}
		for i := 0; i < len(raw); i++ {
			if raw[i].Title == title {
				exact = raw[i].Number
				return len(raw), true, nil
			}
			if folded == 0 && strings.EqualFold(raw[i].Title, title) {
				folded = raw[i].Number
			}
		}
		return len(raw), false, nil
	})
	switch {
	case err != nil:
		return 0, fmt.Errorf("resolve milestone %q: %w", title, err)
	case exact > 0:
		return exact, nil
	case folded > 0:
		return folded, nil
	}
	return 0, fmt.Errorf("milestone %q does not exist in the target repository", title)
}

func (g *GitHubDriver) CreateIssue(ctx context.Context, spec IssueSpec) (*IssueResponse, error) {
	if err := g.Authenticate(ctx); err != nil {
		return nil, err
	}
	if spec.Title == "" {
		return nil, errors.New("create issue: title is required")
	}

	payload, err := g.buildIssuePayload(ctx, spec)
	if err != nil {
		return nil, err
	}
	path, err := g.repoPath("issues")
	if err != nil {
		return nil, fmt.Errorf("create issue: %w", err)
	}
	respBody, status, err := g.sendRequest(ctx, http.MethodPost, path, payload)
	if err != nil {
		return nil, fmt.Errorf("failed creating issue: %w", err)
	}
	if status != http.StatusCreated {
		return nil, fmt.Errorf("unexpected status %d creating issue: %s", status, util.BodyPreview(respBody))
	}

	var res IssueResponse
	if err := json.Unmarshal(respBody, &res); err != nil {
		return nil, fmt.Errorf("failed parsing issue response (raw: %q): %w", util.BodyPreview(respBody), err)
	}
	return &res, nil
}

// ListIssues fetches every issue in the repository, following pagination up to the
// maxIssuePages scalar bound and discarding pull requests, which the issues endpoint also
// returns. A full final page is incomplete even if every item was a pull request.
func (g *GitHubDriver) ListIssues(ctx context.Context, state string) ([]IssueSpec, error) {
	if err := g.Authenticate(ctx); err != nil {
		return nil, err
	}
	if state == "" {
		state = "all"
	}
	base, err := g.repoPath("issues")
	if err != nil {
		return nil, fmt.Errorf("list issues: %w", err)
	}

	all := make([]IssueSpec, 0, issuesPerPage)
	query := "state=" + url.QueryEscape(state) + "&"
	err = g.walkPages(ctx, base, query, "issues", maxIssuePages, func(body []byte) (int, bool, error) {
		specs, rawCount, err := parseGitHubIssues(body)
		if err != nil {
			return 0, false, err
		}
		all = append(all, specs...)
		return rawCount, false, nil
	})
	if errors.Is(err, errPageCeiling) {
		return nil, &IssueListIncompleteError{Limit: MaxListedIssuesLimit}
	}
	if err != nil {
		return nil, err
	}
	return all, nil
}

type ghIssueRaw struct {
	Number int    `json:"number"`
	Title  string `json:"title"`
	Body   string `json:"body"`
	State  string `json:"state"`
	Labels []struct {
		Name string `json:"name"`
	} `json:"labels"`
	// PullRequest is non-nil when the item is a pull request rather than an issue.
	PullRequest *struct {
		URL string `json:"url"`
	} `json:"pull_request"`
}

// parseGitHubIssues decodes one page of the issues endpoint. It returns the converted
// issue specs and the number of raw items on the page (pull requests included), which the
// caller needs to decide whether another page must be fetched.
func parseGitHubIssues(body []byte) ([]IssueSpec, int, error) {
	var raw []ghIssueRaw
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, 0, fmt.Errorf("failed parsing issues list (len %d, raw: %q): %w", len(body), util.BodyPreview(body), err)
	}
	if raw == nil {
		return nil, 0, errors.New("GitHub issue listing must be an array, not null")
	}
	if len(raw) > issuesPerPage {
		return nil, 0, &IssueListIncompleteError{Limit: issuesPerPage}
	}
	specs := make([]IssueSpec, 0, len(raw))
	for _, r := range raw {
		if r.Number <= 0 || strings.TrimSpace(r.Title) == "" {
			return nil, 0, errors.New("GitHub issue listing contains an invalid issue number or empty title")
		}
		if r.PullRequest != nil {
			continue
		}
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
	return specs, len(raw), nil
}

// UpdateIssue modifies state and labels of an existing issue. The GitHub REST API replaces
// the whole label set with the labels argument, so callers must pass the complete desired
// set; use AddLabels / RemoveLabel for additive transitions that must preserve labels.
func (g *GitHubDriver) UpdateIssue(ctx context.Context, number int, labels []string, state string) error {
	if err := g.Authenticate(ctx); err != nil {
		return err
	}
	if number <= 0 {
		return fmt.Errorf("update issue: issue number must be positive, got %d", number)
	}
	payload := make(map[string]any)
	if len(labels) > 0 {
		payload["labels"] = labels
	}
	if state != "" {
		payload["state"] = state
	}
	if len(payload) == 0 {
		return errors.New("update issue: nothing to update (no labels and no state)")
	}
	base, err := g.repoPath("issues")
	if err != nil {
		return fmt.Errorf("update issue #%d: %w", number, err)
	}
	body, status, err := g.sendRequest(ctx, http.MethodPatch, fmt.Sprintf("%s/%d", base, number), payload)
	if err != nil {
		return fmt.Errorf("failed updating issue #%d: %w", number, err)
	}
	if status != http.StatusOK {
		return fmt.Errorf("unexpected status %d updating issue #%d: %s", status, number, util.BodyPreview(body))
	}
	return nil
}

// AddLabels adds labels to an issue without touching the labels it already carries.
func (g *GitHubDriver) AddLabels(ctx context.Context, number int, labels []string) error {
	if err := g.Authenticate(ctx); err != nil {
		return err
	}
	if number <= 0 {
		return fmt.Errorf("add labels: issue number must be positive, got %d", number)
	}
	if len(labels) == 0 {
		return errors.New("add labels: label set cannot be empty")
	}
	base, err := g.repoPath("issues")
	if err != nil {
		return fmt.Errorf("add labels to issue #%d: %w", number, err)
	}
	path := fmt.Sprintf("%s/%d/labels", base, number)
	body, status, err := g.sendRequest(ctx, http.MethodPost, path, map[string]any{"labels": labels})
	if err != nil {
		return fmt.Errorf("failed adding labels to issue #%d: %w", number, err)
	}
	if status != http.StatusOK && status != http.StatusCreated {
		return fmt.Errorf("unexpected status %d adding labels to issue #%d: %s", status, number, util.BodyPreview(body))
	}
	return nil
}

// RemoveLabel removes a single label from an issue. A label that is not present is not an
// error: the desired end state is already reached.
func (g *GitHubDriver) RemoveLabel(ctx context.Context, number int, label string) error {
	if err := g.Authenticate(ctx); err != nil {
		return err
	}
	if number <= 0 {
		return fmt.Errorf("remove label: issue number must be positive, got %d", number)
	}
	if label == "" {
		return errors.New("remove label: label name cannot be empty")
	}
	base, err := g.repoPath("issues")
	if err != nil {
		return fmt.Errorf("remove label from issue #%d: %w", number, err)
	}
	path := fmt.Sprintf("%s/%d/labels/%s", base, number, url.PathEscape(label))
	body, status, err := g.sendRequest(ctx, http.MethodDelete, path, nil)
	if err != nil {
		return fmt.Errorf("failed removing label %s from issue #%d: %w", label, number, err)
	}
	if status != http.StatusOK && status != http.StatusNoContent && status != http.StatusNotFound {
		return fmt.Errorf("unexpected status %d removing label %s from issue #%d: %s",
			status, label, number, util.BodyPreview(body))
	}
	return nil
}
