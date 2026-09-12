package forge

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/cordanaLLM/praetor/internal/state"
	"github.com/cordanaLLM/praetor/internal/util"
)

const (
	ProjectCacheFile = "project.json"
	// MaxProjectsLimit bounds every iteration over cached or remote project boards
	// (HISS-02).
	MaxProjectsLimit = 1000
	// MaxProjectItemsLimit bounds how many items a single cached board may hold
	// (HISS-02).
	MaxProjectItemsLimit = 10000
	// projectCacheFilePerm is the mode applied to the local project cache.
	projectCacheFilePerm = 0o644
	// projectCacheDirPerm is the mode applied to the working directory holding the cache.
	projectCacheDirPerm = 0o750
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
	// LocalOnly marks an item that exists only in the local cache because no forge
	// credentials were available when it was recorded; it was never added to the board.
	LocalOnly bool `json:"local_only,omitempty"`
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
//
// HISS-02: the token lookup may shell out to `gh auth token`, which can block on a locked
// OS keyring, so it runs under the caller's context rather than context.Background.
func NewProjectManager(ctx context.Context, owner, token, endpoint string) *ProjectManager {
	if endpoint == "" {
		endpoint = "https://api.github.com/graphql"
	}
	return &ProjectManager{
		Owner:    owner,
		Token:    util.ResolveAuthTokenContext(ctx, token),
		Endpoint: endpoint,
		HTTPClient: &http.Client{
			Timeout: defaultHTTPTimeout,
		},
	}
}

// NewCachedProjectManager creates a ProjectManager that never resolves credentials and
// therefore never reaches the network: it serves the local cache only.
func NewCachedProjectManager(owner string) *ProjectManager {
	return &ProjectManager{
		Owner:    owner,
		Endpoint: "https://api.github.com/graphql",
		HTTPClient: &http.Client{
			Timeout: defaultHTTPTimeout,
		},
	}
}

// ListProjects returns the organization's project boards.
//
// With credentials the remote boards are merged into the local cache by board number, so
// locally recorded items survive a refresh; a remote failure is reported rather than
// masked by silently serving a stale cache. Without credentials the cache is returned
// unchanged and nothing is written.
func (pm *ProjectManager) ListProjects(ctx context.Context, rootPath string) ([]ProjectV2, error) {
	cached, err := pm.loadCache(rootPath)
	if err != nil {
		return nil, err
	}
	if pm.Token == "" {
		return cached, nil
	}

	remote, err := pm.fetchRemoteProjects(ctx)
	if err != nil {
		return nil, fmt.Errorf("list remote projects for %q: %w", pm.Owner, err)
	}

	merged := mergeProjects(cached, remote)
	if saveErr := pm.saveCache(rootPath, merged); saveErr != nil {
		return nil, fmt.Errorf("cache remote projects: %w", saveErr)
	}
	return merged, nil
}

// mergeProjects overlays remote board metadata onto the cached boards, preserving the
// items recorded locally (the remote projectsV2 selection carries no items) and keeping
// cached boards that the remote no longer reports.
func mergeProjects(cached, remote []ProjectV2) []ProjectV2 {
	byNumber := make(map[int]int, len(cached))
	for i := 0; i < len(cached) && i < MaxProjectsLimit; i++ {
		byNumber[cached[i].Number] = i
	}

	merged := make([]ProjectV2, 0, len(cached)+len(remote))
	seen := make(map[int]bool, len(remote))
	for i := 0; i < len(remote) && i < MaxProjectsLimit; i++ {
		board := remote[i]
		if idx, ok := byNumber[board.Number]; ok {
			board.Items = cached[idx].Items
			if board.TotalItems == 0 {
				board.TotalItems = cached[idx].TotalItems
			}
		}
		seen[board.Number] = true
		merged = append(merged, board)
	}
	for i := 0; i < len(cached) && i < MaxProjectsLimit; i++ {
		if !seen[cached[i].Number] {
			merged = append(merged, cached[i])
		}
	}
	return merged
}

// AddItem adds an issue or PR URL to the target project board.
//
// With credentials the item is added through `gh project item-add` and a failure is
// returned: the local cache is never used to fabricate a success record for a board the
// item never reached. Without credentials the item is recorded as LocalOnly.
func (pm *ProjectManager) AddItem(ctx context.Context, rootPath string, projectNum int, itemURL string) (*ProjectItem, error) {
	trimmedURL := strings.TrimSpace(itemURL)
	if trimmedURL == "" {
		return nil, fmt.Errorf("item URL cannot be empty")
	}
	if projectNum <= 0 {
		return nil, fmt.Errorf("project number must be positive, got %d", projectNum)
	}
	if err := util.ValidateExecArg(trimmedURL); err != nil {
		return nil, fmt.Errorf("reject item URL %q: %w", trimmedURL, err)
	}

	if pm.Token != "" {
		return pm.addItemRemote(ctx, rootPath, projectNum, trimmedURL)
	}

	return pm.appendItemToCache(rootPath, projectNum, ProjectItem{
		Type:      "ISSUE",
		Title:     trimmedURL,
		URL:       trimmedURL,
		Status:    "Todo",
		UpdatedAt: time.Now().UTC(),
		LocalOnly: true,
	})
}

// addItemRemote adds the item to the real board through the gh CLI and records the
// resulting item id in the cache.
func (pm *ProjectManager) addItemRemote(ctx context.Context, rootPath string, projectNum int, itemURL string) (*ProjectItem, error) {
	if err := util.ValidateExecArg(pm.Owner); err != nil {
		return nil, fmt.Errorf("reject project owner %q: %w", pm.Owner, err)
	}

	cmdArgs := []string{
		"project", "item-add", strconv.Itoa(projectNum),
		"--owner", pm.Owner, "--url", itemURL, "--format", "json",
	}
	out, err := util.RunCommand(ctx, "", "gh", cmdArgs...)
	if err != nil {
		return nil, fmt.Errorf("gh project item-add %d --url %s: %w (output: %s)",
			projectNum, itemURL, err, truncateExcerpt(out, maxErrorBodyBytes))
	}

	var res struct {
		ID string `json:"id"`
	}
	if decodeErr := json.Unmarshal([]byte(out), &res); decodeErr != nil {
		return nil, fmt.Errorf("decode gh project item output: %w", decodeErr)
	}
	if strings.TrimSpace(res.ID) == "" {
		return nil, fmt.Errorf("gh project item-add %d returned no item id", projectNum)
	}

	return pm.appendItemToCache(rootPath, projectNum, ProjectItem{
		ID:        res.ID,
		Type:      "ISSUE",
		Title:     itemURL,
		URL:       itemURL,
		UpdatedAt: time.Now().UTC(),
	})
}

func (pm *ProjectManager) fetchRemoteProjects(ctx context.Context) (projects []ProjectV2, err error) {
	query := fmt.Sprintf(`{"query":"query { organization(login: \"%s\") { projectsV2(first: 20) { nodes { id number title url closed } } } }"}`, pm.Owner)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, pm.Endpoint, bytes.NewBufferString(query))
	if err != nil {
		return nil, fmt.Errorf("build projects request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+pm.Token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := pm.HTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("projects request failed: %w", err)
	}
	defer func() {
		if cerr := resp.Body.Close(); cerr != nil && err == nil {
			err = fmt.Errorf("close projects response body: %w", cerr)
		}
	}()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GitHub GraphQL returned HTTP %d: %s",
			resp.StatusCode, readErrorBody(resp.Body))
	}

	var res struct {
		Data struct {
			Organization struct {
				ProjectsV2 struct {
					Nodes []ProjectV2 `json:"nodes"`
				} `json:"projectsV2"`
			} `json:"organization"`
		} `json:"data"`
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxHTTPResponseBody)).Decode(&res); err != nil {
		return nil, fmt.Errorf("decode projects response: %w", err)
	}
	if len(res.Errors) > 0 {
		return nil, fmt.Errorf("GitHub GraphQL reported %d error(s), first: %s",
			len(res.Errors), truncateExcerpt(res.Errors[0].Message, maxErrorBodyBytes))
	}
	return res.Data.Organization.ProjectsV2.Nodes, nil
}

// projectCachePath resolves the cache file inside rootPath, refusing a path that escapes
// the repository root through "..' or a symbolic link.
func projectCachePath(rootPath string) (string, error) {
	return util.ConfinePath(rootPath, filepath.Join(state.WorkingDirName, ProjectCacheFile))
}

func (pm *ProjectManager) loadCache(rootPath string) ([]ProjectV2, error) {
	filePath, err := projectCachePath(rootPath)
	if err != nil {
		return nil, fmt.Errorf("resolve project cache path: %w", err)
	}
	if !util.FileExists(filePath) {
		return []ProjectV2{}, nil
	}

	data, err := util.ReadFileNoFollow(filePath)
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
	filePath, err := projectCachePath(rootPath)
	if err != nil {
		return fmt.Errorf("resolve project cache path: %w", err)
	}
	if err := util.MkdirSecure(filepath.Dir(filePath), projectCacheDirPerm); err != nil {
		return fmt.Errorf("create project cache directory: %w", err)
	}

	store := ProjectStore{
		Projects:    projects,
		LastUpdated: time.Now().UTC(),
	}
	data, err := json.MarshalIndent(store, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal project cache: %w", err)
	}

	if err := util.WriteFileNoFollow(filePath, data, projectCacheFilePerm); err != nil {
		return fmt.Errorf("write project cache: %w", err)
	}
	return nil
}

// appendItemToCache records item on board projectNum, assigning a collision-free id to
// local-only items. A cache that fails to load is reported, never silently replaced.
func (pm *ProjectManager) appendItemToCache(rootPath string, projectNum int, item ProjectItem) (*ProjectItem, error) {
	projects, err := pm.loadCache(rootPath)
	if err != nil {
		return nil, fmt.Errorf("load project cache before recording item: %w", err)
	}

	idx := -1
	for i := 0; i < len(projects) && i < MaxProjectsLimit; i++ {
		if projects[i].Number == projectNum {
			idx = i
			break
		}
	}
	if idx == -1 {
		if len(projects) >= MaxProjectsLimit {
			return nil, fmt.Errorf("project cache holds the maximum of %d boards", MaxProjectsLimit)
		}
		projects = append(projects, ProjectV2{
			Number: projectNum,
			Title:  fmt.Sprintf("Project #%d", projectNum),
		})
		idx = len(projects) - 1
	}
	if len(projects[idx].Items) >= MaxProjectItemsLimit {
		return nil, fmt.Errorf("board #%d already holds the maximum of %d items", projectNum, MaxProjectItemsLimit)
	}

	if item.ID == "" {
		item.ID = fmt.Sprintf("local-%d-%d-%d", projectNum, len(projects[idx].Items)+1, time.Now().UTC().UnixNano())
	}
	projects[idx].Items = append(projects[idx].Items, item)
	projects[idx].TotalItems = len(projects[idx].Items)

	if err := pm.saveCache(rootPath, projects); err != nil {
		return nil, err
	}
	return &item, nil
}
