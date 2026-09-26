// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package forge

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/cordanaLLM/praetor/internal/state"
)

// graphQLCall is one operation the manager sent to the fake GraphQL endpoint.
type graphQLCall struct {
	Authorization string
	Query         string
	Variables     map[string]any
}

// fakeGraphQL is a hermetic stand-in for https://api.github.com/graphql. The handler
// returns the raw JSON answer for the index-th call.
type fakeGraphQL struct {
	mu    sync.Mutex
	calls []graphQLCall
}

func newFakeGraphQL(t *testing.T, token string, answer func(index int, call graphQLCall) string) (*ProjectManager, *fakeGraphQL) {
	t.Helper()
	fake := &fakeGraphQL{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read request: %v", err)
		}
		var call graphQLCall
		if err := json.Unmarshal(raw, &call); err != nil {
			t.Errorf("request is not a GraphQL JSON body: %v (%s)", err, raw)
		}
		call.Authorization = r.Header.Get("Authorization")
		fake.mu.Lock()
		index := len(fake.calls)
		fake.calls = append(fake.calls, call)
		fake.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		if _, err := w.Write([]byte(answer(index, call))); err != nil {
			t.Errorf("write answer: %v", err)
		}
	}))
	t.Cleanup(srv.Close)
	pm := newTestProjectManager(t, token, srv.URL)
	pm.HTTPClient = srv.Client()
	return pm, fake
}

func (f *fakeGraphQL) recorded() []graphQLCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]graphQLCall{}, f.calls...)
}

func projectsAnswer(nodes string, hasNext bool, cursor string) string {
	next, end := "false", "null"
	if hasNext {
		next = "true"
	}
	if cursor != "" {
		end = `"` + cursor + `"`
	}
	return `{"data":{"repositoryOwner":{"projectsV2":{"nodes":[` + nodes + `],` +
		`"pageInfo":{"hasNextPage":` + next + `,"endCursor":` + end + `}}}}}`
}

// ============================================================================
// ListProjects (BUG-794)
// ============================================================================

func TestProjectList_Positive_ItemCountsAndSecondPage(t *testing.T) {
	dir := setupTestProjectDir(t)
	pm, fake := newFakeGraphQL(t, "test-token", func(index int, _ graphQLCall) string {
		if index == 0 {
			return projectsAnswer(`{"id":"PVT_1","number":1,"title":"Roadmap","url":"u1","closed":false,"items":{"totalCount":42}}`, true, "C1")
		}
		return projectsAnswer(`{"id":"PVT_2","number":2,"title":"Bugs","url":"u2","closed":true,"items":{"totalCount":3}}`, false, "")
	})
	pm.Owner = "octocat" // a user owner: repositoryOwner resolves users and organizations alike

	projects, err := pm.ListProjects(context.Background(), dir)
	if err != nil {
		t.Fatalf("ListProjects: %v", err)
	}
	if len(projects) != 2 || projects[0].TotalItems != 42 || projects[1].TotalItems != 3 || !projects[1].Closed {
		t.Fatalf("item counts or second page lost: %+v", projects)
	}
	calls := fake.recorded()
	if len(calls) != 2 {
		t.Fatalf("expected two pages, got %d calls", len(calls))
	}
	if calls[0].Variables["after"] != nil || calls[1].Variables["after"] != "C1" {
		t.Fatalf("cursor not followed: %v then %v", calls[0].Variables["after"], calls[1].Variables["after"])
	}
	if !strings.Contains(calls[0].Query, "repositoryOwner(login: $owner)") || strings.Contains(calls[0].Query, "organization(") {
		t.Fatalf("query must resolve users as well as organizations: %s", calls[0].Query)
	}
	if !strings.Contains(calls[0].Query, "items { totalCount }") {
		t.Fatalf("query does not request item counts: %s", calls[0].Query)
	}
}

func TestProjectList_Negative_OwnerTravelsAsVariable(t *testing.T) {
	dir := setupTestProjectDir(t)
	hostile := `evil") { viewer { login } } #`
	pm, fake := newFakeGraphQL(t, "test-token", func(int, graphQLCall) string {
		return `{"data":{"repositoryOwner":null}}`
	})
	pm.Owner = hostile

	_, err := pm.ListProjects(context.Background(), dir)
	if err == nil || !strings.Contains(err.Error(), "does not exist") {
		t.Fatalf("an unknown owner must be reported, got %v", err)
	}
	calls := fake.recorded()
	if len(calls) != 1 {
		t.Fatalf("expected one call, got %d", len(calls))
	}
	if strings.Contains(calls[0].Query, "evil") || calls[0].Variables["owner"] != hostile {
		t.Fatalf("owner was spliced into the query instead of sent as a variable: %+v", calls[0])
	}
	if _, statErr := os.Stat(filepath.Join(dir, state.WorkingDirName, ProjectCacheFile)); !os.IsNotExist(statErr) {
		t.Fatalf("a failed listing wrote the cache: %v", statErr)
	}
}

func TestProjectList_Boundary_PageCeilingAndMissingCursor(t *testing.T) {
	dir := setupTestProjectDir(t)
	pm, fake := newFakeGraphQL(t, "test-token", func(int, graphQLCall) string {
		return projectsAnswer(`{"id":"PVT_1","number":1,"title":"t","url":"u","closed":false,"items":{"totalCount":1}}`, true, "again")
	})
	if _, err := pm.ListProjects(context.Background(), dir); err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("an endless listing must stop at the ceiling with an error, got %v", err)
	}
	if n := len(fake.recorded()); n != maxProjectPages {
		t.Fatalf("expected %d pages before the ceiling, got %d", maxProjectPages, n)
	}

	stuck, _ := newFakeGraphQL(t, "test-token", func(int, graphQLCall) string {
		return projectsAnswer("", true, "")
	})
	if _, err := stuck.ListProjects(context.Background(), dir); err == nil || !strings.Contains(err.Error(), "no end cursor") {
		t.Fatalf("a page without a cursor must not loop, got %v", err)
	}
	blank := newTestProjectManager(t, "test-token", "http://127.0.0.1:1")
	blank.Owner = "  "
	if _, err := blank.ListProjects(context.Background(), dir); err == nil {
		t.Fatal("a blank owner must be refused before any request")
	}
}

// ============================================================================
// AddItem (BUG-846, BUG-965)
// ============================================================================

const resolvedTargets = `{"data":{"repositoryOwner":{"projectV2":{"id":"PVT_board"}},` +
	`"resource":{"__typename":"PullRequest","id":"PR_node"}}}`

func TestProjectAddItem_Positive_MutationHonoursTokenAndEndpoint(t *testing.T) {
	isolatePATH(t) // no gh anywhere: the add must not shell out
	t.Setenv("GITHUB_TOKEN", "ambient-token")
	t.Setenv("GH_TOKEN", "ambient-token")
	dir := setupTestProjectDir(t)
	pm, fake := newFakeGraphQL(t, "explicit-token", func(index int, _ graphQLCall) string {
		if index == 0 {
			return resolvedTargets
		}
		return `{"data":{"addProjectV2ItemById":{"item":{"id":"PVTI_new"}}}}`
	})

	item, err := pm.AddItem(context.Background(), dir, 5, "https://github.com/cordanaLLM/praetor/pull/7")
	if err != nil {
		t.Fatalf("AddItem: %v", err)
	}
	if item.ID != "PVTI_new" || item.Type != "PULL_REQUEST" || item.LocalOnly {
		t.Fatalf("unexpected item: %+v", item)
	}
	calls := fake.recorded()
	if len(calls) != 2 {
		t.Fatalf("expected resolve + mutation, got %d calls", len(calls))
	}
	for _, call := range calls {
		if call.Authorization != "Bearer explicit-token" {
			t.Fatalf("explicit token not used: %q", call.Authorization)
		}
	}
	if calls[0].Variables["owner"] != "cordanaLLM" || calls[0].Variables["number"] != float64(5) ||
		calls[0].Variables["url"] != "https://github.com/cordanaLLM/praetor/pull/7" {
		t.Fatalf("resolve variables wrong: %v", calls[0].Variables)
	}
	if !strings.Contains(calls[1].Query, "addProjectV2ItemById") ||
		calls[1].Variables["project"] != "PVT_board" || calls[1].Variables["content"] != "PR_node" {
		t.Fatalf("mutation wrong: %+v", calls[1])
	}
	cached, err := NewCachedProjectManager("cordanaLLM").ListProjects(context.Background(), dir)
	if err != nil || len(cached) != 1 || len(cached[0].Items) != 1 || cached[0].Items[0].ID != "PVTI_new" {
		t.Fatalf("remote add not recorded in the cache: %+v (%v)", cached, err)
	}
}

func TestProjectAddItem_Negative_MutationErrorAndUnresolvedTargets(t *testing.T) {
	isolatePATH(t)
	dir := setupTestProjectDir(t)
	pm, _ := newFakeGraphQL(t, "test-token", func(index int, _ graphQLCall) string {
		if index == 0 {
			return resolvedTargets
		}
		return `{"data":{"addProjectV2ItemById":null},"errors":[{"message":"Content already exists in this project\u001b[2J"}]}`
	})
	_, err := pm.AddItem(context.Background(), dir, 1, "https://github.com/cordanaLLM/praetor/pull/7")
	if err == nil || !strings.Contains(err.Error(), "Content already exists") || strings.Contains(err.Error(), "\x1b") {
		t.Fatalf("mutation error must surface sanitized, got %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(dir, state.WorkingDirName, ProjectCacheFile)); !os.IsNotExist(statErr) {
		t.Fatalf("a failed remote add wrote a cache record: %v", statErr)
	}

	for name, answer := range map[string]string{
		"missing board":     `{"data":{"repositoryOwner":{"projectV2":null},"resource":{"__typename":"Issue","id":"I_1"}}}`,
		"unknown owner":     `{"data":{"repositoryOwner":null,"resource":{"__typename":"Issue","id":"I_1"}}}`,
		"not an issue":      `{"data":{"repositoryOwner":{"projectV2":{"id":"PVT_board"}},"resource":{"__typename":"Repository"}}}`,
		"invisible content": `{"data":{"repositoryOwner":{"projectV2":{"id":"PVT_board"}},"resource":null}}`,
	} {
		unresolved, fake := newFakeGraphQL(t, "test-token", func(int, graphQLCall) string { return answer })
		if _, err := unresolved.AddItem(context.Background(), dir, 1, "https://github.com/o/r/issues/1"); err == nil {
			t.Fatalf("%s: expected an error", name)
		}
		if n := len(fake.recorded()); n != 1 {
			t.Fatalf("%s: the mutation must not be sent after a failed resolve, got %d calls", name, n)
		}
	}
}

func TestProjectAddItem_Boundary_CountKeepsRemoteTotal(t *testing.T) {
	isolatePATH(t)
	dir := setupTestProjectDir(t)
	seed := newTestProjectManager(t, "", "")
	if err := seed.saveCache(dir, []ProjectV2{{Number: 1, Title: "Roadmap", TotalItems: 50}}); err != nil {
		t.Fatalf("seed cache: %v", err)
	}
	pm, _ := newFakeGraphQL(t, "test-token", func(index int, _ graphQLCall) string {
		if index == 0 {
			return `{"data":{"repositoryOwner":{"projectV2":{"id":"PVT_board"}},"resource":{"__typename":"Issue","id":"I_9"}}}`
		}
		return `{"data":{"addProjectV2ItemById":{"item":{"id":"PVTI_9"}}}}`
	})
	item, err := pm.AddItem(context.Background(), dir, 1, "https://github.com/o/r/issues/9")
	if err != nil || item.Type != "ISSUE" {
		t.Fatalf("AddItem: %+v, %v", item, err)
	}
	cached, err := seed.loadCache(dir)
	if err != nil || len(cached) != 1 || cached[0].TotalItems != 51 {
		t.Fatalf("an add must extend the remote total, not reset it to the cached items: %+v (%v)", cached, err)
	}
}
