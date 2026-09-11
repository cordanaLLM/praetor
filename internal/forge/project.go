package forge

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/cordanaLLM/praetor/internal/state"
	"github.com/cordanaLLM/praetor/internal/util"
)

const (
	ProjectCacheFile = "project.json"
)

// ProjectV2 represents a GitHub Projects (v2) board.
type ProjectV2 struct {
	ID          string        `json:"id"`
	Number      int           `json:"number"`
	Title       string        `json:"title"`
	Description string        `json:"description,omitempty"`
	URL         string        `json:"url"`
	Closed      bool          `json:"closed"`
	TotalItems  int           `json:"total_items"`
	Items       []ProjectItem `json:"items,omitempty"`
}

// ProjectItem represents an item (issue, PR, or draft) within a Project V2 board.
type ProjectItem struct {
	ID        string    `json:"id"`
	Type      string    `json:"type"` // ISSUE, PULL_REQUEST, DRAFT_ISSUE
	Title     string    `json:"title"`
	Status    string    `json:"status,omitempty"`
	URL       string    `json:"url,omitempty"`
	UpdatedAt time.Time `json:"updated_at"`
}

// ProjectStore defines local cache structure for project boards.
type ProjectStore struct {
	Projects    []ProjectV2 `json:"projects"`
	LastUpdated time.Time   `json:"last_updated"`
}

// ProjectManager coordinates GitHub Projects v2 operations.
type ProjectManager struct {
	Owner      string
	Token      string
	Endpoint   string
	HTTPClient *http.Client
}

// NewProjectManager creates a ProjectManager with resolved auth.
func NewProjectManager(owner, token, endpoint string) *ProjectManager {
	if endpoint == "" {
		endpoint = "https://api.github.com/graphql"
	}
	return &ProjectManager{
		Owner:    owner,
		Token:    util.ResolveAuthToken(token),
		Endpoint: endpoint,
		HTTPClient: &http.Client{
			Timeout: defaultHTTPTimeout,
		},
	}
}

// ListProjects returns projects for the organization from remote or local cache.
func (pm *ProjectManager) ListProjects(ctx context.Context, rootPath string) ([]ProjectV2, error) {
	if pm.Token != "" {
		projects, err := pm.fetchRemoteProjects(ctx)
		if err == nil && len(projects) > 0 {
			if saveErr := pm.saveCache(rootPath, projects); saveErr != nil {
				return nil, fmt.Errorf("cache remote projects: %w", saveErr)
			}
			return projects, nil
		}
	}

	return pm.loadCache(rootPath)
}

// AddItem adds an issue or PR URL to the target project board.
func (pm *ProjectManager) AddItem(ctx context.Context, rootPath string, projectNum int, itemURL string) (*ProjectItem, error) {
	trimmedURL := strings.TrimSpace(itemURL)
	if trimmedURL == "" {
		return nil, fmt.Errorf("item URL cannot be empty")
	}

	// Try remote CLI add if available
	if pm.Token != "" {
		cmdArgs := []string{"project", "item-add", fmt.Sprintf("%d", projectNum), "--owner", pm.Owner, "--url", trimmedURL, "--format", "json"}
		if out, err := util.RunCommand(ctx, "", "gh", cmdArgs...); err == nil {
			var res struct {
				ID string `json:"id"`
			}
			if decodeErr := json.Unmarshal([]byte(out), &res); decodeErr != nil {
				return nil, fmt.Errorf("decode gh project item output: %w", decodeErr)
			}
			item := &ProjectItem{
				ID:        res.ID,
				Type:      "ISSUE",
				Title:     trimmedURL,
				URL:       trimmedURL,
				UpdatedAt: time.Now().UTC(),
			}
			if cacheErr := pm.appendItemToCache(rootPath, projectNum, *item); cacheErr != nil {
				return nil, fmt.Errorf("cache project item: %w", cacheErr)
			}
			return item, nil
		}
	}

	// Fallback to local cache record
	item := &ProjectItem{
		ID:        fmt.Sprintf("item-%d-%d", projectNum, time.Now().Unix()),
		Type:      "ISSUE",
		Title:     trimmedURL,
		URL:       trimmedURL,
		Status:    "Todo",
		UpdatedAt: time.Now().UTC(),
	}
	if err := pm.appendItemToCache(rootPath, projectNum, *item); err != nil {
		return nil, err
	}
	return item, nil
}

func (pm *ProjectManager) fetchRemoteProjects(ctx context.Context) ([]ProjectV2, error) {
	query := fmt.Sprintf(`{"query":"query { organization(login: \"%s\") { projectsV2(first: 20) { nodes { id number title url closed } } } }"}`, pm.Owner)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, pm.Endpoint, bytes.NewBufferString(query))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+pm.Token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := pm.HTTPClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("GitHub GraphQL returned HTTP %d: %s", resp.StatusCode, string(body))
	}

	var res struct {
		Data struct {
			Organization struct {
				ProjectsV2 struct {
					Nodes []ProjectV2 `json:"nodes"`
				} `json:"projectsV2"`
			} `json:"organization"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&res); err != nil {
		return nil, err
	}
	return res.Data.Organization.ProjectsV2.Nodes, nil
}

func (pm *ProjectManager) loadCache(rootPath string) ([]ProjectV2, error) {
	filePath := filepath.Join(rootPath, state.WorkingDirName, ProjectCacheFile)
	if !util.FileExists(filePath) {
		return []ProjectV2{}, nil
	}

	data, err := os.ReadFile(filePath)
	if err != nil {
		return nil, fmt.Errorf("read project cache: %w", err)
	}

	var store ProjectStore
	if err := json.Unmarshal(data, &store); err != nil {
		return nil, fmt.Errorf("unmarshal project cache: %w", err)
	}
	return store.Projects, nil
}

func (pm *ProjectManager) saveCache(rootPath string, projects []ProjectV2) error {
	wDir := filepath.Join(rootPath, state.WorkingDirName)
	if err := os.MkdirAll(wDir, 0755); err != nil {
		return err
	}

	store := ProjectStore{
		Projects:    projects,
		LastUpdated: time.Now().UTC(),
	}
	data, err := json.MarshalIndent(store, "", "  ")
	if err != nil {
		return err
	}

	return os.WriteFile(filepath.Join(wDir, ProjectCacheFile), data, 0644)
}

func (pm *ProjectManager) appendItemToCache(rootPath string, projectNum int, item ProjectItem) error {
	projects, err := pm.loadCache(rootPath)
	if err != nil {
		projects = []ProjectV2{}
	}
	found := false
	for i := range projects {
		if projects[i].Number == projectNum {
			projects[i].Items = append(projects[i].Items, item)
			projects[i].TotalItems = len(projects[i].Items)
			found = true
			break
		}
	}
	if !found {
		projects = append(projects, ProjectV2{
			Number:     projectNum,
			Title:      fmt.Sprintf("Project #%d", projectNum),
			TotalItems: 1,
			Items:      []ProjectItem{item},
		})
	}
	return pm.saveCache(rootPath, projects)
}
