package forge

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/cordanaLLM/praetor/internal/config"
)

// recordedRulesetRequest captures one API call made by the driver.
type recordedRulesetRequest struct {
	Method        string
	Path          string
	Authorization string
	Body          map[string]any
}

// rulesetServer fakes the GitHub rulesets API on the loopback interface: GET on the
// collection lists the configured rulesets, GET on one ruleset returns the document last
// written to it (or its listing entry), a POST stores a new ruleset as id 99 and a PUT
// replaces one. A non-zero writeStatus answers every write with that status instead.
type rulesetServer struct {
	mu          sync.Mutex
	existing    []map[string]any
	stored      map[int]map[string]any
	requests    []recordedRulesetRequest
	writeStatus int
	srv         *httptest.Server
}

// createdRulesetID is the id rulesetServer assigns to a created ruleset.
const createdRulesetID = 99

func newRulesetServer(t *testing.T, existing []map[string]any, writeStatus int) *rulesetServer {
	t.Helper()
	rs := &rulesetServer{existing: existing, writeStatus: writeStatus}
	rs.srv = httptest.NewServer(http.HandlerFunc(rs.handle))
	t.Cleanup(rs.srv.Close)
	return rs
}

func (rs *rulesetServer) handle(w http.ResponseWriter, r *http.Request) {
	rs.mu.Lock()
	defer rs.mu.Unlock()

	var body map[string]any
	data, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if len(data) > 0 {
		if jerr := json.Unmarshal(data, &body); jerr != nil {
			http.Error(w, jerr.Error(), http.StatusBadRequest)
			return
		}
	}
	rs.requests = append(rs.requests, recordedRulesetRequest{
		Method: r.Method, Path: r.URL.RequestURI(), Authorization: r.Header.Get("Authorization"), Body: body,
	})
	rs.serve(w, r.Method, r.URL.Path, body)
}

// serve answers one rulesets API call; callers hold rs.mu or own rs exclusively.
func (rs *rulesetServer) serve(w http.ResponseWriter, method, path string, body map[string]any) {
	if rs.stored == nil {
		rs.stored = map[int]map[string]any{}
	}
	id := 0
	if _, after, found := strings.Cut(path, "/rulesets/"); found {
		if _, err := fmt.Sscanf(after, "%d", &id); err != nil {
			writeRulesetJSON(w, http.StatusNotFound, map[string]any{"message": "Not Found"})
			return
		}
	}
	if method == http.MethodGet {
		rs.serveRead(w, id)
		return
	}
	status := rs.writeStatus
	if status == 0 {
		status = http.StatusOK
		if method == http.MethodPost {
			status, id = http.StatusCreated, createdRulesetID
		}
	}
	if status >= http.StatusMultipleChoices {
		writeRulesetJSON(w, status, map[string]any{"message": "rejected"})
		return
	}
	doc := maps.Clone(body)
	if doc == nil {
		doc = map[string]any{}
	}
	doc["id"] = id
	rs.stored[id] = doc
	writeRulesetJSON(w, status, doc)
}

func (rs *rulesetServer) serveRead(w http.ResponseWriter, id int) {
	if id == 0 {
		writeRulesetJSON(w, http.StatusOK, rs.existing)
		return
	}
	if doc, found := rs.stored[id]; found {
		writeRulesetJSON(w, http.StatusOK, doc)
		return
	}
	for _, entry := range rs.existing {
		if fmt.Sprint(entry["id"]) == fmt.Sprint(id) {
			writeRulesetJSON(w, http.StatusOK, entry)
			return
		}
	}
	writeRulesetJSON(w, http.StatusNotFound, map[string]any{"message": "Not Found"})
}

func writeRulesetJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(payload); err != nil {
		return
	}
}

func (rs *rulesetServer) recorded() []recordedRulesetRequest {
	rs.mu.Lock()
	defer rs.mu.Unlock()
	return append([]recordedRulesetRequest{}, rs.requests...)
}

// rulesetRuleTypes extracts the rule type names from a recorded ruleset payload.
func rulesetRuleTypes(body map[string]any) []string {
	rules, ok := body["rules"].([]any)
	if !ok {
		return nil
	}
	types := make([]string, 0, len(rules))
	for _, raw := range rules {
		rule, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		if typ, ok := rule["type"].(string); ok {
			types = append(types, typ)
		}
	}
	return types
}

func TestReconcileProtection_Positive_CreateThenUpdate(t *testing.T) {
	ctx := context.Background()
	policy := &config.BranchProtectionPolicy{EnforceLinearHistory: true, RequireSignedCommits: false, RequiredApprovingReviewers: 2, DismissStaleReviews: true}

	// No ruleset yet: the driver lists, then creates.
	rs := newRulesetServer(t, nil, 0)
	gh := NewGitHubDriver("ghp_real", rs.srv.URL)
	gh.SetRepository("acme", "widgets")
	if err := gh.ReconcileProtection(ctx, "main", policy); err != nil {
		t.Fatalf("create: %v", err)
	}
	reqs := rs.recorded()
	if len(reqs) != 3 || reqs[2].Method != http.MethodGet || reqs[2].Path != "/repos/acme/widgets/rulesets/99" {
		t.Fatalf("expected GET + POST + readback GET, got %+v", reqs)
	}
	if reqs[0].Method != http.MethodGet || reqs[0].Path != "/repos/acme/widgets/rulesets?per_page=100&page=1" {
		t.Fatalf("unexpected listing request: %+v", reqs[0])
	}
	if reqs[1].Method != http.MethodPost || reqs[1].Path != "/repos/acme/widgets/rulesets" {
		t.Fatalf("unexpected create request: %+v", reqs[1])
	}
	if reqs[1].Body["name"] != "main-branch-protection" {
		t.Fatalf("unexpected ruleset name: %v", reqs[1].Body["name"])
	}
	types := rulesetRuleTypes(reqs[1].Body)
	if !containsString(types, "required_linear_history") || containsString(types, "required_signatures") {
		t.Fatalf("rules must follow the policy (linear=true, signed=false), got %v", types)
	}

	// An existing ruleset of the same name is updated in place, never duplicated.
	rs2 := newRulesetServer(t, []map[string]any{{"id": 7, "name": "main-branch-protection"}, {"id": 8, "name": "other"}}, 0)
	gh2 := NewGitHubDriver("ghp_real", rs2.srv.URL)
	gh2.SetRepository("acme", "widgets")
	signed := *policy
	signed.RequireSignedCommits = true
	if err := gh2.ReconcileProtection(ctx, "main", &signed); err != nil {
		t.Fatalf("update: %v", err)
	}
	reqs2 := rs2.recorded()
	if len(reqs2) != 4 || reqs2[1].Path != "/repos/acme/widgets/rulesets/7" || reqs2[2].Method != http.MethodPut ||
		reqs2[2].Path != "/repos/acme/widgets/rulesets/7" || reqs2[3].Method != http.MethodGet {
		t.Fatalf("expected list, live GET, PUT to ruleset 7 and readback, got %+v", reqs2)
	}
	if !containsString(rulesetRuleTypes(reqs2[2].Body), "required_signatures") {
		t.Fatalf("signed policy must emit required_signatures, got %v", rulesetRuleTypes(reqs2[2].Body))
	}
}

func TestReconcileProtection_Negative(t *testing.T) {
	ctx := context.Background()
	policy := &config.BranchProtectionPolicy{EnforceLinearHistory: true}

	// An API rejection surfaces as an error carrying the status.
	rs := newRulesetServer(t, nil, http.StatusForbidden)
	gh := NewGitHubDriver("ghp_real", rs.srv.URL)
	gh.SetRepository("acme", "widgets")
	err := gh.ReconcileProtection(ctx, "main", policy)
	if err == nil || !strings.Contains(err.Error(), "403") {
		t.Fatalf("expected a 403 error, got %v", err)
	}

	// An unset repository is never guessed: no request leaves the process.
	t.Setenv("GITHUB_REPOSITORY", "")
	rs2 := newRulesetServer(t, nil, 0)
	gh2 := NewGitHubDriver("ghp_real", rs2.srv.URL)
	if err := gh2.ReconcileProtection(ctx, "main", policy); !errors.Is(err, ErrRepositoryUnresolved) {
		t.Fatalf("expected ErrRepositoryUnresolved, got %v", err)
	}
	if _, err := gh2.ListIssues(ctx, "all"); !errors.Is(err, ErrRepositoryUnresolved) {
		t.Fatalf("ListIssues: expected ErrRepositoryUnresolved, got %v", err)
	}
	if err := gh2.ReconcileLabels(ctx, []Label{{Name: "x"}}); !errors.Is(err, ErrRepositoryUnresolved) {
		t.Fatalf("ReconcileLabels: expected ErrRepositoryUnresolved, got %v", err)
	}
	if n := len(rs2.recorded()); n != 0 {
		t.Fatalf("expected no requests for an unset repository, got %d", n)
	}

	// An empty token cannot authenticate.
	if err := NewGitHubDriver("", rs2.srv.URL).ReconcileProtection(ctx, "main", policy); err == nil {
		t.Fatal("expected an authentication error for an empty token")
	}

	// A malformed listing is an error rather than a silent create.
	broken := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if _, werr := w.Write([]byte("not json")); werr != nil {
			return
		}
	}))
	t.Cleanup(broken.Close)
	gh3 := NewGitHubDriver("ghp_real", broken.URL)
	gh3.SetRepository("acme", "widgets")
	if err := gh3.ReconcileProtection(ctx, "main", policy); err == nil || !strings.Contains(err.Error(), "parsing repository rulesets") {
		t.Fatalf("expected a parse error, got %v", err)
	}
}

func TestReconcileProtection_Boundary(t *testing.T) {
	ctx := context.Background()
	policy := &config.BranchProtectionPolicy{}
	rs := newRulesetServer(t, nil, 0)

	// Input validation happens before any request.
	live := NewGitHubDriver("ghp_real", rs.srv.URL)
	live.SetRepository("acme", "widgets")
	if err := live.ReconcileProtection(ctx, "", policy); err == nil {
		t.Fatal("expected an error for an empty branch")
	}
	if err := live.ReconcileProtection(ctx, "main", nil); err == nil {
		t.Fatal("expected an error for a nil policy")
	}
	if n := len(rs.recorded()); n != 0 {
		t.Fatalf("expected no requests before validation passes, got %d", n)
	}

	// GITHUB_REPOSITORY is the only fallback, and a minimal policy emits neither
	// optional rule.
	t.Setenv("GITHUB_REPOSITORY", "env-org/env-repo")
	envDriver := NewGitHubDriver("ghp_real", rs.srv.URL)
	if err := envDriver.ReconcileProtection(ctx, "release", policy); err != nil {
		t.Fatalf("env fallback: %v", err)
	}
	reqs := rs.recorded()
	if len(reqs) != 3 || reqs[1].Path != "/repos/env-org/env-repo/rulesets" {
		t.Fatalf("expected a create on env-org/env-repo, got %+v", reqs)
	}
	types := rulesetRuleTypes(reqs[1].Body)
	if containsString(types, "required_linear_history") || containsString(types, "required_signatures") {
		t.Fatalf("minimal policy must emit neither optional rule, got %v", types)
	}
}

func TestReconcileProtectionTokensDoNotBypassRequests(t *testing.T) {
	for _, token := range []string{"test-token", "ordinary-token"} {
		t.Run(token, func(t *testing.T) {
			rs := newRulesetServer(t, nil, 0)
			gh := NewGitHubDriver(token, rs.srv.URL)
			gh.SetRepository("acme", "widgets")
			if err := gh.ReconcileProtection(context.Background(), "main", &config.BranchProtectionPolicy{}); err != nil {
				t.Fatalf("reconcile: %v", err)
			}
			reqs := rs.recorded()
			if len(reqs) != 3 || reqs[0].Method != http.MethodGet || reqs[1].Method != http.MethodPost || reqs[2].Method != http.MethodGet {
				t.Fatalf("every token must perform the real GET, POST and readback GET, got %+v", reqs)
			}
			for _, req := range reqs {
				if req.Authorization != "Bearer "+token {
					t.Fatalf("unexpected authentication header: %q", req.Authorization)
				}
			}
		})
	}
}

func TestReconcileProtectionRejectsRulesetsAboveBound(t *testing.T) {
	existing := make([]map[string]any, issuesPerPage+1)
	for i := range existing {
		existing[i] = map[string]any{"id": i + 1, "name": "other"}
	}
	// The desired ruleset lies beyond the bound: silently truncating the response
	// would incorrectly create another ruleset instead of reporting incomplete data.
	existing[issuesPerPage]["name"] = "main-branch-protection"
	rs := newRulesetServer(t, existing, 0)
	gh := NewGitHubDriver("ordinary-token", rs.srv.URL)
	gh.SetRepository("acme", "widgets")
	err := gh.ReconcileProtection(context.Background(), "main", &config.BranchProtectionPolicy{})
	if err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("expected an explicit listing-bound error, got %v", err)
	}
	if reqs := rs.recorded(); len(reqs) != 1 || reqs[0].Method != http.MethodGet {
		t.Fatalf("oversize listing must fail before any mutation, got %+v", reqs)
	}
}

func TestGitHubDriverUsesDedicatedBoundedClient(t *testing.T) {
	gh := NewGitHubDriver("ordinary-token", "http://127.0.0.1")
	if gh.HTTPClient == nil || gh.HTTPClient == http.DefaultClient || gh.HTTPClient.Timeout != defaultHTTPTimeout {
		t.Fatalf("driver must own a client with its bounded timeout, got %+v", gh.HTTPClient)
	}
}
