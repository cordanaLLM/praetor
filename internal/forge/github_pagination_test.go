// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package forge

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/config"
)

// namedPage builds one listing page of count {id|number, name|title} entries.
func namedPage(idKey, nameKey string, start, count int, prefix string) []map[string]any {
	page := make([]map[string]any, 0, count)
	for i := 0; i < count; i++ {
		page = append(page, map[string]any{idKey: start + i, nameKey: fmt.Sprintf("%s-%d", prefix, start+i)})
	}
	return page
}

func methodsOf(fake *fakeForgeServer) []string {
	methods := make([]string, 0, len(fake.requests))
	for _, req := range fake.requests {
		methods = append(methods, req.Method)
	}
	return methods
}

// ============================================================================
// Ruleset listing pagination (BUG-843)
// ============================================================================

// pagedRulesetForge serves the ruleset listing from pages (page number -> entries; an
// absent page is empty) and every call on one ruleset from rs, so the driver can read the
// live ruleset it updates and read back what it wrote.
func pagedRulesetForge(t *testing.T, pages map[string][]map[string]any, rs *rulesetServer) (*GitHubDriver, *fakeForgeServer) {
	t.Helper()
	var fake *fakeForgeServer
	gh, fake := newFakeForge(t, func(w http.ResponseWriter, r *http.Request, index int) {
		if r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/rulesets") {
			entries := pages[r.URL.Query().Get("page")]
			if entries == nil {
				entries = []map[string]any{}
			}
			writeJSON(t, w, http.StatusOK, entries)
			return
		}
		rs.serve(w, r.Method, r.URL.Path, fake.requests[index].Body)
	})
	return gh, fake
}

func TestGitHubDriver_Rulesets_Positive_SecondPageRulesetIsUpdatedNotDuplicated(t *testing.T) {
	live := map[string]any{"id": 777, "name": "main-branch-protection"}
	pages := map[string][]map[string]any{
		"1": namedPage("id", "name", 1, issuesPerPage, "other"),
		"2": {live},
	}
	gh, fake := pagedRulesetForge(t, pages, &rulesetServer{existing: []map[string]any{live}})
	if err := gh.ReconcileProtection(context.Background(), "main", &config.BranchProtectionPolicy{}); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if got := strings.Join(methodsOf(fake), ","); got != "GET,GET,GET,PUT,GET" {
		t.Fatalf("expected two listing pages, the live read, an in-place update and a readback, got %s", got)
	}
	for _, i := range []int{2, 3, 4} {
		if fake.requests[i].Path != "/repos/acme/widgets/rulesets/777" {
			t.Fatalf("request %d targeted the wrong ruleset: %s", i, fake.requests[i].Path)
		}
	}
}

func TestGitHubDriver_Rulesets_Negative_PageCeilingFailsBeforeMutation(t *testing.T) {
	gh, fake := newFakeForge(t, func(w http.ResponseWriter, r *http.Request, index int) {
		if r.Method != http.MethodGet {
			t.Errorf("mutation %s sent against an incomplete listing", r.Method)
		}
		writeJSON(t, w, http.StatusOK, namedPage("id", "name", index*issuesPerPage+1, issuesPerPage, "other"))
	})
	err := gh.ReconcileProtection(context.Background(), "main", &config.BranchProtectionPolicy{})
	if !errors.Is(err, errPageCeiling) {
		t.Fatalf("expected the page ceiling to be an explicit error, got %v", err)
	}
	if len(fake.requests) != maxRulesetPages {
		t.Fatalf("expected exactly %d listing pages, got %d", maxRulesetPages, len(fake.requests))
	}
}

func TestGitHubDriver_Rulesets_Boundary_FullFirstPageThenEmptyCreates(t *testing.T) {
	pages := map[string][]map[string]any{"1": namedPage("id", "name", 1, issuesPerPage, "other")}
	gh, fake := pagedRulesetForge(t, pages, &rulesetServer{})
	if err := gh.ReconcileProtection(context.Background(), "main", &config.BranchProtectionPolicy{}); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if got := strings.Join(methodsOf(fake), ","); got != "GET,GET,POST,GET" {
		t.Fatalf("a full first page must be followed before creating and reading back, got %s", got)
	}
	if got := fake.requests[3].Path; got != fmt.Sprintf("/repos/acme/widgets/rulesets/%d", createdRulesetID) {
		t.Fatalf("readback targeted the wrong ruleset: %s", got)
	}
}

// ============================================================================
// Milestone resolution (BUG-843)
// ============================================================================

func TestGitHubDriver_ResolveMilestone_Positive_BeyondFifthPageAndCaseFolded(t *testing.T) {
	const targetPage = 7
	gh, fake := newFakeForge(t, func(w http.ResponseWriter, r *http.Request, index int) {
		if index+1 < targetPage {
			writeJSON(t, w, http.StatusOK, namedPage("number", "title", index*issuesPerPage+1, issuesPerPage, "m"))
			return
		}
		writeJSON(t, w, http.StatusOK, []map[string]any{{"number": 4242, "title": "Release 1.0"}})
	})
	number, err := gh.resolveMilestone(context.Background(), "release 1.0")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if number != 4242 {
		t.Fatalf("expected milestone 4242, got %d", number)
	}
	if len(fake.requests) != targetPage {
		t.Fatalf("expected %d listing pages, got %d", targetPage, len(fake.requests))
	}
	if !strings.Contains(fake.requests[0].Query, "state=all") {
		t.Fatalf("closed milestones must be listed too: %s", fake.requests[0].Query)
	}
}

func TestGitHubDriver_ResolveMilestone_Negative_CeilingAndAbsent(t *testing.T) {
	gh, fake := newFakeForge(t, func(w http.ResponseWriter, r *http.Request, index int) {
		writeJSON(t, w, http.StatusOK, namedPage("number", "title", index*issuesPerPage+1, issuesPerPage, "m"))
	})
	_, err := gh.resolveMilestone(context.Background(), "v9")
	if !errors.Is(err, errPageCeiling) || strings.Contains(err.Error(), "does not exist") {
		t.Fatalf("an incomplete listing must not claim absence, got %v", err)
	}
	if len(fake.requests) != maxMilestonePages {
		t.Fatalf("expected %d listing pages, got %d", maxMilestonePages, len(fake.requests))
	}

	short, _ := newFakeForge(t, func(w http.ResponseWriter, _ *http.Request, _ int) {
		writeJSON(t, w, http.StatusOK, []map[string]any{{"number": 1, "title": "v1"}})
	})
	if _, err := short.resolveMilestone(context.Background(), "v9"); err == nil || !strings.Contains(err.Error(), "does not exist") {
		t.Fatalf("a complete listing without the title must report absence, got %v", err)
	}
}

func TestGitHubDriver_ResolveMilestone_Boundary_ExactTitleBeatsFoldedMatch(t *testing.T) {
	gh, _ := newFakeForge(t, func(w http.ResponseWriter, r *http.Request, index int) {
		if index == 0 {
			page := namedPage("number", "title", 1, issuesPerPage-1, "m")
			writeJSON(t, w, http.StatusOK, append(page, map[string]any{"number": 11, "title": "V1"}))
			return
		}
		writeJSON(t, w, http.StatusOK, []map[string]any{{"number": 12, "title": "v1"}})
	})
	number, err := gh.resolveMilestone(context.Background(), "v1")
	if err != nil || number != 12 {
		t.Fatalf("expected the exact title (12) to win over the folded one, got %d (err %v)", number, err)
	}
}

// ============================================================================
// Repository identity and API base (BUG-844)
// ============================================================================

func TestGitHubDriver_RepositoryIdentity_Positive_ValidNamesReachPath(t *testing.T) {
	t.Setenv("GITHUB_REPOSITORY", "")
	gh := NewGitHubDriver("token", "")
	gh.SetRepository("a", ".github")
	path, err := gh.repoPath("issues")
	if err != nil || path != "/repos/a/.github/issues" {
		t.Fatalf("repoPath = %q, %v", path, err)
	}
}

func TestGitHubDriver_RepositoryIdentity_Negative_TraversalNeverSent(t *testing.T) {
	for _, identity := range [][2]string{
		{"..", "widgets"}, {"acme", ".."}, {"acme", "."}, {"acme/evil", "widgets"},
		{"acme", "widgets/../../users"}, {"acme", "widgets?admin=1"}, {"acme", "w#x"},
	} {
		gh, fake := newFakeForge(t, func(w http.ResponseWriter, r *http.Request, _ int) {
			t.Errorf("request %s %s sent for an invalid repository", r.Method, r.URL.RequestURI())
			writeJSON(t, w, http.StatusOK, []map[string]any{})
		})
		gh.SetRepository(identity[0], identity[1])
		if _, err := gh.ListIssues(context.Background(), "all"); err == nil {
			t.Fatalf("identity %q/%q accepted", identity[0], identity[1])
		}
		if _, err := gh.TargetRepository(); err == nil {
			t.Fatalf("TargetRepository accepted %q/%q", identity[0], identity[1])
		}
		if len(fake.requests) != 0 {
			t.Fatalf("identity %q/%q reached the forge", identity[0], identity[1])
		}
	}

	t.Setenv("GITHUB_REPOSITORY", "acme/../../orgs/x")
	env := NewGitHubDriver("token", "")
	if _, err := env.repoPath("issues"); err == nil || errors.Is(err, ErrRepositoryUnresolved) {
		t.Fatalf("an invalid GITHUB_REPOSITORY must be rejected as invalid, got %v", err)
	}
}

func TestGitHubDriver_APIBase_Boundary_WebOriginMapsToAPI(t *testing.T) {
	for endpoint, want := range map[string]string{
		"":                       "https://api.github.com",
		"https://github.com":     "https://api.github.com",
		"https://github.com/":    "https://api.github.com",
		"https://api.github.com": "https://api.github.com",
		"http://127.0.0.1:9/":    "http://127.0.0.1:9",
	} {
		if got := NewGitHubDriver("token", endpoint).Endpoint; got != want {
			t.Fatalf("NewGitHubDriver(%q).Endpoint = %q, want %q", endpoint, got, want)
		}
	}
}
