package forge

import (
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/config"
)

// LiveRuleset returns the named ruleset as GitHub reports it, so a caller can see a required
// check the merge kept although it no longer asked for it.
//
// Positive: the praetor ruleset is listed and read back, and its required check is visible.
// Boundary: no ruleset of that name is nil without an error, after the listing alone.
// Negative: an empty branch, an empty token and a failed read are errors, never a nil ruleset.
func TestGitHubDriver_LiveRuleset(t *testing.T) {
	live := map[string]any{
		"id": 7, "name": RepositoryRulesetName, "source_type": "Repository", "source": "acme/widgets",
		"rules": []any{map[string]any{"type": "required_status_checks", "parameters": map[string]any{
			"required_status_checks": []any{map[string]any{"context": "Platform Neutrality (Linux)"}},
		}}},
	}
	gh, fake := rulesetForge(t, &rulesetServer{existing: []map[string]any{{"id": 3, "name": "other"}, live}})
	gh.RulesetName = RepositoryRulesetName
	body, err := gh.LiveRuleset(t.Context(), "main")
	if err != nil {
		t.Fatalf("LiveRuleset: %v", err)
	}
	required, err := RulesetRequiresStatusContext(body, "Platform Neutrality (Linux)")
	if err != nil || !required {
		t.Fatalf("live ruleset requires the leg = %v (%v), want true: %s", required, err, body)
	}
	if len(fake.requests) != 2 || fake.requests[1].Path != "/repos/acme/widgets/rulesets/7" {
		t.Fatalf("expected a listing and one read, got %+v", fake.requests)
	}

	absent, fake := rulesetForge(t, &rulesetServer{existing: []map[string]any{{"id": 3, "name": "other"}}})
	absent.RulesetName = RepositoryRulesetName
	if body, err := absent.LiveRuleset(t.Context(), "main"); err != nil || body != nil {
		t.Fatalf("absent ruleset = %q (%v), want nil without an error", body, err)
	}
	if len(fake.requests) != 1 {
		t.Fatalf("absent ruleset: expected the listing alone, got %+v", fake.requests)
	}

	if _, err := gh.LiveRuleset(t.Context(), ""); err == nil {
		t.Error("an empty branch was accepted")
	}
	tokenless := NewGitHubDriver("", "http://127.0.0.1:1")
	tokenless.SetRepository("acme", "widgets")
	if _, err := tokenless.LiveRuleset(t.Context(), "main"); err == nil {
		t.Error("an empty token was accepted")
	}
	failing, _ := newFakeForge(t, func(w http.ResponseWriter, r *http.Request, _ int) {
		if strings.HasSuffix(r.URL.Path, "/rulesets") {
			writeJSON(t, w, http.StatusOK, []map[string]any{{"id": 7, "name": RepositoryRulesetName, "source_type": "Repository", "source": "acme/widgets"}})
			return
		}
		writeJSON(t, w, http.StatusInternalServerError, map[string]any{"message": "boom"})
	})
	failing.RulesetName = RepositoryRulesetName
	if body, err := failing.LiveRuleset(t.Context(), "main"); err == nil || !strings.Contains(err.Error(), "unexpected status 500") {
		t.Fatalf("failed read = %q (%v), want the status 500 error", body, err)
	}
}

// managedRulesetCase describes a complete listing and its only safe managed ruleset id.
type managedRulesetCase struct {
	name    string
	entries []map[string]any
	id      int
	err     string
}

// managedRulesetCases covers ownership, unread scope and ambiguous repository names.
func managedRulesetCases() []managedRulesetCase {
	parent := map[string]any{"id": 1, "name": RepositoryRulesetName, "source_type": "Organization", "source": "acme", "bypass_actors": []any{}}
	repo := map[string]any{"id": 2, "name": RepositoryRulesetName, "source_type": "Repository", "source": "acme/widgets",
		"bypass_actors": []any{map[string]any{"actor_id": 42, "actor_type": "Team", "bypass_mode": "always"}}}
	return []managedRulesetCase{
		{"parent first", []map[string]any{parent, repo}, 2, ""},
		{"repository first", []map[string]any{repo, parent}, 2, ""},
		{"only inherited", []map[string]any{parent}, 0, ""},
		{"only repository", []map[string]any{repo}, 2, ""},
		{"unread scope", []map[string]any{{"id": 3, "name": RepositoryRulesetName, "bypass_actors": []any{}}}, 0, "unread scope"},
		{"unread source", []map[string]any{{"id": 3, "name": RepositoryRulesetName, "source_type": "Repository"}}, 0, "unread scope"},
		{"unread source type", []map[string]any{{"id": 3, "name": RepositoryRulesetName, "source": "acme/widgets"}}, 0, "unread scope"},
		{"another repository", []map[string]any{{"id": 3, "name": RepositoryRulesetName, "source_type": "Repository", "source": "acme/other"}}, 0, ""},
		{"duplicate repository names", []map[string]any{repo,
			{"id": 4, "name": RepositoryRulesetName, "source_type": "Repository", "source": "acme/widgets"}}, 0, "ids 2 and 4"},
	}
}

// TestGitHubDriver_LiveRuleset_Scope compares actors only from an unambiguous repository ruleset.
func TestGitHubDriver_LiveRuleset_Scope(t *testing.T) {
	cases := managedRulesetCases()
	for i := 0; i < len(cases) && i < 9; i++ {
		c := cases[i]
		t.Run(c.name, func(t *testing.T) {
			gh, fake := rulesetForge(t, &rulesetServer{existing: c.entries})
			gh.RulesetName = RepositoryRulesetName
			body, err := gh.LiveRuleset(t.Context(), "main")
			assertManagedRulesetRead(t, c, body, err, fake)
		})
	}
}

// assertManagedRulesetRefusal requires a diagnostic before any detail read or mutation.
func assertManagedRulesetRefusal(t *testing.T, fake *fakeForgeServer, err error, diagnostic string) bool {
	t.Helper()
	if diagnostic == "" {
		return false
	}
	if err == nil || !strings.Contains(err.Error(), diagnostic) {
		t.Fatalf("error %v; want %s", err, diagnostic)
	}
	if len(fake.requests) != 1 {
		t.Fatalf("refused lookup sent more than the listing request: %+v", fake.requests)
	}
	return true
}

// assertManagedRulesetRead checks absence or the repository's actor drift and detail path.
func assertManagedRulesetRead(t *testing.T, c managedRulesetCase, body []byte, err error, fake *fakeForgeServer) {
	t.Helper()
	if assertManagedRulesetRefusal(t, fake, err, c.err) {
		return
	}
	if err != nil || (body == nil) != (c.id == 0) {
		t.Fatalf("body %s, err %v; want id %d", body, err, c.id)
	}
	if c.id == 0 {
		return
	}
	finding, err := EvaluateBypassActors(config.BranchProtectionPolicy{}, body)
	if err != nil || finding.Verdict != ProtectionDrift || finding.Live != "[Team 42 always]" {
		t.Fatalf("repository actors hidden: %+v, %v", finding, err)
	}
	if len(fake.requests) != 2 || fake.requests[1].Path != "/repos/acme/widgets/rulesets/2" {
		t.Fatalf("wrong ruleset read: %+v", fake.requests)
	}
}

// TestGitHubDriver_ReconcileProtection_Scope never updates an inherited or ambiguous ruleset.
func TestGitHubDriver_ReconcileProtection_Scope(t *testing.T) {
	cases := managedRulesetCases()
	for i := 0; i < len(cases) && i < 9; i++ {
		c := cases[i]
		t.Run(c.name, func(t *testing.T) {
			gh, fake := rulesetForge(t, &rulesetServer{existing: c.entries})
			gh.RulesetName = RepositoryRulesetName
			err := gh.ReconcileProtection(t.Context(), "main", &config.BranchProtectionPolicy{})
			if assertManagedRulesetRefusal(t, fake, err, c.err) {
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			write := fake.requests[1]
			wantMethod, wantPath := http.MethodPost, "/repos/acme/widgets/rulesets"
			if c.id != 0 {
				write = fake.requests[2]
				wantMethod, wantPath = http.MethodPut, fmt.Sprintf("%s/%d", wantPath, c.id)
			}
			if write.Method != wantMethod || write.Path != wantPath {
				t.Fatalf("mutation %s %s; want %s %s", write.Method, write.Path, wantMethod, wantPath)
			}
		})
	}
}
