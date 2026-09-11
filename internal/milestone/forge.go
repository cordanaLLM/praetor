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
	tok := util.ResolveAuthToken(token)
	if tok == "" {
		return nil, fmt.Errorf("GitHub milestone sync requires GITHUB_TOKEN or authenticated gh CLI session")
	}

	remotes, err := fetchRemoteMilestones(ctx, owner, repo, tok, endpoint)
	if err != nil {
		return nil, err
	}

	store, err := loadStore(rootPath)
	if err != nil {
		return nil, err
	}

	mergeRemoteMilestones(store, remotes)

	if err := saveStore(rootPath, store); err != nil {
		return nil, err
	}

	if err := SyncToBacklog(rootPath); err != nil {
		return nil, fmt.Errorf("sync to backlog: %w", err)
	}
	return store.Milestones, nil
}

func fetchRemoteMilestones(ctx context.Context, owner, repo, tok, endpoint string) ([]RemoteMilestone, error) {
	apiBase := strings.TrimRight(endpoint, "/")
	if apiBase == "" || apiBase == "https://github.com" {
		apiBase = "https://api.github.com"
	}

	url := fmt.Sprintf("%s/repos/%s/%s/milestones?state=all&per_page=100", apiBase, owner, repo)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("Accept", "application/vnd.github.v3+json")

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch remote milestones: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		bodyBytes, readErr := io.ReadAll(resp.Body)
		if readErr != nil {
			return nil, fmt.Errorf("GitHub API returned HTTP %d (reading body failed: %w)", resp.StatusCode, readErr)
		}
		return nil, fmt.Errorf("GitHub API returned HTTP %d: %s", resp.StatusCode, string(bodyBytes))
	}

	var remotes []RemoteMilestone
	if err := json.NewDecoder(resp.Body).Decode(&remotes); err != nil {
		return nil, fmt.Errorf("decode remote milestones: %w", err)
	}
	return remotes, nil
}

func mergeRemoteMilestones(store *MilestoneStore, remotes []RemoteMilestone) {
	byTitle := make(map[string]int)
	for i, m := range store.Milestones {
		byTitle[strings.ToLower(m.Title)] = i
	}

	for _, rm := range remotes {
		total := rm.OpenIssues + rm.ClosedIssues
		progress := 0.0
		if total > 0 {
			progress = (float64(rm.ClosedIssues) / float64(total)) * 100.0
		}

		idx, exists := byTitle[strings.ToLower(rm.Title)]
		if exists {
			store.Milestones[idx].Number = rm.Number
			store.Milestones[idx].State = rm.State
			store.Milestones[idx].OpenIssues = rm.OpenIssues
			store.Milestones[idx].ClosedIssues = rm.ClosedIssues
			store.Milestones[idx].Progress = progress
			store.Milestones[idx].UpdatedAt = time.Now().UTC()
		} else {
			store.Milestones = append(store.Milestones, Milestone{
				Number:       rm.Number,
				Title:        rm.Title,
				Description:  rm.Description,
				State:        rm.State,
				DueOn:        rm.DueOn,
				OpenIssues:   rm.OpenIssues,
				ClosedIssues: rm.ClosedIssues,
				Progress:     progress,
				CreatedAt:    time.Now().UTC(),
				UpdatedAt:    time.Now().UTC(),
			})
		}
	}
}

// PublishMilestone creates a remote milestone on GitHub for a newly created milestone.
func PublishMilestone(ctx context.Context, owner, repo, token, endpoint string, m *Milestone) error {
	tok := util.ResolveAuthToken(token)
	if tok == "" {
		return fmt.Errorf("publishing milestone requires GITHUB_TOKEN or authenticated gh CLI session")
	}

	apiBase := strings.TrimRight(endpoint, "/")
	if apiBase == "" || apiBase == "https://github.com" {
		apiBase = "https://api.github.com"
	}

	payload := map[string]interface{}{
		"title":       m.Title,
		"description": m.Description,
		"state":       m.State,
	}
	if m.DueOn != nil {
		payload["due_on"] = m.DueOn.Format(time.RFC3339)
	}

	bodyBytes, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	url := fmt.Sprintf("%s/repos/%s/%s/milestones", apiBase, owner, repo)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(bodyBytes))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("Accept", "application/vnd.github.v3+json")
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("create remote milestone: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		out, readErr := io.ReadAll(resp.Body)
		if readErr != nil {
			return fmt.Errorf("GitHub API returned HTTP %d (reading body failed: %w)", resp.StatusCode, readErr)
		}
		return fmt.Errorf("GitHub API returned HTTP %d: %s", resp.StatusCode, string(out))
	}

	var created RemoteMilestone
	if err := json.NewDecoder(resp.Body).Decode(&created); err != nil {
		return fmt.Errorf("decode created remote milestone: %w", err)
	}
	m.Number = created.Number
	return nil
}
