package forge

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/state"
)

func setupTestProjectDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	wDir := filepath.Join(dir, state.WorkingDirName)
	if err := os.MkdirAll(wDir, 0o750); err != nil {
		t.Fatalf("failed creating test workingdir: %v", err)
	}
	return dir
}

// newTestProjectManager builds a manager without resolving any credential: the tests must
// never read GITHUB_TOKEN/GH_TOKEN or shell out to `gh`, and must never reach GitHub.
func newTestProjectManager(t *testing.T, token, endpoint string) *ProjectManager {
	t.Helper()
	return &ProjectManager{
		Owner:      "cordanaLLM",
		Token:      token,
		Endpoint:   endpoint,
		HTTPClient: &http.Client{Timeout: defaultHTTPTimeout},
	}
}

// isolatePATH points PATH at an empty directory so that no `gh` binary on the developer's
// machine can be executed by a test.
func isolatePATH(t *testing.T) {
	t.Helper()
	t.Setenv("PATH", t.TempDir())
}

// ============================================================================
// Positive
// ============================================================================

func TestProject_Positive_LocalLifecycle(t *testing.T) {
	dir := setupTestProjectDir(t)
	pm := newTestProjectManager(t, "", "")

	item, err := pm.AddItem(context.Background(), dir, 1, "https://github.com/cordanaLLM/praetor/issues/42")
	if err != nil {
		t.Fatalf("AddItem failed: %v", err)
	}
	if item.URL != "https://github.com/cordanaLLM/praetor/issues/42" {
		t.Errorf("unexpected item URL: %s", item.URL)
	}
	if !item.LocalOnly {
		t.Errorf("expected a credential-less add to be marked local-only: %+v", item)
	}

	projects, err := pm.ListProjects(context.Background(), dir)
	if err != nil {
		t.Fatalf("ListProjects failed: %v", err)
	}
	if len(projects) != 1 {
		t.Fatalf("expected 1 project, got %d", len(projects))
	}
	if projects[0].Number != 1 || len(projects[0].Items) != 1 {
		t.Errorf("unexpected project details: %+v", projects[0])
	}
}

func TestProject_Positive_RemoteMergePreservesCachedItems(t *testing.T) {
	dir := setupTestProjectDir(t)

	local := newTestProjectManager(t, "", "")
	if _, err := local.AddItem(context.Background(), dir, 1, "https://github.com/cordanaLLM/praetor/issues/42"); err != nil {
		t.Fatalf("AddItem failed: %v", err)
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if _, werr := w.Write([]byte(`{"data":{"organization":{"projectsV2":{"nodes":[
			{"id":"PVT_1","number":1,"title":"Governance","url":"https://example.test/1","closed":false}]}}}}`)); werr != nil {
			t.Errorf("write fake response: %v", werr)
		}
	}))
	defer srv.Close()

	remote := newTestProjectManager(t, "test-token", srv.URL)
	remote.HTTPClient = srv.Client()

	projects, err := remote.ListProjects(context.Background(), dir)
	if err != nil {
		t.Fatalf("ListProjects against the fake forge failed: %v", err)
	}
	if len(projects) != 1 {
		t.Fatalf("expected 1 merged board, got %d", len(projects))
	}
	if projects[0].Title != "Governance" {
		t.Errorf("expected remote metadata to win, got %+v", projects[0])
	}
	if len(projects[0].Items) != 1 || projects[0].TotalItems != 1 {
		t.Fatalf("remote refresh wiped the locally tracked items: %+v", projects[0])
	}

	// The merge must be persisted, not only returned.
	cached, err := local.ListProjects(context.Background(), dir)
	if err != nil {
		t.Fatalf("reloading the cache failed: %v", err)
	}
	if len(cached) != 1 || len(cached[0].Items) != 1 {
		t.Errorf("cache lost the tracked item after a remote refresh: %+v", cached)
	}
}

func TestProject_Positive_NewProjectManagerDefaults(t *testing.T) {
	isolatePATH(t)
	t.Setenv("GITHUB_TOKEN", "env-token")
	t.Setenv("GH_TOKEN", "")

	pm := NewProjectManager(context.Background(), "cordanaLLM", "", "")
	if pm.Endpoint != "https://api.github.com/graphql" {
		t.Errorf("unexpected default endpoint: %s", pm.Endpoint)
	}
	if pm.Token != "env-token" {
		t.Errorf("expected the environment token to be resolved, got %q", pm.Token)
	}

	cacheOnly := NewCachedProjectManager("cordanaLLM")
	if cacheOnly.Token != "" {
		t.Errorf("cache-only manager must not resolve a token, got %q", cacheOnly.Token)
	}
}

// ============================================================================
// Negative
// ============================================================================

func TestProject_Negative_EmptyURLAndCorruptCache(t *testing.T) {
	dir := setupTestProjectDir(t)
	pm := newTestProjectManager(t, "", "")

	if _, err := pm.AddItem(context.Background(), dir, 1, "   "); err == nil {
		t.Fatalf("expected error for empty URL, got nil")
	}
	if _, err := pm.AddItem(context.Background(), dir, 0, "https://example.test/issues/1"); err == nil {
		t.Fatalf("expected error for non-positive project number, got nil")
	}
	if _, err := pm.AddItem(context.Background(), dir, 1, "--owner=evil"); err == nil {
		t.Fatalf("expected an option-shaped item URL to be rejected, got nil")
	}

	corruptPath := filepath.Join(dir, state.WorkingDirName, ProjectCacheFile)
	if err := os.WriteFile(corruptPath, []byte("NOT_JSON"), 0o600); err != nil {
		t.Fatalf("failed writing corrupt cache: %v", err)
	}

	if _, err := pm.ListProjects(context.Background(), dir); err == nil {
		t.Fatalf("expected error reading corrupt cache, got nil")
	}

	// A corrupt cache must never be silently replaced by a single-item file.
	if _, err := pm.AddItem(context.Background(), dir, 3, "https://example.test/issues/7"); err == nil {
		t.Fatalf("expected AddItem to refuse a corrupt cache, got nil")
	}
	data, err := os.ReadFile(corruptPath)
	if err != nil {
		t.Fatalf("read cache after refused add: %v", err)
	}
	if string(data) != "NOT_JSON" {
		t.Errorf("corrupt cache was overwritten: %q", string(data))
	}
}

func TestProject_Negative_RemoteFailuresAreReported(t *testing.T) {
	dir := setupTestProjectDir(t)

	bigBody := strings.Repeat("E", 200*1024)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		if _, werr := w.Write([]byte(bigBody)); werr != nil {
			t.Errorf("write fake response: %v", werr)
		}
	}))
	defer srv.Close()

	pm := newTestProjectManager(t, "test-token", srv.URL)
	pm.HTTPClient = srv.Client()

	_, err := pm.ListProjects(context.Background(), dir)
	if err == nil {
		t.Fatalf("expected the HTTP 502 to be reported, got nil")
	}
	if !strings.Contains(err.Error(), "HTTP 502") {
		t.Errorf("unexpected error: %v", err)
	}
	if len(err.Error()) > maxErrorBodyBytes+4096 {
		t.Errorf("error body was not bounded: %d bytes", len(err.Error()))
	}
}

func TestProject_Negative_GraphQLErrorsAndGhFailure(t *testing.T) {
	dir := setupTestProjectDir(t)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if _, werr := w.Write([]byte(`{"data":{"organization":{"projectsV2":{"nodes":[]}}},` +
			`"errors":[{"message":"Resource not accessible by integration"}]}`)); werr != nil {
			t.Errorf("write fake response: %v", werr)
		}
	}))
	defer srv.Close()

	pm := newTestProjectManager(t, "test-token", srv.URL)
	pm.HTTPClient = srv.Client()
	if _, err := pm.ListProjects(context.Background(), dir); err == nil {
		t.Fatalf("expected a GraphQL errors array to surface, got nil")
	}

	// With credentials present, a failing `gh project item-add` must be reported and must
	// not fabricate a local success record.
	isolatePATH(t)
	if _, err := pm.AddItem(context.Background(), dir, 1, "https://github.com/cordanaLLM/praetor/issues/42"); err == nil {
		t.Fatalf("expected the failing gh invocation to be reported, got nil")
	}
	if _, err := os.Stat(filepath.Join(dir, state.WorkingDirName, ProjectCacheFile)); !os.IsNotExist(err) {
		t.Errorf("a failed remote add wrote a cache record: %v", err)
	}
}

// ============================================================================
// Boundary
// ============================================================================

func TestProject_Boundary_MultipleItemsAndEmpty(t *testing.T) {
	dir := setupTestProjectDir(t)
	pm := newTestProjectManager(t, "", "")

	projects, err := pm.ListProjects(context.Background(), dir)
	if err != nil {
		t.Fatalf("ListProjects on empty failed: %v", err)
	}
	if len(projects) != 0 {
		t.Errorf("expected 0 projects, got %d", len(projects))
	}

	seen := make(map[string]bool)
	urls := []string{
		"https://github.com/cordanaLLM/praetor/issues/1",
		"https://github.com/cordanaLLM/praetor/issues/2",
	}
	for _, u := range urls {
		item, addErr := pm.AddItem(context.Background(), dir, 1, u)
		if addErr != nil {
			t.Fatalf("AddItem(%s) failed: %v", u, addErr)
		}
		if seen[item.ID] {
			t.Fatalf("duplicate local item ID %q for items added in the same second", item.ID)
		}
		seen[item.ID] = true
	}
	if _, err := pm.AddItem(context.Background(), dir, 2, "https://github.com/cordanaLLM/praetor/pull/3"); err != nil {
		t.Fatalf("AddItem on second board failed: %v", err)
	}

	projects, err = pm.ListProjects(context.Background(), dir)
	if err != nil {
		t.Fatalf("ListProjects after additions failed: %v", err)
	}
	if len(projects) != 2 {
		t.Fatalf("expected 2 projects, got %d", len(projects))
	}
	if projects[0].TotalItems != 2 {
		t.Errorf("expected project 1 to have 2 items, got %d", projects[0].TotalItems)
	}
	if projects[1].TotalItems != 1 {
		t.Errorf("expected project 2 to have 1 item, got %d", projects[1].TotalItems)
	}
}

func TestProject_Boundary_MergeKeepsBoardsMissingRemotely(t *testing.T) {
	cached := []ProjectV2{
		{Number: 1, Title: "Local Only", TotalItems: 2, Items: []ProjectItem{{ID: "a"}, {ID: "b"}}},
		{Number: 2, Title: "Stale", TotalItems: 1, Items: []ProjectItem{{ID: "c"}}},
	}
	remote := []ProjectV2{{Number: 1, Title: "Governance"}}

	merged := mergeProjects(cached, remote)
	if len(merged) != 2 {
		t.Fatalf("expected 2 merged boards, got %d", len(merged))
	}
	if merged[0].Title != "Governance" || merged[0].TotalItems != 2 || len(merged[0].Items) != 2 {
		t.Errorf("merge lost cached items: %+v", merged[0])
	}
	if merged[1].Number != 2 || merged[1].TotalItems != 1 {
		t.Errorf("merge dropped a board the remote does not report: %+v", merged[1])
	}

	if got := mergeProjects(nil, nil); len(got) != 0 {
		t.Errorf("expected empty merge of empty inputs, got %d", len(got))
	}
}

func TestProject_Boundary_SymlinkedCacheIsRefused(t *testing.T) {
	dir := setupTestProjectDir(t)
	victim := filepath.Join(t.TempDir(), "victim.json")
	if err := os.WriteFile(victim, []byte("do not touch"), 0o600); err != nil {
		t.Fatalf("write victim: %v", err)
	}
	cachePath := filepath.Join(dir, state.WorkingDirName, ProjectCacheFile)
	if err := os.Symlink(victim, cachePath); err != nil {
		t.Skipf("symlinks unsupported on this platform: %v", err)
	}

	pm := newTestProjectManager(t, "", "")
	if _, err := pm.AddItem(context.Background(), dir, 1, "https://example.test/issues/1"); err == nil {
		t.Fatalf("expected a symlinked cache to be refused, got nil")
	}
	data, err := os.ReadFile(victim)
	if err != nil {
		t.Fatalf("read victim: %v", err)
	}
	if string(data) != "do not touch" {
		t.Errorf("write followed the symlink: %q", string(data))
	}
}
