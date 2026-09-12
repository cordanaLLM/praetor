package forge

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/cordanaLLM/praetor/internal/config"
)

// recordedRequest captures one API call made by the driver.
type recordedRequest struct {
	Method string
	Path   string
	Body   map[string]any
}

// rulesetServer fakes the GitHub rulesets API on the loopback interface: GET lists the
// configured rulesets, writes are recorded and answered with writeStatus.
type rulesetServer struct {
	mu          sync.Mutex
	existing    []map[string]any
	requests    []recordedRequest
	writeStatus int
	srv         *httptest.Server
}

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
	rs.requests = append(rs.requests, recordedRequest{Method: r.Method, Path: r.URL.RequestURI(), Body: body})

	w.Header().Set("Content-Type", "application/json")
	if r.Method == http.MethodGet {
		if encErr := json.NewEncoder(w).Encode(rs.existing); encErr != nil {
			http.Error(w, encErr.Error(), http.StatusInternalServerError)
		}
		return
	}
	status := rs.writeStatus
	if status == 0 {
		status = http.StatusOK
		if r.Method == http.MethodPost {
			status = http.StatusCreated
		}
	}
	w.WriteHeader(status)
	if _, werr := w.Write([]byte(`{"id": 99}`)); werr != nil {
		return
	}
}

func (rs *rulesetServer) recorded() []recordedRequest {
	rs.mu.Lock()
	defer rs.mu.Unlock()
	return append([]recordedRequest{}, rs.requests...)
}

// ruleTypes extracts the rule type names from a recorded ruleset payload.
func ruleTypes(body map[string]any) []string {
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

func containsString(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
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
	if len(reqs) != 2 {
		t.Fatalf("expected GET + POST, got %+v", reqs)
	}
	if reqs[0].Method != http.MethodGet || reqs[0].Path != "/repos/acme/widgets/rulesets?per_page=100" {
		t.Fatalf("unexpected listing request: %+v", reqs[0])
	}
	if reqs[1].Method != http.MethodPost || reqs[1].Path != "/repos/acme/widgets/rulesets" {
		t.Fatalf("unexpected create request: %+v", reqs[1])
	}
	if reqs[1].Body["name"] != "main-branch-protection" {
		t.Fatalf("unexpected ruleset name: %v", reqs[1].Body["name"])
	}
	types := ruleTypes(reqs[1].Body)
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
	if len(reqs2) != 2 || reqs2[1].Method != http.MethodPut || reqs2[1].Path != "/repos/acme/widgets/rulesets/7" {
		t.Fatalf("expected PUT to ruleset 7, got %+v", reqs2)
	}
	if !containsString(ruleTypes(reqs2[1].Body), "required_signatures") {
		t.Fatalf("signed policy must emit required_signatures, got %v", ruleTypes(reqs2[1].Body))
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
	if err := gh2.ReconcileProtection(ctx, "main", policy); !errors.Is(err, ErrRepositoryUnset) {
		t.Fatalf("expected ErrRepositoryUnset, got %v", err)
	}
	if _, err := gh2.ListIssues(ctx, "all"); !errors.Is(err, ErrRepositoryUnset) {
		t.Fatalf("ListIssues: expected ErrRepositoryUnset, got %v", err)
	}
	if err := gh2.ReconcileLabels(ctx, []Label{{Name: "x"}}); !errors.Is(err, ErrRepositoryUnset) {
		t.Fatalf("ReconcileLabels: expected ErrRepositoryUnset, got %v", err)
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
	if err := gh3.ReconcileProtection(ctx, "main", policy); err == nil || !strings.Contains(err.Error(), "parsing rulesets") {
		t.Fatalf("expected a parse error, got %v", err)
	}
}

func TestReconcileProtection_Boundary(t *testing.T) {
	ctx := context.Background()
	policy := &config.BranchProtectionPolicy{}
	rs := newRulesetServer(t, nil, 0)

	// A fixture token constructs a dry-run driver that performs no request.
	fixture := NewGitHubDriver(FixtureTokenPrefix+"token", rs.srv.URL)
	fixture.SetRepository("acme", "widgets")
	if !fixture.DryRun {
		t.Fatal("fixture token must select DryRun")
	}
	if err := fixture.ReconcileProtection(ctx, "main", policy); err != nil {
		t.Fatalf("dry run: %v", err)
	}

	// DryRun can be set explicitly on a real token.
	explicit := NewGitHubDriver("ghp_real", rs.srv.URL)
	explicit.DryRun = true
	explicit.SetRepository("acme", "widgets")
	if err := explicit.ReconcileProtection(ctx, "main", policy); err != nil {
		t.Fatalf("explicit dry run: %v", err)
	}

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
	if len(reqs) != 2 || reqs[1].Path != "/repos/env-org/env-repo/rulesets" {
		t.Fatalf("expected a create on env-org/env-repo, got %+v", reqs)
	}
	types := ruleTypes(reqs[1].Body)
	if containsString(types, "required_linear_history") || containsString(types, "required_signatures") {
		t.Fatalf("minimal policy must emit neither optional rule, got %v", types)
	}
}
