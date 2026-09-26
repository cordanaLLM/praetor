package forge

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"path/filepath"
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
	// projectsPerPage is the Projects v2 connection page size; GitHub caps first at 100.
	projectsPerPage = 100
	// maxProjectPages bounds board pagination (HISS-02): MaxProjectsLimit boards at most.
	maxProjectPages = MaxProjectsLimit / projectsPerPage
)

// listProjectsQuery pages through an owner's boards with their item counts.
// repositoryOwner resolves an organization or a user alike, and the owner login travels as
// a variable, never spliced into the query text.
const listProjectsQuery = `query($owner: String!, $first: Int!, $after: String) {
  repositoryOwner(login: $owner) {
    ... on ProjectV2Owner {
      projectsV2(first: $first, after: $after) {
        nodes { id number title url closed items { totalCount } }
        pageInfo { hasNextPage endCursor }
      }
    }
  }
}`

// resolveProjectItemQuery resolves the node ids addProjectV2ItemById needs: the board's,
// from its owner and number, and the issue's or pull request's, from its URL.
const resolveProjectItemQuery = `query($owner: String!, $number: Int!, $url: URI!) {
  repositoryOwner(login: $owner) {
    ... on ProjectV2Owner { projectV2(number: $number) { id } }
  }
  resource(url: $url) {
    __typename
    ... on Issue { id }
    ... on PullRequest { id }
  }
}`

// addProjectItemMutation adds an issue or pull request to a board by node id.
const addProjectItemMutation = `mutation($project: ID!, $content: ID!) {
  addProjectV2ItemById(input: {projectId: $project, contentId: $content}) {
    item { id }
  }
}`

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
// items recorded locally and keeping cached boards that the remote no longer reports. The
// remote item count is the board's truth; local-only items, which never reached the
// board, are added on top of it.
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
			board.TotalItems += countLocalOnly(cached[idx].Items)
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

// countLocalOnly counts the cached items that exist only locally.
func countLocalOnly(items []ProjectItem) int {
	count := 0
	for i := 0; i < len(items) && i < MaxProjectItemsLimit; i++ {
		if items[i].LocalOnly {
			count++
		}
	}
	return count
}

// AddItem adds an issue or PR URL to the target project board.
//
// With credentials the item is added through the addProjectV2ItemById GraphQL mutation on
// the manager's own endpoint and token, and a failure is returned: the local cache is never
// used to fabricate a success record for a board the item never reached. Without
// credentials the item is recorded as LocalOnly.
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

// projectItemTargets is the data of resolveProjectItemQuery.
type projectItemTargets struct {
	RepositoryOwner *struct {
		ProjectV2 *struct {
			ID string `json:"id"`
		} `json:"projectV2"`
	} `json:"repositoryOwner"`
	Resource *struct {
		TypeName string `json:"__typename"`
		ID       string `json:"id"`
	} `json:"resource"`
}

// projectItemTypes maps a GraphQL content type to the cached item type.
var projectItemTypes = map[string]string{"Issue": "ISSUE", "PullRequest": "PULL_REQUEST"}

// addedProjectItem is the data of addProjectItemMutation.
type addedProjectItem struct {
	AddProjectV2ItemByID *struct {
		Item *struct {
			ID string `json:"id"`
		} `json:"item"`
	} `json:"addProjectV2ItemById"`
}

// addItemRemote adds the item to the real board with addProjectV2ItemById, through the
// manager's own endpoint and token, and records the resulting item id in the cache.
func (pm *ProjectManager) addItemRemote(ctx context.Context, rootPath string, projectNum int, itemURL string) (*ProjectItem, error) {
	projectID, contentID, itemType, err := pm.resolveItemTargets(ctx, projectNum, itemURL)
	if err != nil {
		return nil, err
	}
	var added addedProjectItem
	vars := map[string]any{"project": projectID, "content": contentID}
	if err := pm.postGraphQL(ctx, addProjectItemMutation, vars, &added); err != nil {
		return nil, fmt.Errorf("add %s to project #%d: %w", itemURL, projectNum, err)
	}
	if added.AddProjectV2ItemByID == nil || added.AddProjectV2ItemByID.Item == nil || added.AddProjectV2ItemByID.Item.ID == "" {
		return nil, fmt.Errorf("adding %s to project #%d returned no item id", itemURL, projectNum)
	}

	return pm.appendItemToCache(rootPath, projectNum, ProjectItem{
		ID:        added.AddProjectV2ItemByID.Item.ID,
		Type:      itemType,
		Title:     itemURL,
		URL:       itemURL,
		UpdatedAt: time.Now().UTC(),
	})
}

// resolveItemTargets resolves the board's node id from its owner and number, and the
// content node id and item type from the issue or pull request URL.
func (pm *ProjectManager) resolveItemTargets(ctx context.Context, projectNum int, itemURL string) (projectID, contentID, itemType string, err error) {
	if strings.TrimSpace(pm.Owner) == "" {
		return "", "", "", errors.New("project owner cannot be empty")
	}
	var targets projectItemTargets
	vars := map[string]any{"owner": pm.Owner, "number": projectNum, "url": itemURL}
	if err := pm.postGraphQL(ctx, resolveProjectItemQuery, vars, &targets); err != nil {
		return "", "", "", fmt.Errorf("resolve project #%d and item %s: %w", projectNum, itemURL, err)
	}
	if targets.RepositoryOwner == nil || targets.RepositoryOwner.ProjectV2 == nil || targets.RepositoryOwner.ProjectV2.ID == "" {
		return "", "", "", fmt.Errorf("project #%d of %q does not exist or is not visible to this token", projectNum, pm.Owner)
	}
	known := false
	if targets.Resource != nil && targets.Resource.ID != "" {
		itemType, known = projectItemTypes[targets.Resource.TypeName]
	}
	if !known {
		return "", "", "", fmt.Errorf("item URL %s does not name an issue or pull request visible to this token", itemURL)
	}
	return targets.RepositoryOwner.ProjectV2.ID, targets.Resource.ID, itemType, nil
}

// projectsConnection is one page of an owner's projectsV2 connection.
type projectsConnection struct {
	Nodes []*struct {
		ID     string `json:"id"`
		Number int    `json:"number"`
		Title  string `json:"title"`
		URL    string `json:"url"`
		Closed bool   `json:"closed"`
		Items  struct {
			TotalCount int `json:"totalCount"`
		} `json:"items"`
	} `json:"nodes"`
	PageInfo struct {
		HasNextPage bool   `json:"hasNextPage"`
		EndCursor   string `json:"endCursor"`
	} `json:"pageInfo"`
}

// projectsPage is the data of listProjectsQuery.
type projectsPage struct {
	RepositoryOwner *struct {
		ProjectsV2 *projectsConnection `json:"projectsV2"`
	} `json:"repositoryOwner"`
}

// boards validates one page and converts its nodes; a null node is skipped.
func (p *projectsPage) boards(owner string) ([]ProjectV2, *projectsConnection, error) {
	if p.RepositoryOwner == nil || p.RepositoryOwner.ProjectsV2 == nil {
		return nil, nil, fmt.Errorf("GitHub owner %q does not exist or is not visible to this token", owner)
	}
	conn := p.RepositoryOwner.ProjectsV2
	if len(conn.Nodes) > projectsPerPage {
		return nil, nil, fmt.Errorf("projects page exceeds %d boards", projectsPerPage)
	}
	boards := make([]ProjectV2, 0, len(conn.Nodes))
	for _, node := range conn.Nodes {
		if node == nil {
			continue
		}
		boards = append(boards, ProjectV2{
			ID: node.ID, Number: node.Number, Title: node.Title, URL: node.URL,
			Closed: node.Closed, TotalItems: node.Items.TotalCount,
		})
	}
	return boards, conn, nil
}

// fetchRemoteProjects lists every board of the owner, an organization or a user, following
// the connection cursor up to maxProjectPages (HISS-02). A listing that still has pages at
// the bound is an error, not a silently shortened list.
func (pm *ProjectManager) fetchRemoteProjects(ctx context.Context) ([]ProjectV2, error) {
	if strings.TrimSpace(pm.Owner) == "" {
		return nil, errors.New("project owner cannot be empty")
	}
	projects := make([]ProjectV2, 0, projectsPerPage)
	var after any // JSON null requests the first page
	for page := 0; page < maxProjectPages; page++ {
		var data projectsPage
		vars := map[string]any{"owner": pm.Owner, "first": projectsPerPage, "after": after}
		if err := pm.postGraphQL(ctx, listProjectsQuery, vars, &data); err != nil {
			return nil, err
		}
		boards, conn, err := data.boards(pm.Owner)
		if err != nil {
			return nil, err
		}
		projects = append(projects, boards...)
		if !conn.PageInfo.HasNextPage {
			return projects, nil
		}
		if conn.PageInfo.EndCursor == "" {
			return nil, errors.New("projects page reports more boards but no end cursor")
		}
		after = conn.PageInfo.EndCursor
	}
	return nil, fmt.Errorf("project listing for %q exceeds %d boards", pm.Owner, MaxProjectsLimit)
}

// graphQLResponse is the envelope of every GitHub GraphQL answer.
type graphQLResponse struct {
	Data   json.RawMessage `json:"data"`
	Errors []struct {
		Message string `json:"message"`
	} `json:"errors"`
}

// postGraphQL sends one GraphQL operation through the GitHub driver's authenticated,
// bounded request path (sendRequest) and decodes its data into out. The query text is a
// constant and every caller-supplied value travels in variables. A non-200 status, a
// non-empty errors array and a missing data object are all failures; response text reaches
// the error only through util.BodyPreview.
func (pm *ProjectManager) postGraphQL(ctx context.Context, query string, variables map[string]any, out any) error {
	transport := &GitHubDriver{Token: pm.Token, Endpoint: pm.Endpoint, HTTPClient: pm.HTTPClient}
	body, status, err := transport.sendRequest(ctx, http.MethodPost, "", map[string]any{"query": query, "variables": variables})
	if err != nil {
		return err
	}
	if status != http.StatusOK {
		return fmt.Errorf("GitHub GraphQL returned HTTP %d: %s", status, util.BodyPreview(body))
	}
	var res graphQLResponse
	if err := json.Unmarshal(body, &res); err != nil {
		return fmt.Errorf("decode GraphQL response (raw: %q): %w", util.BodyPreview(body), err)
	}
	if len(res.Errors) > 0 {
		return fmt.Errorf("GitHub GraphQL reported %d error(s), first: %s",
			len(res.Errors), util.BodyPreview([]byte(res.Errors[0].Message)))
	}
	if len(res.Data) == 0 || string(res.Data) == "null" {
		return errors.New("GitHub GraphQL response carries no data")
	}
	if err := json.Unmarshal(res.Data, out); err != nil {
		return fmt.Errorf("decode GraphQL data: %w", err)
	}
	return nil
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
	// The cached count may carry the board's remote total, which exceeds the items recorded
	// here; resetting it to len(Items) made status under-report a populated board.
	projects[idx].TotalItems = max(projects[idx].TotalItems+1, len(projects[idx].Items))

	if err := pm.saveCache(rootPath, projects); err != nil {
		return nil, err
	}
	return &item, nil
}
