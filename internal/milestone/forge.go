package milestone

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/cordanaLLM/praetor/internal/util"
)

const (
	// remoteHTTPTimeout bounds a single forge request (HISS-02).
	remoteHTTPTimeout = 15 * time.Second
	// milestonePageSize is the page size requested from the forge.
	milestonePageSize = 100
	// maxMilestonePages bounds the pagination loop (HISS-02).
	maxMilestonePages = 50
	// maxAPIResponseBytes bounds how much of a forge response is parsed, so a hostile or
	// misconfigured endpoint cannot stream an unbounded body into memory.
	maxAPIResponseBytes = 8 << 20
	// maxErrorBodyBytes bounds how much of an error response is embedded in an error
	// message that the CLI prints verbatim.
	maxErrorBodyBytes = util.MaxErrorBodyBytes
)

// RemoteMilestone represents the GitHub REST API representation of a milestone.
type RemoteMilestone struct {
	Number       int        `json:"number"`
	Title        string     `json:"title"`
	Description  string     `json:"description"`
	State        string     `json:"state"`
	DueOn        *time.Time `json:"due_on,omitempty"`
	OpenIssues   int        `json:"open_issues"`
	ClosedIssues int        `json:"closed_issues"`
	HTMLURL      string     `json:"html_url"`
}

// SyncWithGitHub synchronizes local milestones with GitHub remote milestones.
func SyncWithGitHub(ctx context.Context, rootPath, owner, repo, token, endpoint string) ([]Milestone, error) {
	tok := util.ResolveAuthTokenContext(ctx, token)
	if tok == "" {
		return nil, fmt.Errorf("GitHub milestone sync requires GITHUB_TOKEN or authenticated gh CLI session")
	}

	remotes, err := fetchRemoteMilestones(ctx, owner, repo, tok, endpoint)
	if err != nil {
		return nil, err
	}

	store, err := loadStore(ctx, rootPath)
	if err != nil {
		return nil, err
	}

	if err := mergeRemoteMilestones(store, remotes); err != nil {
		return nil, err
	}

	if err := commitStoreAndBacklog(ctx, rootPath, store); err != nil {
		return nil, err
	}
	return store.Milestones, nil
}

// fetchRemoteMilestones lists every milestone of the repository, following pagination up
// to maxMilestonePages so that a repository with more than one page of milestones is
// never silently truncated.
func fetchRemoteMilestones(ctx context.Context, owner, repo, tok, endpoint string) ([]RemoteMilestone, error) {
	if err := util.ValidateGitHubRepositoryIdentity(owner, repo); err != nil {
		return nil, err
	}

	apiBase := util.GitHubAPIBase(endpoint)
	client := &http.Client{Timeout: remoteHTTPTimeout}
	all := make([]RemoteMilestone, 0, milestonePageSize)

	for page := 1; page <= maxMilestonePages; page++ {
		url := fmt.Sprintf("%s/repos/%s/%s/milestones?state=all&per_page=%d&page=%d",
			apiBase, owner, repo, milestonePageSize, page)
		batch, err := fetchMilestonePage(ctx, client, url, tok)
		if err != nil {
			return nil, err
		}
		all = append(all, batch...)
		if len(batch) < milestonePageSize {
			return all, nil
		}
	}
	return nil, fmt.Errorf("remote milestone listing for %s/%s exceeds the %d page bound",
		owner, repo, maxMilestonePages)
}

// fetchMilestonePage retrieves a single page of remote milestones.
func fetchMilestonePage(ctx context.Context, client *http.Client, url, tok string) (page []RemoteMilestone, err error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("build milestone request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("Accept", "application/vnd.github.v3+json")

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch remote milestones: %w", err)
	}
	defer func() {
		if cerr := resp.Body.Close(); cerr != nil && err == nil {
			err = fmt.Errorf("close milestone response body: %w", cerr)
		}
	}()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("GitHub API returned HTTP %d: %s", resp.StatusCode, util.ReadErrorBody(resp.Body))
	}

	var remotes []RemoteMilestone
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxAPIResponseBytes)).Decode(&remotes); err != nil {
		return nil, fmt.Errorf("decode remote milestones: %w", err)
	}
	return remotes, nil
}

// remoteProgress computes the completion ratio reported by the forge.
func remoteProgress(rm RemoteMilestone) float64 {
	total := rm.OpenIssues + rm.ClosedIssues
	if total <= 0 {
		return 0.0
	}
	return (float64(rm.ClosedIssues) / float64(total)) * 100.0
}

// applyRemote copies the forge's view onto a local milestone. The remote number is stored
// separately: the local Number is a stable identifier and must not be rewritten, or two
// milestones could end up sharing one number and `milestone close <n>` would become
// ambiguous.
func applyRemote(m *Milestone, rm RemoteMilestone, title string) {
	m.Title = title
	m.RemoteNumber = rm.Number
	m.State = rm.State
	m.OpenIssues = rm.OpenIssues
	m.ClosedIssues = rm.ClosedIssues
	m.Progress = remoteProgress(rm)
	m.UpdatedAt = time.Now().UTC()
	if strings.TrimSpace(m.Description) == "" {
		m.Description = strings.TrimSpace(rm.Description)
	}
	if m.DueOn == nil {
		m.DueOn = rm.DueOn
	}
}

func mergeRemoteMilestones(store *MilestoneStore, remotes []RemoteMilestone) error {
	if err := checkRemoteMergeCapacity(store, remotes); err != nil {
		return err
	}
	byTitle := make(map[string]int, len(store.Milestones))
	nextNum := 1
	for i := 0; i < len(store.Milestones) && i < MaxMilestonesLimit; i++ {
		byTitle[strings.ToLower(store.Milestones[i].Title)] = i
		if store.Milestones[i].Number >= nextNum {
			nextNum = store.Milestones[i].Number + 1
		}
	}

	now := time.Now().UTC()
	for i := 0; i < len(remotes) && i < MaxMilestonesLimit; i++ {
		title := sanitizeTitle(remotes[i].Title)
		key := strings.ToLower(title)
		if idx, exists := byTitle[key]; exists {
			applyRemote(&store.Milestones[idx], remotes[i], title)
			continue
		}
		m := Milestone{Number: nextNum, CreatedAt: now}
		applyRemote(&m, remotes[i], title)
		store.Milestones = append(store.Milestones, m)
		byTitle[key] = len(store.Milestones) - 1
		nextNum++
	}
	return nil
}

// checkRemoteMergeCapacity rejects an oversized union before modifying local IDs
// or milestone fields. Repeated titles consume one slot, matching the merge.
func checkRemoteMergeCapacity(store *MilestoneStore, remotes []RemoteMilestone) error {
	if len(store.Milestones) > MaxMilestonesLimit || len(remotes) > MaxMilestonesLimit {
		return fmt.Errorf("milestone merge input exceeds maximum of %d entries", MaxMilestonesLimit)
	}
	known := make(map[string]struct{}, len(store.Milestones))
	for _, m := range store.Milestones {
		known[strings.ToLower(m.Title)] = struct{}{}
	}
	count := len(store.Milestones)
	for _, rm := range remotes {
		key := strings.ToLower(sanitizeTitle(rm.Title))
		if _, exists := known[key]; exists {
			continue
		}
		count++
		if count > MaxMilestonesLimit {
			return fmt.Errorf("merged milestone store exceeds maximum of %d entries", MaxMilestonesLimit)
		}
		known[key] = struct{}{}
	}
	return nil
}

// PublishMilestone creates the milestone on the forge and persists the number the forge
// assigned to it, so that a later run does not believe the milestone is unpublished.
func PublishMilestone(ctx context.Context, rootPath, owner, repo, token, endpoint string, m *Milestone) error {
	if m == nil {
		return fmt.Errorf("milestone to publish cannot be nil")
	}
	tok := util.ResolveAuthTokenContext(ctx, token)
	if tok == "" {
		return fmt.Errorf("publishing milestone requires GITHUB_TOKEN or authenticated gh CLI session")
	}
	if err := util.ValidateGitHubRepositoryIdentity(owner, repo); err != nil {
		return err
	}

	url := fmt.Sprintf("%s/repos/%s/%s/milestones", util.GitHubAPIBase(endpoint), owner, repo)
	created, err := postMilestone(ctx, url, tok, m)
	if err != nil {
		return err
	}

	m.RemoteNumber = created.Number
	return persistRemoteNumber(ctx, rootPath, m)
}

// postMilestone performs the milestone creation request.
func postMilestone(ctx context.Context, url, tok string, m *Milestone) (created *RemoteMilestone, err error) {
	payload := map[string]any{
		"title":       m.Title,
		"description": m.Description,
		"state":       m.State,
	}
	if m.DueOn != nil {
		payload["due_on"] = m.DueOn.Format(time.RFC3339)
	}

	bodyBytes, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("encode milestone payload: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, fmt.Errorf("build milestone request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("Accept", "application/vnd.github.v3+json")
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: remoteHTTPTimeout}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("create remote milestone: %w", err)
	}
	defer func() {
		if cerr := resp.Body.Close(); cerr != nil && err == nil {
			err = fmt.Errorf("close milestone response body: %w", cerr)
		}
	}()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("GitHub API returned HTTP %d: %s", resp.StatusCode, util.ReadErrorBody(resp.Body))
	}

	var decoded RemoteMilestone
	if decodeErr := json.NewDecoder(io.LimitReader(resp.Body, maxAPIResponseBytes)).Decode(&decoded); decodeErr != nil {
		return nil, fmt.Errorf("decode created remote milestone: %w", decodeErr)
	}
	return &decoded, nil
}

// persistRemoteNumber writes the forge-assigned number back into the local store.
func persistRemoteNumber(ctx context.Context, rootPath string, m *Milestone) error {
	store, err := loadStore(ctx, rootPath)
	if err != nil {
		return fmt.Errorf("reload milestone store after publishing: %w", err)
	}
	for i := 0; i < len(store.Milestones) && i < MaxMilestonesLimit; i++ {
		if store.Milestones[i].Number != m.Number {
			continue
		}
		store.Milestones[i].RemoteNumber = m.RemoteNumber
		store.Milestones[i].UpdatedAt = time.Now().UTC()
		if err := saveStore(ctx, rootPath, store); err != nil {
			return fmt.Errorf("persist remote milestone number: %w", err)
		}
		return nil
	}
	return fmt.Errorf("milestone #%d is not present in the local store", m.Number)
}
