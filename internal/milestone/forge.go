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

// SyncResult is the outcome of a remote milestone sync: the merged store and every
// divergence the sync kept or repaired instead of silently overwriting.
type SyncResult struct {
	// Milestones is the merged local store as persisted.
	Milestones []Milestone
	// PendingCloses lists the local numbers of milestones closed locally that the forge
	// still reports open. Their local closed state was kept, not reverted; publish the
	// close with `milestone close <number> --publish`.
	PendingCloses []int
	// StaleBindings lists local rows that shared a forge binding with another row, left
	// behind by the title-keyed merge that duplicated a milestone on every remote rename.
	// Each was unbound (RemoteNumber reset to 0) and kept as a local-only milestone.
	StaleBindings []StaleBinding
}

// StaleBinding names a local milestone that was unbound from a remote milestone because
// another local row holds the same binding.
type StaleBinding struct {
	Local  int `json:"local"`
	Remote int `json:"remote"`
	Kept   int `json:"kept"`
}

// SyncWithGitHub synchronizes local milestones with GitHub remote milestones.
func SyncWithGitHub(ctx context.Context, rootPath, owner, repo, token, endpoint string) (*SyncResult, error) {
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

	result, err := mergeRemoteMilestones(store, remotes)
	if err != nil {
		return nil, err
	}

	if err := commitStoreAndBacklog(ctx, rootPath, store); err != nil {
		return nil, err
	}
	result.Milestones = store.Milestones
	return result, nil
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
		var batch []RemoteMilestone
		if err := milestoneRequest(ctx, client, http.MethodGet, url, tok, nil, &batch, "fetch remote milestones"); err != nil {
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

// milestoneRequest performs one bounded forge request: it sends payload as JSON when it
// is non-nil, rejects a non-2xx status with a bounded excerpt of the error body, and
// decodes at most maxAPIResponseBytes of the response into out. action names the
// operation in every error.
func milestoneRequest(ctx context.Context, client *http.Client, method, url, tok string, payload, out any, action string) (err error) {
	var body io.Reader
	if payload != nil {
		encoded, encodeErr := json.Marshal(payload)
		if encodeErr != nil {
			return fmt.Errorf("%s: encode payload: %w", action, encodeErr)
		}
		body = bytes.NewReader(encoded)
	}
	req, err := http.NewRequestWithContext(ctx, method, url, body)
	if err != nil {
		return fmt.Errorf("%s: build request: %w", action, err)
	}
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("Accept", "application/vnd.github.v3+json")
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("%s: %w", action, err)
	}
	defer func() {
		if cerr := resp.Body.Close(); cerr != nil && err == nil {
			err = fmt.Errorf("%s: close response body: %w", action, cerr)
		}
	}()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("%s: GitHub API returned HTTP %d: %s", action, resp.StatusCode, util.ReadErrorBody(resp.Body))
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxAPIResponseBytes)).Decode(out); err != nil {
		return fmt.Errorf("%s: decode response: %w", action, err)
	}
	return nil
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
//
// A milestone closed locally whose close the forge has not confirmed keeps its closed
// state while the forge still reports it open; applyRemote reports that divergence
// instead of reverting the close. Once the forge reports the milestone closed, the
// pending flag clears.
func applyRemote(m *Milestone, rm RemoteMilestone, title string) (pendingClose bool) {
	pendingClose = m.PendingRemoteClose && m.State == StateClosed && rm.State != StateClosed
	m.Title = title
	m.RemoteNumber = rm.Number
	if !pendingClose {
		m.State = rm.State
		m.Progress = remoteProgress(rm)
		m.PendingRemoteClose = false
	}
	m.OpenIssues = rm.OpenIssues
	m.ClosedIssues = rm.ClosedIssues
	m.UpdatedAt = time.Now().UTC()
	if strings.TrimSpace(m.Description) == "" {
		m.Description = strings.TrimSpace(rm.Description)
	}
	if m.DueOn == nil {
		m.DueOn = rm.DueOn
	}
	return pendingClose
}

// PublishMilestone creates the milestone on the forge and persists the number the forge
// assigned to it, so that a later run does not believe the milestone is unpublished.
func PublishMilestone(ctx context.Context, rootPath, owner, repo, token, endpoint string, m *Milestone) error {
	if m == nil {
		return fmt.Errorf("milestone to publish cannot be nil")
	}
	tok, err := forgeCredentials(ctx, owner, repo, token, "publishing milestone")
	if err != nil {
		return err
	}

	payload := map[string]any{
		"title":       m.Title,
		"description": m.Description,
		"state":       m.State,
	}
	if m.DueOn != nil {
		payload["due_on"] = m.DueOn.Format(time.RFC3339)
	}
	url := fmt.Sprintf("%s/repos/%s/%s/milestones", util.GitHubAPIBase(endpoint), owner, repo)
	client := &http.Client{Timeout: remoteHTTPTimeout}
	var created RemoteMilestone
	if err := milestoneRequest(ctx, client, http.MethodPost, url, tok, payload, &created, "create remote milestone"); err != nil {
		return err
	}

	m.RemoteNumber = created.Number
	return persistMilestone(ctx, rootPath, m.Number, func(stored *Milestone) {
		stored.RemoteNumber = m.RemoteNumber
	})
}

// PublishClose closes the milestone's bound forge milestone, reads it back and clears the
// pending-close flag only once the forge reports it closed. The milestone must already be
// published: a local-only milestone has nothing on the forge to close.
func PublishClose(ctx context.Context, rootPath, owner, repo, token, endpoint string, m *Milestone) error {
	if m == nil {
		return fmt.Errorf("milestone to close on the forge cannot be nil")
	}
	if m.RemoteNumber <= 0 {
		return fmt.Errorf("milestone #%d is not published to the forge; there is no remote milestone to close", m.Number)
	}
	tok, err := forgeCredentials(ctx, owner, repo, token, "closing a remote milestone")
	if err != nil {
		return err
	}

	url := fmt.Sprintf("%s/repos/%s/%s/milestones/%d", util.GitHubAPIBase(endpoint), owner, repo, m.RemoteNumber)
	client := &http.Client{Timeout: remoteHTTPTimeout}
	var patched, readBack RemoteMilestone
	if err := milestoneRequest(ctx, client, http.MethodPatch, url, tok, map[string]string{"state": StateClosed}, &patched, "close remote milestone"); err != nil {
		return err
	}
	if err := milestoneRequest(ctx, client, http.MethodGet, url, tok, nil, &readBack, "read back remote milestone"); err != nil {
		return err
	}
	if readBack.Number != m.RemoteNumber || readBack.State != StateClosed {
		return fmt.Errorf("remote milestone #%d reads back as #%d in state %q after the close; the close stays pending",
			m.RemoteNumber, readBack.Number, readBack.State)
	}

	m.PendingRemoteClose = false
	return persistMilestone(ctx, rootPath, m.Number, func(stored *Milestone) {
		stored.PendingRemoteClose = false
		stored.OpenIssues = readBack.OpenIssues
		stored.ClosedIssues = readBack.ClosedIssues
	})
}

// forgeCredentials resolves the token and validates the repository identity every forge
// write needs. purpose names the operation in the missing-token error.
func forgeCredentials(ctx context.Context, owner, repo, token, purpose string) (string, error) {
	tok := util.ResolveAuthTokenContext(ctx, token)
	if tok == "" {
		return "", fmt.Errorf("%s requires GITHUB_TOKEN or authenticated gh CLI session", purpose)
	}
	if err := util.ValidateGitHubRepositoryIdentity(owner, repo); err != nil {
		return "", err
	}
	return tok, nil
}

// persistMilestone reloads the store, applies update to the milestone with the given
// local number and saves the store, so a forge round trip never writes back a stale copy
// of the rest of the ledger.
func persistMilestone(ctx context.Context, rootPath string, number int, update func(*Milestone)) error {
	store, err := loadStore(ctx, rootPath)
	if err != nil {
		return fmt.Errorf("reload milestone store after the forge update: %w", err)
	}
	for i := 0; i < len(store.Milestones) && i < MaxMilestonesLimit; i++ {
		if store.Milestones[i].Number != number {
			continue
		}
		update(&store.Milestones[i])
		store.Milestones[i].UpdatedAt = time.Now().UTC()
		if err := saveStore(ctx, rootPath, store); err != nil {
			return fmt.Errorf("persist forge milestone state: %w", err)
		}
		return nil
	}
	return fmt.Errorf("milestone #%d is not present in the local store", number)
}
